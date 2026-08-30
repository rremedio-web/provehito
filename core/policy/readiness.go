// Package policy evaluates whether a reviewed candidate is locally ready.
// It owns the whole readiness question: required-check binding, candidate
// drift, reviewer independence, review binding, evidence-set equality, and
// hash-set uniqueness.
package policy

import (
	"sort"

	"github.com/provehito-project/provehito/core/evidence"
	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/fingerprint"
	"github.com/provehito-project/provehito/core/lifecycle"
	"github.com/provehito-project/provehito/core/manifest"
)

// Verdict is the only review decision understood by the core.
type Verdict string

const (
	Pass Verdict = "PASS"
	Fail Verdict = "FAIL"
)

// EvidenceLoader loads one evidence receipt by content-addressed reference.
// The filesystem evidence store is the production adapter; tests supply an
// in-memory loader.
type EvidenceLoader interface {
	Load(evidence.Reference) (evidence.Receipt, error)
}

// Input is the complete readiness witness: the lane manifest, the current
// workspace fingerprint, and the evidence loader.
type Input struct {
	Manifest manifest.Manifest
	Current  fingerprint.Fingerprint
	Loader   EvidenceLoader
}

// ReadyRecord is a non-authorizing readiness result.
type ReadyRecord struct {
	CandidateEquivalentHash string
	ManifestHash            string
	EvidenceHashes          []string
	Banner                  string
}

// Readiness evaluates explicit state and immutable identities only.
type Readiness struct{}

func NewReadiness() *Readiness { return &Readiness{} }

// Evaluate answers the whole readiness question from the manifest, the
// current fingerprint, and the evidence loader.
func (r *Readiness) Evaluate(input Input) (ReadyRecord, error) {
	m := input.Manifest
	if m.State != lifecycle.Reviewed || m.Freeze == nil || m.Review == nil {
		return ReadyRecord{}, failure.New(failure.PolicyOrTransition, "readiness lifecycle")
	}
	verified, err := VerifyEvidence(m, input.Loader)
	if err != nil {
		return ReadyRecord{}, err
	}
	if !validHashSet(m.Review.EvidenceHashes) {
		return ReadyRecord{}, failure.New(failure.CandidateOrReview, "ready evidence")
	}
	if Drifted(m, input.Current) {
		return ReadyRecord{}, failure.New(failure.CandidateOrReview, "ready candidate drift")
	}
	if reason := IndependenceReason(m.Dispatch.Family, m.Dispatch.SeatID, m.Review.Family, m.Review.SeatID); reason != "" {
		return ReadyRecord{}, IndependenceFailure("readiness", reason)
	}
	if m.Review.Verdict == "" {
		return ReadyRecord{}, failure.New(failure.PolicyOrTransition, "readiness explicit verdict")
	}
	if err := validateReviewBinding(m); err != nil {
		return ReadyRecord{}, err
	}
	if !evidenceEqual(verified, m.Review.EvidenceHashes) {
		return ReadyRecord{}, failure.New(failure.CandidateOrReview, "readiness evidence")
	}
	if m.Review.Verdict != string(Pass) {
		return ReadyRecord{}, failure.New(failure.CandidateOrReview, "readiness verdict")
	}
	evidenceHashes := append([]string(nil), m.Review.EvidenceHashes...)
	sort.Strings(evidenceHashes)
	return ReadyRecord{CandidateEquivalentHash: input.Current.EquivalentHash, ManifestHash: m.DispatchHash, EvidenceHashes: evidenceHashes, Banner: "READY is not authorization"}, nil
}

// VerifyEvidence enforces the required-check binding rule: a receipt
// satisfies a required check only when its method, candidate hash, dispatch
// hash, result class, and exit code all bind to this lane. It returns the
// verified evidence hash set.
func VerifyEvidence(m manifest.Manifest, loader EvidenceLoader) ([]string, error) {
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
	if loader == nil {
		return nil, failure.New(failure.UsageOrSchema, "readiness evidence loader")
	}
	hashes := make([]string, 0, len(m.Evidence))
	seenNames := make(map[string]struct{}, len(m.Evidence))
	for _, ref := range m.Evidence {
		if _, exists := seenNames[ref.Name]; exists {
			return nil, failure.New(failure.CandidateOrReview, "duplicate evidence name")
		}
		seenNames[ref.Name] = struct{}{}
		receipt, err := loader.Load(evidence.Reference{Hash: ref.Hash})
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
	if !validHashSet(hashes) {
		return nil, failure.New(failure.CandidateOrReview, "review evidence")
	}
	for _, present := range required {
		if !present {
			return nil, failure.New(failure.CandidateOrReview, "required evidence missing")
		}
	}
	return hashes, nil
}

// IndependenceReason reports the typed reason the reviewer identity violates
// the independence requirement of the dispatch, or the empty reason when the
// reviewer family and seat are independent. The rule exists exactly once.
func IndependenceReason(writerFamily, writerSeatID, reviewerFamily, reviewerSeatID string) failure.Reason {
	if writerFamily == "" || reviewerFamily == "" || writerFamily == reviewerFamily {
		return failure.ReasonReviewerFamily
	}
	if writerSeatID == "" || reviewerSeatID == "" || writerSeatID == reviewerSeatID {
		return failure.ReasonReviewerSeat
	}
	return ""
}

// IndependenceFailure builds the classified, correctable failure for a
// violated independence requirement. prefix names the calling operation.
func IndependenceFailure(prefix string, reason failure.Reason) error {
	op := prefix + " reviewer seat"
	if reason == failure.ReasonReviewerFamily {
		op = prefix + " reviewer family"
	}
	return failure.NewReason(failure.PolicyOrTransition, op, reason)
}

// FrozenFingerprint reconstructs the candidate fingerprint bound by the
// manifest's freeze record and dispatch hash.
func FrozenFingerprint(m manifest.Manifest) fingerprint.Fingerprint {
	if m.Freeze == nil {
		return fingerprint.Fingerprint{}
	}
	return fingerprint.Fingerprint{
		BaseCommit:     m.Freeze.Base,
		HeadCommit:     m.Freeze.Head,
		HeadTree:       m.Freeze.Tree,
		DiffHash:       m.Freeze.Diff,
		ManifestHash:   m.DispatchHash,
		EquivalentHash: m.Freeze.Candidate,
	}
}

// Drifted reports whether current no longer matches the manifest's frozen
// candidate.
func Drifted(m manifest.Manifest, current fingerprint.Fingerprint) bool {
	return !sameCandidate(FrozenFingerprint(m), current)
}

func validateReviewBinding(m manifest.Manifest) error {
	record := m.Review
	if record == nil || m.Freeze == nil {
		return failure.New(failure.CandidateOrReview, "review binding")
	}
	if record.Reviewer == "" || record.Family == "" || record.SeatID == "" ||
		(record.Verdict != string(Pass) && record.Verdict != string(Fail)) ||
		record.Fingerprint == "" || !failure.IsHash(record.Fingerprint) ||
		!failure.IsHash(m.Freeze.Candidate) || !failure.IsHash(m.DispatchHash) ||
		record.Fingerprint != m.Freeze.Candidate {
		return failure.New(failure.CandidateOrReview, "review binding")
	}
	if !validHashSet(record.EvidenceHashes) {
		return failure.New(failure.CandidateOrReview, "review evidence binding")
	}
	return nil
}

func sameCandidate(a, b fingerprint.Fingerprint) bool {
	return a.EquivalentHash == b.EquivalentHash && a.ManifestHash == b.ManifestHash && a.BaseCommit == b.BaseCommit && a.HeadCommit == b.HeadCommit && a.HeadTree == b.HeadTree && a.DiffHash == b.DiffHash
}

func validHashSet(values []string) bool {
	if len(values) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !failure.IsHash(value) {
			return false
		}
		if _, ok := seen[value]; ok {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func evidenceEqual(left, right []string) bool {
	if !validHashSet(left) || !validHashSet(right) || len(left) != len(right) {
		return false
	}
	a, b := append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
