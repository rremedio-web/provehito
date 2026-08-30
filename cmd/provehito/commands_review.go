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
	"github.com/provehito-project/provehito/core/review"
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
	if !sameFrozen(m.Freeze, fp) {
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
	if *family == m.Dispatch.Family {
		return writeResult(stdout, stderr, "review record", *jsonOutput, nil, failure.NewReason(failure.PolicyOrTransition, "review reviewer family", failure.ReasonReviewerFamily))
	}
	if *seatID == m.Dispatch.SeatID {
		return writeResult(stdout, stderr, "review record", *jsonOutput, nil, failure.NewReason(failure.PolicyOrTransition, "review reviewer seat", failure.ReasonReviewerSeat))
	}
	fp, err := fingerprint.NewGitProvider().Freeze(context.Background(), m.Dispatch.Workspace, frozenBase(m.Freeze, *base), m.DispatchHash)
	if err != nil {
		return writeResult(stdout, stderr, "review record", *jsonOutput, nil, err)
	}
	if !sameFrozen(m.Freeze, fp) {
		return writeResult(stdout, stderr, "review record", *jsonOutput, nil, failure.New(failure.CandidateOrReview, "review candidate drift"))
	}
	if *fingerprintValue == "" {
		*fingerprintValue = m.Freeze.Candidate
	}
	if *fingerprintValue != m.Freeze.Candidate {
		return writeResult(stdout, stderr, "review record", *jsonOutput, nil, failure.New(failure.CandidateOrReview, "review fingerprint"))
	}
	evidenceHashes, err := verifyLaneEvidence(*state, m)
	if err != nil {
		return writeResult(stdout, stderr, "review record", *jsonOutput, nil, err)
	}
	verdictValue, err := parseVerdict(*verdict)
	if err != nil {
		return writeResult(stdout, stderr, "review record", *jsonOutput, nil, err)
	}
	sort.Strings(evidenceHashes)
	m.Review = &manifest.ReviewRecord{Reviewer: *reviewer, Family: *family, SeatID: *seatID, Verdict: verdictValue, Fingerprint: *fingerprintValue, EvidenceHashes: evidenceHashes, At: time.Now().UTC().Format(time.RFC3339)}
	if *source != "" {
		// Source text is deliberately not copied into the authority-bearing model.
		_ = source
	}
	snapshot, err := lifecycle.Apply(lifecycle.Snapshot{State: m.State}, lifecycle.RecordReview)
	if err != nil {
		return writeResult(stdout, stderr, "review record", *jsonOutput, nil, err)
	}
	m.State = snapshot.State
	newHash, err := store.Update(hash, m)
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

func sameFrozen(freeze *manifest.FreezeRecord, fp fingerprint.Fingerprint) bool {
	return freeze != nil && freeze.Base == fp.BaseCommit && freeze.Head == fp.HeadCommit && freeze.Candidate == fp.EquivalentHash && freeze.Tree == fp.HeadTree && freeze.Diff == fp.DiffHash
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

func verifyLaneEvidence(state string, m manifest.Manifest) ([]string, error) {
	if m.Freeze == nil || len(m.Dispatch.RequiredChecks) == 0 {
		return nil, failure.New(failure.CandidateOrReview, "required evidence")
	}
	required := make(map[string]bool, len(m.Dispatch.RequiredChecks))
	for _, name := range m.Dispatch.RequiredChecks {
		if name == "" {
			return nil, failure.New(failure.CandidateOrReview, "required evidence")
		}
		if _, exists := required[name]; exists {
			return nil, failure.New(failure.CandidateOrReview, "duplicate required evidence")
		}
		required[name] = false
	}
	store := evidence.NewStore(state)
	hashes := make([]string, 0, len(m.Evidence))
	seenNames := make(map[string]struct{}, len(m.Evidence))
	for _, ref := range m.Evidence {
		if _, exists := seenNames[ref.Name]; exists {
			return nil, failure.New(failure.CandidateOrReview, "duplicate evidence name")
		}
		seenNames[ref.Name] = struct{}{}
		receipt, err := store.Load(evidence.Reference{Hash: ref.Hash})
		if err != nil {
			return nil, err
		}
		if receipt.MethodID != ref.Name || receipt.CandidateHash != m.Freeze.Candidate || receipt.ManifestHash != m.DispatchHash ||
			receipt.ResultClass != evidence.ResultSuccess || receipt.ExitCode != 0 {
			return nil, failure.New(failure.CandidateOrReview, "evidence binding")
		}
		if _, needed := required[ref.Name]; needed {
			required[ref.Name] = true
		}
		hashes = append(hashes, ref.Hash)
	}
	if !uniqueHashSet(hashes) {
		return nil, failure.New(failure.CandidateOrReview, "review evidence")
	}
	for _, present := range required {
		if !present {
			return nil, failure.New(failure.CandidateOrReview, "required evidence missing")
		}
	}
	return hashes, nil
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
	refs := make([]evidence.Reference, 0, len(m.Evidence))
	for _, ref := range m.Evidence {
		refs = append(refs, evidence.Reference{Name: ref.Name, Hash: ref.Hash})
	}
	verified, err := verifyLaneEvidence(*state, m)
	if err != nil {
		return writeResult(stdout, stderr, "ready", *jsonOutput, nil, err)
	}
	if !uniqueHashSet(m.Review.EvidenceHashes) {
		return writeResult(stdout, stderr, "ready", *jsonOutput, nil, failure.New(failure.CandidateOrReview, "ready evidence"))
	}
	fp, err := fingerprint.NewGitProvider().Freeze(context.Background(), m.Dispatch.Workspace, frozenBase(m.Freeze, *base), m.DispatchHash)
	if err != nil {
		return writeResult(stdout, stderr, "ready", *jsonOutput, nil, err)
	}
	if !sameFrozen(m.Freeze, fp) {
		return writeResult(stdout, stderr, "ready", *jsonOutput, nil, failure.New(failure.CandidateOrReview, "ready candidate drift"))
	}
	record := review.Record{ReviewerID: m.Review.Reviewer, ReviewerFamily: m.Review.Family, ReviewerSeatID: m.Review.SeatID, Verdict: review.Verdict(m.Review.Verdict), CandidateEquivalentHash: m.Review.Fingerprint, ManifestHash: m.DispatchHash, EvidenceHashes: append([]string(nil), m.Review.EvidenceHashes...)}
	ready, err := policy.NewReadiness().Evaluate(policy.Input{State: m.State, Frozen: fp, Current: fp, ManifestHash: m.DispatchHash, RequiredEvidence: refs, VerifiedEvidenceHashes: verified, Review: record, Family: policy.FamilyPolicy{WriterFamily: m.Dispatch.Family, WriterSeatID: m.Dispatch.SeatID, RequireIndependent: true}})
	if err != nil {
		return writeResult(stdout, stderr, "ready", *jsonOutput, nil, err)
	}
	snapshot, err := lifecycle.Apply(lifecycle.Snapshot{State: m.State}, lifecycle.MarkReady)
	if err != nil {
		return writeResult(stdout, stderr, "ready", *jsonOutput, nil, err)
	}
	m.State = snapshot.State
	newHash, err := store.Update(hash, m)
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
	if *expected != "" && *expected != hash {
		return writeResult(stdout, stderr, "close", *jsonOutput, nil, failure.New(failure.Integrity, "close manifest prior hash mismatch"))
	}
	if m.State != lifecycle.Ready {
		return writeResult(stdout, stderr, "close", *jsonOutput, nil, failure.New(failure.PolicyOrTransition, "close lifecycle"))
	}
	snapshot, err := lifecycle.Apply(lifecycle.Snapshot{State: m.State}, lifecycle.Close)
	if err != nil {
		return writeResult(stdout, stderr, "close", *jsonOutput, nil, err)
	}
	m.State = snapshot.State
	newHash, err := store.Update(hash, m)
	if err != nil {
		return writeResult(stdout, stderr, "close", *jsonOutput, nil, err)
	}
	return writeResult(stdout, stderr, "close", *jsonOutput, map[string]any{"state": string(m.State), "previous_hash": hash, "hash": newHash}, nil)
}
