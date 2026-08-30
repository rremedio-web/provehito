// Package fingerprint freezes deterministic identities for Git workspaces.
package fingerprint

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/provehito-project/provehito/core/clock"
	"github.com/provehito-project/provehito/core/failure"
)

// Fingerprint identifies the exact Git candidate and the bytes changed from
// its base. FrozenAt is deliberately excluded from EquivalentHash.
type Fingerprint struct {
	BaseCommit     string    `json:"base_commit"`
	HeadCommit     string    `json:"head_commit"`
	HeadTree       string    `json:"head_tree"`
	DiffHash       string    `json:"diff_hash"`
	ManifestHash   string    `json:"manifest_hash"`
	FrozenAt       time.Time `json:"frozen_at"`
	EquivalentHash string    `json:"equivalent_hash"`
}

// GitProvider inspects and freezes Git candidates through Runner.
type GitProvider struct {
	Runner Runner
	Clock  clock.Clock
}

// ProviderOption configures a GitProvider.
type ProviderOption func(*GitProvider)

// WithRunner injects a process runner, primarily for adapter-boundary tests.
func WithRunner(r Runner) ProviderOption { return func(p *GitProvider) { p.Runner = r } }

// WithClock injects the timestamp source used by Freeze.
func WithClock(c clock.Clock) ProviderOption { return func(p *GitProvider) { p.Clock = c } }

// NewGitProvider returns a provider using the standard Git runner and system
// UTC clock. Options are applied in order.
func NewGitProvider(options ...ProviderOption) *GitProvider {
	p := &GitProvider{Runner: NewExecRunner(), Clock: clock.System{}}
	for _, option := range options {
		if option != nil {
			option(p)
		}
	}
	return p
}

// Inspect verifies workspace cleanliness and returns its Git identity. It
// does not bind a manifest or timestamp.
func (p *GitProvider) Inspect(ctx context.Context, workspace, base string) (Fingerprint, error) {
	if p == nil || p.Runner == nil {
		return Fingerprint{}, failure.New(failure.ToolingOrAdapter, "git runner")
	}
	if workspace == "" || base == "" {
		return Fingerprint{}, failure.New(failure.Integrity, "git inspect arguments")
	}
	if err := p.requireClean(ctx, workspace); err != nil {
		return Fingerprint{}, err
	}
	baseCommit, err := p.resolve(ctx, workspace, base+"^{commit}")
	if err != nil {
		return Fingerprint{}, err
	}
	headCommit, err := p.resolve(ctx, workspace, "HEAD^{commit}")
	if err != nil {
		return Fingerprint{}, err
	}
	headTree, err := p.resolve(ctx, workspace, "HEAD^{tree}")
	if err != nil {
		return Fingerprint{}, err
	}
	diff, err := p.run(ctx, workspace, "diff", "--no-ext-diff", "--no-textconv", "--binary", "--full-index", "--find-renames", baseCommit+".."+headCommit)
	if err != nil {
		return Fingerprint{}, err
	}
	digest := sha256.Sum256(diff)
	f := Fingerprint{
		BaseCommit: baseCommit,
		HeadCommit: headCommit,
		HeadTree:   headTree,
		DiffHash:   hex.EncodeToString(digest[:]),
	}
	return f, nil
}

// Freeze verifies a clean workspace and binds the identity to a valid
// manifest hash and injected UTC timestamp.
func (p *GitProvider) Freeze(ctx context.Context, workspace, base, manifestHash string) (Fingerprint, error) {
	if !failure.IsHash(manifestHash) {
		return Fingerprint{}, failure.New(failure.Integrity, "freeze manifest hash")
	}
	f, err := p.Inspect(ctx, workspace, base)
	if err != nil {
		return Fingerprint{}, err
	}
	if p.Clock == nil {
		return Fingerprint{}, failure.New(failure.ToolingOrAdapter, "freeze clock")
	}
	f.ManifestHash = manifestHash
	f.FrozenAt = p.Clock.Now().UTC().Truncate(time.Second)
	f.EquivalentHash = equivalentHash(f)
	return f, nil
}

func (p *GitProvider) requireClean(ctx context.Context, workspace string) error {
	status, err := p.run(ctx, workspace, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return err
	}
	dirty, err := parseStatus(status)
	if err != nil {
		return failure.Wrap(failure.ToolingOrAdapter, "git status output", err)
	}
	if dirty {
		return failure.New(failure.WorkspaceDrift, "git workspace dirty")
	}
	submodules, err := p.run(ctx, workspace, "submodule", "status", "--recursive")
	if err != nil {
		return err
	}
	dirty, err = parseSubmoduleStatus(submodules)
	if err != nil {
		return failure.Wrap(failure.ToolingOrAdapter, "git submodule output", err)
	}
	if dirty {
		return failure.New(failure.WorkspaceDrift, "git submodule present")
	}
	return nil
}

func (p *GitProvider) resolve(ctx context.Context, workspace, rev string) (string, error) {
	out, err := p.run(ctx, workspace, "rev-parse", "--verify", "--end-of-options", rev)
	if err != nil {
		return "", err
	}
	value, ok := parseObjectID(out)
	if !ok {
		return "", failure.New(failure.ToolingOrAdapter, "git resolve object")
	}
	return value, nil
}

func (p *GitProvider) run(ctx context.Context, workspace string, args ...string) ([]byte, error) {
	out, err := p.Runner.Run(ctx, workspace, args...)
	if err != nil {
		return nil, failure.Wrap(failure.ToolingOrAdapter, "git command", err)
	}
	return out, nil
}

func parseStatus(data []byte) (bool, error) {
	if len(data) == 0 {
		return false, nil
	}
	if data[len(data)-1] != 0 {
		return false, fmt.Errorf("status output is not NUL terminated")
	}
	records := bytes.Split(data[:len(data)-1], []byte{0})
	dirty := false
	for i := 0; i < len(records); i++ {
		record := records[i]
		if len(record) < 4 || record[2] != ' ' || !validStatusCode(record[0]) || !validStatusCode(record[1]) || record[0] == ' ' && record[1] == ' ' || record[0] == '?' && record[1] != '?' || record[1] == '?' && record[0] != '?' || record[0] == '!' && record[1] != '!' || record[1] == '!' && record[0] != '!' || len(record[3:]) == 0 {
			return false, fmt.Errorf("malformed status record")
		}
		dirty = true
		if record[0] == 'R' || record[0] == 'C' || record[1] == 'R' || record[1] == 'C' {
			if i+1 >= len(records) || len(records[i+1]) == 0 {
				return false, fmt.Errorf("rename or copy record missing second path")
			}
			i++
		}
	}
	return dirty, nil
}

func validStatusCode(code byte) bool {
	switch code {
	case ' ', 'M', 'T', 'A', 'D', 'R', 'C', 'U', '?', '!':
		return true
	default:
		return false
	}
}

func parseSubmoduleStatus(data []byte) (bool, error) {
	if len(data) == 0 {
		return false, nil
	}
	if data[len(data)-1] != '\n' || len(data) > 1 && data[len(data)-2] == '\n' {
		return false, fmt.Errorf("submodule output must have exactly one final LF")
	}
	data = data[:len(data)-1]
	if bytes.IndexByte(data, 0) >= 0 {
		return false, fmt.Errorf("submodule output contains NUL")
	}
	lines := bytes.Split(data, []byte{'\n'})
	for _, line := range lines {
		if len(line) == 0 || strings.TrimSpace(string(line)) == "" || !validSubmoduleLine(line) {
			return false, fmt.Errorf("malformed submodule status line")
		}
	}
	return true, nil
}

func validSubmoduleLine(line []byte) bool {
	if len(line) < 2 || (line[0] != ' ' && line[0] != '+' && line[0] != '-' && line[0] != 'U') {
		return false
	}
	rest := line[1:]
	for _, size := range []int{40, 64} {
		if len(rest) <= size || rest[size] != ' ' || !isObjectID(string(rest[:size])) {
			continue
		}
		path := rest[size+1:]
		if len(bytes.TrimSpace(path)) == 0 {
			return false
		}
		for _, char := range path {
			if char < 0x20 || char == 0x7f {
				return false
			}
		}
		return true
	}
	return false
}

func parseObjectID(data []byte) (string, bool) {
	if len(data) == 0 || data[len(data)-1] != '\n' || bytes.Count(data, []byte{'\n'}) != 1 {
		return "", false
	}
	value := string(data[:len(data)-1])
	return value, isObjectID(value)
}

func equivalentHash(f Fingerprint) string {
	identity := struct {
		BaseCommit   string `json:"base_commit"`
		HeadCommit   string `json:"head_commit"`
		HeadTree     string `json:"head_tree"`
		DiffHash     string `json:"diff_hash"`
		ManifestHash string `json:"manifest_hash"`
	}{f.BaseCommit, f.HeadCommit, f.HeadTree, f.DiffHash, f.ManifestHash}
	bytes, err := json.Marshal(identity)
	if err != nil {
		panic(fmt.Sprintf("fingerprint identity marshal: %v", err))
	}
	digest := sha256.Sum256(bytes)
	return hex.EncodeToString(digest[:])
}

func isObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}
