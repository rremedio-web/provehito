package fingerprint_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/provehito-project/provehito/core/clock"
	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/fingerprint"
)

const validManifestHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestFreezeRejectsUntrackedFile(t *testing.T) {
	repo := newGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := fingerprint.NewGitProvider().Freeze(context.Background(), repo, "HEAD~1", validManifestHash)
	if failure.ExitCodeFor(err) != 30 {
		t.Fatalf("expected drift failure, got %v", err)
	}
}

func TestFreezeIsStable(t *testing.T) {
	repo := newGitRepo(t)
	a, err := fingerprint.NewGitProvider().Freeze(context.Background(), repo, "HEAD~1", validManifestHash)
	if err != nil {
		t.Fatal(err)
	}
	b, err := fingerprint.NewGitProvider().Freeze(context.Background(), repo, "HEAD~1", validManifestHash)
	if err != nil {
		t.Fatal(err)
	}
	if a.EquivalentHash != b.EquivalentHash {
		t.Fatalf("%#v %#v", a, b)
	}
}

func TestFreezeRejectsTrackedEdit(t *testing.T) {
	repo := newGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("uncommitted\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := fingerprint.NewGitProvider().Freeze(context.Background(), repo, "HEAD~1", validManifestHash)
	if failure.ExitCodeFor(err) != 30 {
		t.Fatalf("expected tracked drift, got %v", err)
	}
}

func TestFreezeRejectsInvalidManifestHash(t *testing.T) {
	repo := newGitRepo(t)
	for _, value := range []string{"", "not-a-hash", strings.Repeat("A", 64), strings.Repeat("a", 63)} {
		_, err := fingerprint.NewGitProvider().Freeze(context.Background(), repo, "HEAD~1", value)
		if failure.ExitCodeFor(err) != 60 {
			t.Errorf("manifest %q: expected integrity failure, got %v", value, err)
		}
	}
}

func TestFreezeUsesFullObjectIDsAndFixedClock(t *testing.T) {
	repo := newGitRepo(t)
	wantTime := time.Date(2026, 8, 19, 12, 34, 56, 987654321, time.FixedZone("west", -2*60*60))
	p := fingerprint.NewGitProvider(fingerprint.WithClock(clock.Fixed{Time: wantTime}))
	got, err := p.Freeze(context.Background(), repo, "HEAD~1", validManifestHash)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"base":       got.BaseCommit,
		"head":       got.HeadCommit,
		"tree":       got.HeadTree,
		"diff":       got.DiffHash,
		"manifest":   got.ManifestHash,
		"equivalent": got.EquivalentHash,
	} {
		if len(value) != 64 && name != "base" && name != "head" && name != "tree" {
			t.Errorf("%s hash length: got %d", name, len(value))
		}
	}
	if len(got.BaseCommit) != 40 || len(got.HeadCommit) != 40 || len(got.HeadTree) != 40 {
		t.Fatalf("expected full SHA-1 IDs: %#v", got)
	}
	wantUTC := wantTime.UTC().Truncate(time.Second)
	if !got.FrozenAt.Equal(wantUTC) || got.FrozenAt.Location() != time.UTC {
		t.Fatalf("freeze time: got %s want %s UTC", got.FrozenAt, wantUTC)
	}
	t.Logf("full IDs: base=%s head=%s tree=%s", got.BaseCommit, got.HeadCommit, got.HeadTree)
}

func TestInspectNeverBindsManifestTimeOrEquivalentHash(t *testing.T) {
	runner := scriptedRunner{}
	got, err := fingerprint.NewGitProvider(
		fingerprint.WithRunner(&runner),
		fingerprint.WithClock(clock.Fixed{Time: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)}),
	).Inspect(context.Background(), t.TempDir(), "HEAD~1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ManifestHash != "" || !got.FrozenAt.IsZero() || got.EquivalentHash != "" {
		t.Fatalf("Inspect bound freeze-only fields: %#v", got)
	}
}

func TestMalformedStatusOutputIsTooling(t *testing.T) {
	runner := scriptedRunner{status: []byte("X malformed\x00")}
	_, err := fingerprint.NewGitProvider(fingerprint.WithRunner(&runner)).Inspect(context.Background(), t.TempDir(), "HEAD~1")
	if failure.ExitCodeFor(err) != 40 {
		t.Fatalf("expected malformed status tooling failure, got %v", err)
	}
}

func TestMalformedStatusXYIsTooling(t *testing.T) {
	runner := scriptedRunner{status: []byte("?M malformed\x00")}
	_, err := fingerprint.NewGitProvider(fingerprint.WithRunner(&runner)).Inspect(context.Background(), t.TempDir(), "HEAD~1")
	if failure.ExitCodeFor(err) != 40 {
		t.Fatalf("expected malformed XY tooling failure, got %v", err)
	}
}

func TestValidStatusRecordsIncludingRenameAreWorkspaceDrift(t *testing.T) {
	runner := scriptedRunner{status: []byte("R  renamed.txt\x00original.txt\x00")}
	_, err := fingerprint.NewGitProvider(fingerprint.WithRunner(&runner)).Inspect(context.Background(), t.TempDir(), "HEAD~1")
	if failure.ExitCodeFor(err) != 30 {
		t.Fatalf("expected valid rename status drift, got %v", err)
	}
}

func TestMalformedSubmoduleOutputIsTooling(t *testing.T) {
	runner := scriptedRunner{submodule: []byte("   \n")}
	_, err := fingerprint.NewGitProvider(fingerprint.WithRunner(&runner)).Inspect(context.Background(), t.TempDir(), "HEAD~1")
	if failure.ExitCodeFor(err) != 40 {
		t.Fatalf("expected malformed submodule tooling failure, got %v", err)
	}
}

func TestValidSubmoduleOutputIsWorkspaceDrift(t *testing.T) {
	runner := scriptedRunner{submodule: []byte(" " + strings.Repeat("a", 40) + " nested\n")}
	_, err := fingerprint.NewGitProvider(fingerprint.WithRunner(&runner)).Inspect(context.Background(), t.TempDir(), "HEAD~1")
	if failure.ExitCodeFor(err) != 30 {
		t.Fatalf("expected valid submodule drift, got %v", err)
	}
}

func TestValidSHA256SubmoduleOutputIsWorkspaceDrift(t *testing.T) {
	runner := scriptedRunner{submodule: []byte("+" + strings.Repeat("b", 64) + " nested module\n")}
	_, err := fingerprint.NewGitProvider(fingerprint.WithRunner(&runner)).Inspect(context.Background(), t.TempDir(), "HEAD~1")
	if failure.ExitCodeFor(err) != 30 {
		t.Fatalf("expected valid SHA-256 submodule drift, got %v", err)
	}
}

func TestMalformedSubmoduleOutputFormsAreTooling(t *testing.T) {
	base := strings.Repeat("a", 40)
	for name, output := range map[string][]byte{
		"missing final LF": []byte(" " + base + " nested"),
		"extra empty line": []byte(" " + base + " nested\n\n"),
		"embedded NUL":     []byte(" " + base + " nested\x00\n"),
		"ASCII control":    []byte(" " + base + " nested\x01\n"),
	} {
		t.Run(name, func(t *testing.T) {
			runner := scriptedRunner{submodule: output}
			_, err := fingerprint.NewGitProvider(fingerprint.WithRunner(&runner)).Inspect(context.Background(), t.TempDir(), "HEAD~1")
			if failure.ExitCodeFor(err) != 40 {
				t.Fatalf("expected malformed submodule tooling failure, got %v", err)
			}
		})
	}
}

func TestMalformedRevParseOutputIsTooling(t *testing.T) {
	runner := scriptedRunner{rev: []byte(" " + strings.Repeat("a", 40) + "\n")}
	_, err := fingerprint.NewGitProvider(fingerprint.WithRunner(&runner)).Inspect(context.Background(), t.TempDir(), "HEAD~1")
	if failure.ExitCodeFor(err) != 40 {
		t.Fatalf("expected malformed rev-parse tooling failure, got %v", err)
	}
}

func TestRevParseRequiresExactlyOneTrailingLF(t *testing.T) {
	for name, output := range map[string][]byte{
		"missing LF":   []byte(strings.Repeat("a", 40)),
		"CRLF":         []byte(strings.Repeat("a", 40) + "\r\n"),
		"extra LF":     []byte(strings.Repeat("a", 40) + "\n\n"),
		"extra data":   []byte(strings.Repeat("a", 40) + "\nextra"),
		"trailing tab": []byte(strings.Repeat("a", 40) + "\t\n"),
	} {
		t.Run(name, func(t *testing.T) {
			runner := scriptedRunner{rev: output}
			_, err := fingerprint.NewGitProvider(fingerprint.WithRunner(&runner)).Inspect(context.Background(), t.TempDir(), "HEAD~1")
			if failure.ExitCodeFor(err) != 40 {
				t.Fatalf("expected malformed rev-parse tooling failure, got %v", err)
			}
		})
	}
}

func TestOptionLookingBaseCannotAlterRevParseArguments(t *testing.T) {
	repo := newGitRepo(t)
	_, err := fingerprint.NewGitProvider().Inspect(context.Background(), repo, "--output=/tmp/provehito-option")
	if failure.ExitCodeFor(err) != 40 {
		t.Fatalf("expected option-looking base tooling failure, got %v", err)
	}
}

func TestFsmonitorConfigDoesNotExecute(t *testing.T) {
	repo := newGitRepo(t)
	root := t.TempDir()
	marker := filepath.Join(root, "fsmonitor-invoked")
	script := filepath.Join(root, "fsmonitor-hook")
	contents := "#!/bin/sh\nprintf invoked > " + marker + "\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "config", "core.fsmonitor", script)
	if _, err := fingerprint.NewGitProvider().Freeze(context.Background(), repo, "HEAD~1", validManifestHash); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fsmonitor hook executed and created marker: %v", err)
	}
}

func TestExecRunnerAppliesGitHardeningFlagsAndEnvironment(t *testing.T) {
	repo := newGitRepo(t)
	root := t.TempDir()
	capture := filepath.Join(root, "probe.txt")
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "git-probe")
	contents := "#!/bin/sh\nprintf '%s\\n' \"$*\" > " + capture + ".args\nprintf '%s\\n' \"$LC_ALL\" \"$LANG\" \"$TZ\" \"$PATH\" \"$GIT_CONFIG_NOSYSTEM\" \"$GIT_CONFIG_GLOBAL\" \"$GIT_ATTR_NOSYSTEM\" \"$GIT_OPTIONAL_LOCKS\" > " + capture + ".env\nexec " + gitPath + " \"$@\"\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &fingerprint.ExecRunner{Executable: script}
	if _, err := runner.Run(context.Background(), repo, "status", "--porcelain=v1", "--untracked-files=all"); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(capture + ".args")
	if err != nil {
		t.Fatal(err)
	}
	argText := string(args)
	for _, required := range []string{
		"--no-optional-locks",
		"-c core.fsmonitor=false",
		"-c core.untrackedCache=false",
		"status --porcelain=v1 --untracked-files=all",
	} {
		if !strings.Contains(argText, required) {
			t.Errorf("missing hardened argument %q in %q", required, argText)
		}
	}
	env, err := os.ReadFile(capture + ".env")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(env), "\n"), "\n")
	if len(lines) != 8 || lines[0] != "C" || lines[1] != "C" || lines[2] != "UTC" || lines[3] != root ||
		lines[4] != "1" || lines[5] != "/dev/null" || lines[6] != "1" || lines[7] != "0" {
		t.Fatalf("runner hardened environment: %q", string(env))
	}
}

func TestGitArgumentsUseDeterministicHardeningFlags(t *testing.T) {
	runner := &scriptedRunner{}
	if _, err := fingerprint.NewGitProvider(fingerprint.WithRunner(runner)).Inspect(context.Background(), t.TempDir(), "HEAD~1"); err != nil {
		t.Fatal(err)
	}
	joined := make([]string, 0, len(runner.calls))
	for _, call := range runner.calls {
		joined = append(joined, strings.Join(call, " "))
	}
	all := strings.Join(joined, "\n")
	for _, required := range []string{
		"status --porcelain=v1 -z --untracked-files=all",
		"rev-parse --verify --end-of-options HEAD~1^{commit}",
		"diff --no-ext-diff --no-textconv --binary --full-index --find-renames",
	} {
		if !strings.Contains(all, required) {
			t.Errorf("missing Git argument sequence %q in %q", required, all)
		}
	}
}

func TestNewExecRunnerPreservesLookPathFailure(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	runner := fingerprint.NewExecRunner()
	if runner.Err == nil {
		t.Fatal("expected LookPath error to be preserved")
	}
	if _, err := runner.Run(context.Background(), t.TempDir(), "status"); err == nil {
		t.Fatal("expected preserved LookPath error from Run")
	}
}

func TestEquivalentHashIgnoresFreezeTime(t *testing.T) {
	repo := newGitRepo(t)
	a := mustFreezeWithClock(t, repo, time.Date(2026, 8, 19, 1, 2, 3, 0, time.UTC), validManifestHash)
	b := mustFreezeWithClock(t, repo, time.Date(2027, 9, 20, 4, 5, 6, 0, time.UTC), validManifestHash)
	if a.EquivalentHash != b.EquivalentHash {
		t.Fatalf("freeze time changed equivalent identity: %s %s", a.EquivalentHash, b.EquivalentHash)
	}
	if a.FrozenAt.Equal(b.FrozenAt) {
		t.Fatal("fixture clocks unexpectedly equal")
	}
}

func TestEquivalentHashBindsManifestHash(t *testing.T) {
	repo := newGitRepo(t)
	a := mustFreeze(t, repo, validManifestHash)
	b := mustFreeze(t, repo, strings.Repeat("b", 64))
	if a.EquivalentHash == b.EquivalentHash {
		t.Fatal("changed manifest hash did not change equivalent identity")
	}
}

func TestDiffHashIsExactBinaryFullIndexDiff(t *testing.T) {
	repo := newGitRepo(t)
	got := mustFreeze(t, repo, validManifestHash)
	diff := exactDiff(t, repo, got)
	wantBytes := sha256.Sum256([]byte(diff))
	if got.DiffHash != hex.EncodeToString(wantBytes[:]) {
		t.Fatalf("diff hash: got %s want %s", got.DiffHash, hex.EncodeToString(wantBytes[:]))
	}
}

func TestFreezeRecordsRenameIdentity(t *testing.T) {
	repo := newGitRepo(t)
	if err := os.Rename(filepath.Join(repo, "file.txt"), filepath.Join(repo, "renamed.txt")); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-qm", "rename")
	got := mustFreeze(t, repo, validManifestHash)
	diff := exactDiff(t, repo, got)
	want := sha256.Sum256([]byte(diff))
	if got.DiffHash != hex.EncodeToString(want[:]) || !strings.Contains(diff, "similarity index") || !strings.Contains(diff, "rename from file.txt") || !strings.Contains(diff, "rename to renamed.txt") {
		t.Fatalf("rename diff was not substantive:\n%s", diff)
	}
}

func TestFreezeRecordsBinaryDiff(t *testing.T) {
	repo := newGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "binary.bin"), []byte{0, 1, 2, 255, 0}, 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "binary.bin")
	runGit(t, repo, "commit", "-qm", "binary")
	got := mustFreeze(t, repo, validManifestHash)
	diff := exactDiff(t, repo, got)
	want := sha256.Sum256([]byte(diff))
	if got.DiffHash != hex.EncodeToString(want[:]) || !strings.Contains(diff, "GIT binary patch") {
		t.Fatalf("binary diff was not substantive:\n%s", diff)
	}
}

func TestFreezeRecordsExecutableMode(t *testing.T) {
	repo := newGitRepo(t)
	if err := os.Chmod(filepath.Join(repo, "file.txt"), 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "file.txt")
	runGit(t, repo, "commit", "-qm", "mode")
	got := mustFreeze(t, repo, validManifestHash)
	diff := exactDiff(t, repo, got)
	want := sha256.Sum256([]byte(diff))
	if got.DiffHash != hex.EncodeToString(want[:]) || !strings.Contains(diff, "old mode 100644") || !strings.Contains(diff, "new mode 100755") {
		t.Fatalf("mode diff was not substantive:\n%s", diff)
	}
}

func TestFreezeRecordsSymlink(t *testing.T) {
	repo := newGitRepo(t)
	if err := os.Symlink("file.txt", filepath.Join(repo, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	runGit(t, repo, "add", "link.txt")
	runGit(t, repo, "commit", "-qm", "symlink")
	got := mustFreeze(t, repo, validManifestHash)
	diff := exactDiff(t, repo, got)
	want := sha256.Sum256([]byte(diff))
	if got.DiffHash != hex.EncodeToString(want[:]) || !strings.Contains(diff, "new file mode 120000") || !strings.Contains(diff, "+file.txt") {
		t.Fatalf("symlink diff was not substantive:\n%s", diff)
	}
}

func TestDiffDisablesExternalDiffAndTextconv(t *testing.T) {
	repo := newGitRepo(t)
	root := t.TempDir()
	marker := filepath.Join(root, "invoked")
	hook := filepath.Join(root, "diff-hook")
	hookBody := "#!/bin/sh\nprintf invoked > " + marker + "\nprintf converted\n"
	if err := os.WriteFile(hook, []byte(hookBody), 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "config", "diff.external", hook)
	runGit(t, repo, "config", "diff.probe.textconv", hook)
	if err := os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("*.txt diff=probe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".gitattributes")
	runGit(t, repo, "commit", "-qm", "attributes")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "file.txt")
	runGit(t, repo, "commit", "-qm", "candidate")
	got := mustFreeze(t, repo, validManifestHash)
	diff := exactDiff(t, repo, got)
	want := sha256.Sum256([]byte(diff))
	if got.DiffHash != hex.EncodeToString(want[:]) || !strings.Contains(diff, "-after") || strings.Contains(diff, "converted") {
		t.Fatalf("internal diff output was not preserved:\n%s", diff)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("external diff/textconv hook invoked: %v", err)
	}
}

func TestFreezeSupportsSHA256ObjectFormatWhenAvailable(t *testing.T) {
	repo, supported := newSHA256GitRepo(t)
	if !supported {
		t.Skip("installed Git explicitly lacks SHA-256 object-format support")
	}
	got := mustFreeze(t, repo, validManifestHash)
	if len(got.BaseCommit) != 64 || len(got.HeadCommit) != 64 || len(got.HeadTree) != 64 {
		t.Fatalf("expected full SHA-256 object IDs: %#v", got)
	}
}

func TestFreezeRejectsSubmodule(t *testing.T) {
	repo := newGitRepo(t)
	module := t.TempDir()
	runGit(t, module, "init", "-q")
	runGit(t, module, "config", "user.email", "test@example.invalid")
	runGit(t, module, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(module, "nested.txt"), []byte("nested\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, module, "add", "nested.txt")
	runGit(t, module, "commit", "-qm", "nested")
	runGit(t, repo, "-c", "protocol.file.allow=always", "submodule", "add", "-q", module, "nested")
	runGit(t, repo, "commit", "-qm", "submodule")
	_, err := fingerprint.NewGitProvider().Freeze(context.Background(), repo, "HEAD~1", validManifestHash)
	if failure.ExitCodeFor(err) != 30 {
		t.Fatalf("expected submodule drift, got %v", err)
	}
}

func TestFreezeClassifiesGitFailureAsTooling(t *testing.T) {
	p := fingerprint.NewGitProvider(fingerprint.WithRunner(failingRunner{}))
	_, err := p.Freeze(context.Background(), t.TempDir(), "HEAD~1", validManifestHash)
	if failure.ExitCodeFor(err) != 40 {
		t.Fatalf("expected tooling failure, got %v", err)
	}
}

func TestExecRunnerUsesFixedMinimalEnvironment(t *testing.T) {
	repo := newGitRepo(t)
	root := t.TempDir()
	capture := filepath.Join(root, "env.txt")
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "git-probe")
	contents := "#!/bin/sh\nprintf '%s\\n' \"$LC_ALL\" \"$LANG\" \"$TZ\" \"$PATH\" \"$GIT_CONFIG_NOSYSTEM\" \"$GIT_CONFIG_GLOBAL\" \"$GIT_ATTR_NOSYSTEM\" \"$GIT_OPTIONAL_LOCKS\" > " + capture + "\nexec " + gitPath + " \"$@\"\n"
	if err := os.WriteFile(script, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &fingerprint.ExecRunner{Executable: script}
	if _, err := runner.Run(context.Background(), repo, "status", "--porcelain=v1", "--untracked-files=all"); err != nil {
		t.Fatal(err)
	}
	env, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(env), "\n"), "\n")
	if len(lines) != 8 || lines[0] != "C" || lines[1] != "C" || lines[2] != "UTC" || lines[3] != root ||
		lines[4] != "1" || lines[5] != "/dev/null" || lines[6] != "1" || lines[7] != "0" {
		t.Fatalf("runner environment: %q", string(env))
	}
}

type failingRunner struct{}

func (failingRunner) Run(context.Context, string, ...string) ([]byte, error) {
	return nil, errors.New("synthetic git failure")
}

type scriptedRunner struct {
	status    []byte
	submodule []byte
	rev       []byte
	diff      []byte
	calls     [][]string
}

func (r *scriptedRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	switch args[0] {
	case "status":
		return r.status, nil
	case "submodule":
		return r.submodule, nil
	case "rev-parse":
		if r.rev != nil {
			return r.rev, nil
		}
		return []byte(strings.Repeat("a", 40) + "\n"), nil
	case "diff":
		return r.diff, nil
	default:
		return nil, errors.New("unexpected git command")
	}
}

func mustFreeze(t *testing.T, repo, manifestHash string) fingerprint.Fingerprint {
	t.Helper()
	got, err := fingerprint.NewGitProvider().Freeze(context.Background(), repo, "HEAD~1", manifestHash)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func mustFreezeWithClock(t *testing.T, repo string, at time.Time, manifestHash string) fingerprint.Fingerprint {
	t.Helper()
	got, err := fingerprint.NewGitProvider(
		fingerprint.WithClock(clock.Fixed{Time: at}),
	).Freeze(context.Background(), repo, "HEAD~1", manifestHash)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func exactDiff(t *testing.T, repo string, got fingerprint.Fingerprint) string {
	t.Helper()
	return runGit(t, repo, "diff", "--no-ext-diff", "--no-textconv", "--binary", "--full-index", "--find-renames", got.BaseCommit+".."+got.HeadCommit)
}

func newGitRepo(t *testing.T) string {
	return newGitRepoWithInit(t)
}

func newGitRepoWithInit(t *testing.T, initArgs ...string) string {
	t.Helper()
	repo := t.TempDir()
	args := append([]string{"init", "-q"}, initArgs...)
	runGit(t, repo, args...)
	runGit(t, repo, "config", "user.email", "test@example.invalid")
	runGit(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "file.txt")
	runGit(t, repo, "commit", "-qm", "before")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "file.txt")
	runGit(t, repo, "commit", "-qm", "after")
	return repo
}

func newSHA256GitRepo(t *testing.T) (string, bool) {
	t.Helper()
	repo := t.TempDir()
	cmd := exec.Command("git", "init", "-q", "--object-format=sha256")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		message := strings.ToLower(string(out))
		if strings.Contains(message, "unknown option") || strings.Contains(message, "object format") && strings.Contains(message, "not supported") {
			return "", false
		}
		t.Fatalf("git SHA-256 object-format probe: %v\n%s", err, out)
	}
	runGit(t, repo, "config", "user.email", "test@example.invalid")
	runGit(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "file.txt")
	runGit(t, repo, "commit", "-qm", "before")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "file.txt")
	runGit(t, repo, "commit", "-qm", "after")
	return repo, true
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}
