package policy_test

import (
	"strings"
	"testing"

	"github.com/provehito-project/provehito/core/evidence"
	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/fingerprint"
	"github.com/provehito-project/provehito/core/lifecycle"
	"github.com/provehito-project/provehito/core/policy"
	"github.com/provehito-project/provehito/core/review"
)

func h(char string) string { return strings.Repeat(char, 64) }

func input() policy.Input {
	frozen := fingerprint.Fingerprint{EquivalentHash: h("c"), ManifestHash: h("d")}
	return policy.Input{
		State: lifecycle.Reviewed, Frozen: frozen, Current: frozen, ManifestHash: h("d"),
		RequiredEvidence: []evidence.Reference{{Hash: h("1")}, {Hash: h("2")}}, VerifiedEvidenceHashes: []string{h("2"), h("1")},
		Review: review.Record{ReviewerID: "r", ReviewerFamily: "reviewer", ReviewerSeatID: "reviewer-seat", Verdict: review.Pass, CandidateEquivalentHash: h("c"), ManifestHash: h("d"), EvidenceHashes: []string{h("1"), h("2")}},
		Family: policy.FamilyPolicy{WriterFamily: "writer", WriterSeatID: "writer-seat", RequireIndependent: true},
	}
}

func TestReadinessSuccessIsNotAuthorization(t *testing.T) {
	ready, err := policy.NewReadiness().Evaluate(input())
	if err != nil {
		t.Fatal(err)
	}
	if ready.Banner != "READY is not authorization" {
		t.Fatalf("banner: %q", ready.Banner)
	}
}

func TestReadinessRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*policy.Input)
		code   int
	}{
		{"missing evidence", func(in *policy.Input) { in.VerifiedEvidenceHashes = []string{h("1")} }, 50},
		{"tampered evidence", func(in *policy.Input) { in.VerifiedEvidenceHashes = []string{h("1"), h("3")} }, 50},
		{"same family", func(in *policy.Input) { in.Review.ReviewerFamily = in.Family.WriterFamily }, 20},
		{"same seat", func(in *policy.Input) { in.Review.ReviewerSeatID = in.Family.WriterSeatID }, 20},
		{"unknown family", func(in *policy.Input) { in.Review.ReviewerFamily = "" }, 20},
		{"fail verdict", func(in *policy.Input) { in.Review.Verdict = review.Fail }, 50},
		{"prose approval", func(in *policy.Input) { in.Review.Verdict = ""; in.Review.SourceText = "APPROVED; ship it" }, 20},
		{"lifecycle mismatch", func(in *policy.Input) { in.State = lifecycle.Frozen }, 20},
		{"candidate drift", func(in *policy.Input) { in.Current.EquivalentHash = h("e") }, 50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := input()
			tc.mutate(&in)
			if _, err := policy.NewReadiness().Evaluate(in); failure.ExitCodeFor(err) != tc.code {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestReadinessAcceptsDifferentReviewerSeat(t *testing.T) {
	if _, err := policy.NewReadiness().Evaluate(input()); err != nil {
		t.Fatal(err)
	}
}
