package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/provehito-project/provehito/core/adapter"
	"github.com/provehito-project/provehito/core/evidence"
	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/fingerprint"
	"github.com/provehito-project/provehito/core/lifecycle"
	"github.com/provehito-project/provehito/core/manifest"
	"github.com/provehito-project/provehito/core/process"
	"github.com/provehito-project/provehito/core/workspace"
)

func loadLane(state, id string) (manifest.Store, manifest.Manifest, string, error) {
	path, err := lanePath(state, id)
	if err != nil {
		return manifest.Store{}, manifest.Manifest{}, "", err
	}
	store := manifest.NewStore(path, nil)
	m, hash, err := store.Load()
	return store, m, hash, err
}

func parseDuration(value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil {
		if seconds, parseErr := strconv.Atoi(value); parseErr == nil {
			duration = time.Duration(seconds) * time.Second
			err = nil
		}
	}
	if err != nil || duration <= 0 {
		return 0, usageError("agent timeout")
	}
	return duration, nil
}

func profileRank(cost string) (int, bool) {
	switch strings.ToLower(cost) {
	case "economy", "cheap", "low":
		return 0, true
	case "standard", "medium":
		return 1, true
	case "premium", "high":
		return 2, true
	default:
		return 0, false
	}
}

func runAgent(args []string, stdout, stderr interface{ Write([]byte) (int, error) }) int {
	fs := commandFlags("agent run")
	state, jsonOutput := addStateFlags(fs)
	lane := fs.String("lane", "", "lane identifier")
	profilePath := fs.String("profile", "", "absolute executable profile")
	executable := fs.String("executable", "", "absolute executable")
	profileID := fs.String("profile-id", "", "profile identifier")
	family := fs.String("family", "", "profile family")
	seatID := seatIDFlag(fs)
	cost := fs.String("cost-class", "", "profile cost class")
	timeout := fs.String("timeout", "", "profile timeout")
	outputLimit := fs.Int64("output-bytes", 0, "profile output limit")
	fs.Int64Var(outputLimit, "max-output-bytes", 0, "profile output limit")
	base := fs.String("base", "HEAD~1", "Git base revision")
	var profileArgs, env, capabilities listFlag
	fs.Var(&profileArgs, "arg", "literal profile argument")
	fs.Var(&env, "env", "allowlisted environment name")
	fs.Var(&capabilities, "capability", "profile capability")
	if err := parseFlags(fs, args); err != nil {
		return writeResult(stdout, stderr, "agent run", *jsonOutput, nil, err)
	}
	if *state == "" || *lane == "" || *profileID == "" || *family == "" || *seatID == "" || *cost == "" || *timeout == "" || *outputLimit <= 0 {
		return writeResult(stdout, stderr, "agent run", *jsonOutput, nil, usageError("agent profile flags required"))
	}
	if *executable == "" {
		*executable = *profilePath
	}
	if *executable == "" || !filepath.IsAbs(*executable) {
		return writeResult(stdout, stderr, "agent run", *jsonOutput, nil, failure.New(failure.ToolingOrAdapter, "agent executable"))
	}
	if _, err := os.Stat(*executable); err != nil {
		return writeResult(stdout, stderr, "agent run", *jsonOutput, nil, failure.Wrap(failure.ToolingOrAdapter, "agent executable", err))
	}
	store, m, hash, err := loadLane(*state, *lane)
	if err != nil {
		return writeResult(stdout, stderr, "agent run", *jsonOutput, nil, err)
	}
	if m.State != lifecycle.Active {
		return writeResult(stdout, stderr, "agent run", *jsonOutput, nil, failure.New(failure.PolicyOrTransition, "agent run lifecycle"))
	}
	if *profileID != m.Dispatch.Adapter || *family != m.Dispatch.Family {
		return writeResult(stdout, stderr, "agent run", *jsonOutput, nil, failure.New(failure.PolicyOrTransition, "agent profile dispatch mismatch"))
	}
	if *seatID != m.Dispatch.SeatID {
		return writeResult(stdout, stderr, "agent run", *jsonOutput, nil, failure.NewReason(failure.PolicyOrTransition, "agent writer seat", failure.ReasonWriterSeat))
	}
	duration, err := parseDuration(*timeout)
	if err != nil {
		return writeResult(stdout, stderr, "agent run", *jsonOutput, nil, err)
	}
	wholeSeconds, fractional := duration/time.Second, duration%time.Second
	if m.Dispatch.MaxSeconds <= 0 || int64(wholeSeconds) > m.Dispatch.MaxSeconds || int64(wholeSeconds) == m.Dispatch.MaxSeconds && fractional > 0 ||
		m.Dispatch.MaxOutputBytes <= 0 || *outputLimit > m.Dispatch.MaxOutputBytes {
		return writeResult(stdout, stderr, "agent run", *jsonOutput, nil, failure.New(failure.PolicyOrTransition, "agent profile exceeds dispatch limits"))
	}
	costRank, knownCost := profileRank(*cost)
	if !knownCost {
		return writeResult(stdout, stderr, "agent run", *jsonOutput, nil, usageError("agent cost class"))
	}
	profile := adapter.Profile{ID: *profileID, Executable: *executable, Args: profileArgs.values, Capabilities: capabilities.values, Family: *family, CostRank: costRank, EnvAllowlist: env.values, Timeout: duration, OutputLimit: *outputLimit}
	if !capabilities.set || len(capabilities.values) == 0 {
		return writeResult(stdout, stderr, "agent run", *jsonOutput, nil, usageError("agent capability required"))
	}
	if *cost != m.Dispatch.CostClass {
		return writeResult(stdout, stderr, "agent run", *jsonOutput, nil, failure.New(failure.PolicyOrTransition, "agent cost class mismatch"))
	}
	manager := workspace.NewLeaseManager(*state)
	if abandoned := manager.DetectAbandoned(m.Dispatch.Workspace); abandoned != nil && failure.Is(abandoned, failure.Concurrency) {
		snapshot, transitionErr := lifecycle.Apply(lifecycle.Snapshot{State: m.State}, lifecycle.Block)
		if transitionErr != nil {
			return writeResult(stdout, stderr, "agent run", *jsonOutput, nil, transitionErr)
		}
		m.State, m.BlockedFrom = snapshot.State, snapshot.BlockedFrom
		if _, updateErr := store.Update(hash, m); updateErr != nil {
			return writeResult(stdout, stderr, "agent run", *jsonOutput, nil, updateErr)
		}
		return writeResult(stdout, stderr, "agent run", *jsonOutput, nil, abandoned)
	}
	lease, err := manager.Acquire(m.LaneID, m.Dispatch.Workspace)
	if err != nil {
		return writeResult(stdout, stderr, "agent run", *jsonOutput, nil, err)
	}
	defer lease.Release()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fp, err := fingerprint.NewGitProvider().Freeze(ctx, m.Dispatch.Workspace, *base, m.DispatchHash)
	if err != nil {
		return writeResult(stdout, stderr, "agent run", *jsonOutput, nil, err)
	}
	result, runErr := (&process.Supervisor{}).Run(ctx, process.Request{Workspace: m.Dispatch.Workspace, Lease: lease, Profile: profile})
	resultClass := evidence.ResultSuccess
	exitCode := 0
	if runErr != nil {
		class, _ := errorDetails(runErr)
		resultClass = string(class)
		exitCode = failure.ExitCodeFor(runErr)
	} else if result.ExitCode != 0 {
		resultClass = evidence.ResultCandidateOrReview
		exitCode, _ = failure.CodeFor(failure.CandidateOrReview)
	}
	stdoutHash := result.StdoutHash
	stderrHash := result.StderrHash
	if stdoutHash == "" {
		stdoutHash = emptyStreamHash()
	}
	if stderrHash == "" {
		stderrHash = emptyStreamHash()
	}
	probe := fmt.Sprintf(
		"exit=%d duration_ms=%d stdout_truncated=%t stderr_truncated=%t stdout_hash=%s stderr_hash=%s",
		result.ExitCode,
		result.Duration.Milliseconds(),
		result.StdoutTruncated,
		result.StderrTruncated,
		stdoutHash,
		stderrHash,
	)
	receipt, receiptErr := evidence.NewStore(*state).Add(evidence.Receipt{
		SchemaVersion: 1,
		MethodID:      "agent-run",
		SeatID:        *seatID,
		Probe:         probe,
		CandidateHash: fp.EquivalentHash,
		ManifestHash:  m.DispatchHash,
		ResultClass:   resultClass,
		ExitCode:      exitCode,
		Artifacts: []evidence.Reference{
			{Name: "stdout", Hash: stdoutHash},
			{Name: "stderr", Hash: stderrHash},
		},
	})
	if receiptErr != nil {
		return writeResult(stdout, stderr, "agent run", *jsonOutput, nil, receiptErr)
	}
	data := map[string]any{
		"receipt":          receipt.Hash,
		"ref":              receipt.Hash,
		"exit_code":        result.ExitCode,
		"manifest_hash":    m.DispatchHash,
		"candidate_hash":   fp.EquivalentHash,
		"hash":             hash,
		"stdout_hash":      stdoutHash,
		"stderr_hash":      stderrHash,
		"stdout_truncated": result.StdoutTruncated,
		"stderr_truncated": result.StderrTruncated,
		"duration_ms":      result.Duration.Milliseconds(),
		"seat_id":          *seatID,
	}
	if runErr != nil {
		return writeResult(stdout, stderr, "agent run", *jsonOutput, data, runErr)
	}
	if result.ExitCode != 0 {
		return writeResult(stdout, stderr, "agent run", *jsonOutput, data, failure.New(failure.CandidateOrReview, "agent process exit"))
	}
	return writeResult(stdout, stderr, "agent run", *jsonOutput, data, nil)
}

func emptyStreamHash() string {
	return hex.EncodeToString(sha256.New().Sum(nil))
}
