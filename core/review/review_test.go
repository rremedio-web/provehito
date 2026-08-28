package review_test

import (
	"strings"
	"testing"

	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/fingerprint"
	"github.com/provehito-project/provehito/core/review"
)

func h(char string) string { return strings.Repeat(char, 64) }

func fp(candidate, manifest string) fingerprint.Fingerprint {
	return fingerprint.Fingerprint{EquivalentHash: candidate, ManifestHash: manifest}
}

func rec(candidate, manifest, family string) review.Record {
	return review.Record{ReviewerID: "reviewer-1", ReviewerFamily: family, ReviewerSeatID: "reviewer-seat", Verdict: review.Pass, CandidateEquivalentHash: candidate, ManifestHash: manifest, EvidenceHashes: []string{h("1"), h("2")}}
}

func TestReviewFailsOnFingerprintDrift(t *testing.T) {
	if err := review.Validate(fp(h("a"), h("d")), rec(h("b"), h("d"), "reviewer-family")); failure.ExitCodeFor(err) != 50 {
		t.Fatalf("expected candidate/review failure, got %v", err)
	}
}

func TestReviewRequiresExplicitAuthorityFields(t *testing.T) {
	cases := []review.Record{
		{ReviewerFamily: "family", Verdict: review.Pass, CandidateEquivalentHash: h("a"), ManifestHash: h("d")},
		{ReviewerID: "id", Verdict: review.Pass, CandidateEquivalentHash: h("a"), ManifestHash: h("d")},
		{ReviewerID: "id", ReviewerFamily: "family", CandidateEquivalentHash: h("a"), ManifestHash: h("d")},
		{ReviewerID: "id", ReviewerFamily: "family", Verdict: "APPROVED", CandidateEquivalentHash: h("a"), ManifestHash: h("d")},
	}
	for _, record := range cases {
		if err := review.Validate(fp(h("a"), h("d")), record); failure.ExitCodeFor(err) != 50 {
			t.Fatalf("expected candidate/review failure, got %v", err)
		}
	}
}

func TestReviewRejectsMalformedHashes(t *testing.T) {
	cases := []struct {
		frozen fingerprint.Fingerprint
		record review.Record
	}{
		{fp("A"+strings.Repeat("a", 63), h("d")), rec("A"+strings.Repeat("a", 63), h("d"), "family")},
		{fp(h("a"), "short"), rec(h("a"), "short", "family")},
		{fp(h("a"), h("d")), func() review.Record { r := rec(h("a"), h("d"), "family"); r.EvidenceHashes = []string{"z"}; return r }()},
	}
	for _, tc := range cases {
		if err := review.Validate(tc.frozen, tc.record); failure.ExitCodeFor(err) != 50 {
			t.Fatalf("expected malformed hash candidate/review failure, got %v", err)
		}
	}
}
