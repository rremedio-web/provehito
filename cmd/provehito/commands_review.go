package main

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/provehito-project/provehito/core/evidence"
	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/fingerprint"
	"github.com/provehito-project/provehito/core/lifecycle"
	"github.com/provehito-project/provehito/core/manifest"
	"github.com/provehito-project/provehito/core/policy"
)

func runReview(operation string, args []string, stdout, stderr interface{ Write([]byte) (int, error) }) int {
	if operation == "open" {
		return runReviewOpen(args, stdout, stderr)
	}
	return runReviewRecord(args, stdout, stderr)
}

func runReviewOpen(args []string, stdout, stderr interface{ Write([]byte) (int, error) }) int {
	fs := commandFlags("review open")
	state, jsonOutput := addStateFlags(fs)
	lane := fs.String("lane", "", "lane identifier")
	base := fs.String("base", "", "optional Git base revision; defaults to the frozen base")
	if err := parseFlags(fs, args); err != nil {
		return writeResult(stdout, stderr, "review open", *jsonOutput, nil, err)
	}
	_, m, _, err := loadLane(*state, *lane)
	if err != nil {
		return writeResult(stdout, stderr, "review open", *jsonOutput, nil, err)
	}
	if m.State != lifecycle.Frozen || m.Freeze == nil {
		return writeResult(stdout, stderr, "review open", *jsonOutput, nil, failure.New(failure.PolicyOrTransition, "review open lifecycle"))
	}
	fp, err := fingerprint.NewGitProvider().Freeze(context.Background(), m.Dispatch.Workspace, frozenBase(m.Freeze, *base), m.DispatchHash)
	if err != nil {
		return writeResult(stdout, stderr, "review open", *jsonOutput, nil, err)
	}
	if policy.Drifted(m, fp) {
		return writeResult(stdout, stderr, "review open", *jsonOutput, nil, failure.New(failure.CandidateOrReview, "review open candidate drift"))
	}
	return writeResult(stdout, stderr, "review open", *jsonOutput, map[string]any{"lane": m.LaneID, "candidate_hash": m.Freeze.Candidate, "dispatch_hash": m.DispatchHash, "evidence_hashes": hashList(m), "head": fp.HeadCommit, "tree": fp.HeadTree, "diff": fp.DiffHash}, nil)
}

func runReviewRecord(args []string, stdout, stderr interface{ Write([]byte) (int, error) }) int {
	fs := commandFlags("review record")
	state, jsonOutput := addStateFlags(fs)
	lane := fs.String("lane", "", "lane identifier")
	reviewer := fs.String("reviewer", "", "reviewer identity")
	family := fs.String("family", "", "reviewer family")
	seatID := seatIDFlag(fs)
	verdict := fs.String("verdict", "", "PASS or FAIL")
	fingerprintValue := fs.String("fingerprint", "", "candidate equivalent hash")
	source := fs.String("source", "", "untrusted source text")
	base := fs.String("base", "", "optional Git base revision; defaults to the frozen base")
	if err := parseFlags(fs, args); err != nil {
		return writeResult(stdout, stderr, "review record", *jsonOutput, nil, err)
	}
	store, m, hash, err := loadLane(*state, *lane)
	if err != nil {
		return writeResult(stdout, stderr, "review record", *jsonOutput, nil, err)
	}
	if m.State != lifecycle.Frozen || m.Freeze == nil {
		return writeResult(stdout, stderr, "review record", *jsonOutput, nil, failure.New(failure.PolicyOrTransition, "review record lifecycle"))
	}
	if *reviewer == "" || *family == "" || *seatID == "" {
		return writeResult(stdout, stderr, "review record", *jsonOutput, nil, usageError("reviewer, family, and seat id required"))
	}
	if reason := policy.IndependenceReason(m.Dispatch.Family, m.Dispatch.SeatID, *family, *seatID); reason != "" {
		return writeResult(stdout, stderr, "review record", *jsonOutput, nil, policy.IndependenceFailure("review", reason))
	}
	fp, err := fingerprint.NewGitProvider().Freeze(context.Background(), m.Dispatch.Workspace, frozenBase(m.Freeze, *base), m.DispatchHash)
	if err != nil {
		return writeResult(stdout, stderr, "review record", *jsonOutput, nil, err)
	}
	if policy.Drifted(m, fp) {
		return writeResult(stdout, stderr, "review record", *jsonOutput, nil, failure.New(failure.CandidateOrReview, "review candidate drift"))
	}
	if *fingerprintValue == "" {
		*fingerprintValue = m.Freeze.Candidate
	}
	if *fingerprintValue != m.Freeze.Candidate {
		return writeResult(stdout, stderr, "review record", *jsonOutput, nil, failure.New(failure.CandidateOrReview, "review fingerprint"))
	}
	evidenceHashes, err := policy.VerifyEvidence(m, evidence.NewStore(*state))
	if err != nil {
		return writeResult(stdout, stderr, "review record", *jsonOutput, nil, err)
	}
	verdictValue, err := parseVerdict(*verdict)
	if err != nil {
		return writeResult(stdout, stderr, "review record", *jsonOutput, nil, err)
	}
	sort.Strings(evidenceHashes)
	record := manifest.ReviewRecord{Reviewer: *reviewer, Family: *family, SeatID: *seatID, Verdict: verdictValue, Fingerprint: *fingerprintValue, EvidenceHashes: evidenceHashes, At: time.Now().UTC().Format(time.RFC3339)}
	if *source != "" {
		// Source text is deliberately not copied into the authority-bearing model.
		_ = source
	}
	_, newHash, err := store.Apply(manifest.ExpectedHash{}, lifecycle.RecordReview, func(next *manifest.Manifest) {
		next.Review = &record
	})
	if err != nil {
		return writeResult(stdout, stderr, "review record", *jsonOutput, nil, err)
	}
	return writeResult(stdout, stderr, "review record", *jsonOutput, map[string]any{"reviewer": *reviewer, "family": *family, "seat_id": *seatID, "verdict": verdictValue, "fingerprint": *fingerprintValue, "evidence_hashes": evidenceHashes, "previous_hash": hash, "hash": newHash}, nil)
}

func parseVerdict(value string) (string, error) {
	switch strings.ToLower(value) {
	case "pass", "approve", "approved":
		return "PASS", nil
	case "fail", "reject", "rejected":
		return "FAIL", nil
	default:
		return "", failure.New(failure.PolicyOrTransition, "review explicit verdict")
	}
}

func frozenBase(freeze *manifest.FreezeRecord, override string) string {
	if override != "" {
		return override
	}
	if freeze == nil {
		return ""
	}
	return freeze.Base
}

func runReady(args []string, stdout, stderr interface{ Write([]byte) (int, error) }) int {
	fs := commandFlags("ready")
	state, jsonOutput := addStateFlags(fs)
	lane := fs.String("lane", "", "lane identifier")
	base := fs.String("base", "", "optional Git base revision; defaults to the frozen base")
	if err := parseFlags(fs, args); err != nil {
		return writeResult(stdout, stderr, "ready", *jsonOutput, nil, err)
	}
	store, m, hash, err := loadLane(*state, *lane)
	if err != nil {
		return writeResult(stdout, stderr, "ready", *jsonOutput, nil, err)
	}
	if m.State != lifecycle.Reviewed || m.Freeze == nil || m.Review == nil {
		return writeResult(stdout, stderr, "ready", *jsonOutput, nil, failure.New(failure.PolicyOrTransition, "ready lifecycle"))
	}
	fp, err := fingerprint.NewGitProvider().Freeze(context.Background(), m.Dispatch.Workspace, frozenBase(m.Freeze, *base), m.DispatchHash)
	if err != nil {
		return writeResult(stdout, stderr, "ready", *jsonOutput, nil, err)
	}
	ready, err := policy.NewReadiness().Evaluate(policy.Input{Manifest: m, Current: fp, Loader: evidence.NewStore(*state)})
	if err != nil {
		return writeResult(stdout, stderr, "ready", *jsonOutput, nil, err)
	}
	_, newHash, err := store.Apply(manifest.ExpectedHash{}, lifecycle.MarkReady, nil)
	if err != nil {
		return writeResult(stdout, stderr, "ready", *jsonOutput, nil, err)
	}
	return writeResult(stdout, stderr, "ready", *jsonOutput, map[string]any{"candidate_hash": ready.CandidateEquivalentHash, "evidence_hashes": ready.EvidenceHashes, "banner": ready.Banner, "previous_hash": hash, "hash": newHash}, nil)
}

func runClose(args []string, stdout, stderr interface{ Write([]byte) (int, error) }) int {
	fs := commandFlags("close")
	state, jsonOutput := addStateFlags(fs)
	lane := fs.String("lane", "", "lane identifier")
	expected := fs.String("expected-hash", "", "expected manifest hash")
	if err := parseFlags(fs, args); err != nil {
		return writeResult(stdout, stderr, "close", *jsonOutput, nil, err)
	}
	store, m, hash, err := loadLane(*state, *lane)
	if err != nil {
		return writeResult(stdout, stderr, "close", *jsonOutput, nil, err)
	}
	if m.State != lifecycle.Ready {
		return writeResult(stdout, stderr, "close", *jsonOutput, nil, failure.New(failure.PolicyOrTransition, "close lifecycle"))
	}
	m, newHash, err := store.Apply(manifest.OptionalHash(*expected, "close"), lifecycle.Close, nil)
	if err != nil {
		return writeResult(stdout, stderr, "close", *jsonOutput, nil, err)
	}
	return writeResult(stdout, stderr, "close", *jsonOutput, map[string]any{"state": string(m.State), "previous_hash": hash, "hash": newHash}, nil)
}
