// Package policy evaluates whether a reviewed candidate is locally ready.
package policy

import (
	"sort"

	"github.com/provehito-project/provehito/core/evidence"
	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/fingerprint"
	"github.com/provehito-project/provehito/core/lifecycle"
	"github.com/provehito-project/provehito/core/review"
)

// FamilyPolicy controls the independent-family requirement.
type FamilyPolicy struct {
	WriterFamily       string
	RequireIndependent bool
}

// Input is the complete, already-collected readiness witness.
type Input struct {
	State                  lifecycle.State
	Frozen                 fingerprint.Fingerprint
	Current                fingerprint.Fingerprint
	ManifestHash           string
	RequiredEvidence       []evidence.Reference
	VerifiedEvidenceHashes []string
	Review                 review.Record
	Family                 FamilyPolicy
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

func (r *Readiness) Evaluate(input Input) (ReadyRecord, error) {
	if input.State != lifecycle.Reviewed {
		return ReadyRecord{}, failure.New(failure.PolicyOrTransition, "readiness lifecycle")
	}
	if input.ManifestHash == "" || input.Frozen.ManifestHash != input.ManifestHash || input.Current.ManifestHash != input.ManifestHash || !sameCandidate(input.Frozen, input.Current) {
		return ReadyRecord{}, failure.New(failure.CandidateOrReview, "readiness candidate")
	}
	writerFamily, independent := input.Family.WriterFamily, input.Family.RequireIndependent
	if independent && (writerFamily == "" || input.Review.ReviewerFamily == "" || writerFamily == input.Review.ReviewerFamily) {
		return ReadyRecord{}, failure.New(failure.PolicyOrTransition, "readiness reviewer family")
	}
	if input.Review.Verdict == "" {
		return ReadyRecord{}, failure.New(failure.PolicyOrTransition, "readiness explicit verdict")
	}
	if err := review.Validate(input.Frozen, input.Review); err != nil {
		return ReadyRecord{}, err
	}
	required := make([]string, len(input.RequiredEvidence))
	for i, ref := range input.RequiredEvidence {
		required[i] = ref.Hash
	}
	if !review.EvidenceEqual(required, input.VerifiedEvidenceHashes) || !review.EvidenceEqual(required, input.Review.EvidenceHashes) {
		return ReadyRecord{}, failure.New(failure.CandidateOrReview, "readiness evidence")
	}
	if input.Review.Verdict != review.Pass {
		return ReadyRecord{}, failure.New(failure.CandidateOrReview, "readiness verdict")
	}
	evidenceHashes := append([]string(nil), input.Review.EvidenceHashes...)
	sort.Strings(evidenceHashes)
	return ReadyRecord{CandidateEquivalentHash: input.Current.EquivalentHash, ManifestHash: input.ManifestHash, EvidenceHashes: evidenceHashes, Banner: "READY is not authorization"}, nil
}

func sameCandidate(a, b fingerprint.Fingerprint) bool {
	return a.EquivalentHash == b.EquivalentHash && a.ManifestHash == b.ManifestHash && a.BaseCommit == b.BaseCommit && a.HeadCommit == b.HeadCommit && a.HeadTree == b.HeadTree && a.DiffHash == b.DiffHash
}
