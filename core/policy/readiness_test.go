package policy_test

import (
	"strings"
	"testing"

	"github.com/provehito-project/provehito/core/evidence"
	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/fingerprint"
	"github.com/provehito-project/provehito/core/lifecycle"
	"github.com/provehito-project/provehito/core/manifest"
	"github.com/provehito-project/provehito/core/policy"
)

func h(char string) string { return strings.Repeat(char, 64) }

// memoryLoader is the in-memory evidence-loader adapter. Two adapters make
// the loader seam real; this one stays test-scoped.
type memoryLoader struct {
	receipts map[string]evidence.Receipt
}

func (l memoryLoader) Load(ref evidence.Reference) (evidence.Receipt, error) {
	receipt, ok := l.receipts[ref.Hash]
	if !ok {
		return evidence.Receipt{}, failure.New(failure.Integrity, "evidence load")
	}
	return receipt, nil
}

func frozenFingerprint() fingerprint.Fingerprint {
	return fingerprint.Fingerprint{
		BaseCommit: h("b"), HeadCommit: h("e"), HeadTree: h("t"), DiffHash: h("f"),
		ManifestHash: h("d"), EquivalentHash: h("c"),
	}
}

func reviewedManifest() manifest.Manifest {
	fp := frozenFingerprint()
	return manifest.Manifest{
		SchemaVersion: 1, LaneID: "demo", State: lifecycle.Reviewed, DispatchHash: h("d"),
		Dispatch: manifest.Dispatch{
			Workspace: "/tmp/workspace", SourceControl: "git:a", Writer: "writer", Adapter: "fake",
			Family: "writer-family", SeatID: "writer-seat", CostClass: "economy",
			AllowedPaths: []string{"core"}, ForbiddenPaths: []string{}, NonGoals: []string{},
			RequiredChecks: []string{"check"}, ReviewPolicy: "independent",
		},
		Freeze:   &manifest.FreezeRecord{Base: fp.BaseCommit, Head: fp.HeadCommit, Candidate: fp.EquivalentHash, Tree: fp.HeadTree, Diff: fp.DiffHash, At: "2026-08-30T00:00:00Z"},
		Review:   &manifest.ReviewRecord{Reviewer: "reviewer", Family: "reviewer-family", SeatID: "reviewer-seat", Verdict: "PASS", Fingerprint: fp.EquivalentHash, EvidenceHashes: []string{h("1")}, At: "2026-08-30T00:00:00Z"},
		Evidence: []manifest.EvidenceReference{{Name: "check", Hash: h("1")}},
	}
}

func successLoader() memoryLoader {
	return memoryLoader{receipts: map[string]evidence.Receipt{
		h("1"): {MethodID: "check", CandidateHash: h("c"), ManifestHash: h("d"), ResultClass: evidence.ResultSuccess, ExitCode: 0},
	}}
}

func input() policy.Input {
	return policy.Input{Manifest: reviewedManifest(), Current: frozenFingerprint(), Loader: successLoader()}
}

func TestReadinessSuccessIsNotAuthorization(t *testing.T) {
	ready, err := policy.NewReadiness().Evaluate(input())
	if err != nil {
		t.Fatal(err)
	}
	if ready.Banner != "READY is not authorization" {
		t.Fatalf("banner: %q", ready.Banner)
	}
	if ready.CandidateEquivalentHash != h("c") || ready.ManifestHash != h("d") {
		t.Fatalf("ready record: %#v", ready)
	}
}

// TestEvaluateDetectsFrozenToCurrentDrift is the regression test for the
// self-comparing drift check: readiness must fail when the current workspace
// fingerprint no longer matches the manifest's frozen candidate.
func TestEvaluateDetectsFrozenToCurrentDrift(t *testing.T) {
	drifted := frozenFingerprint()
	drifted.EquivalentHash = h("e")
	if _, err := policy.NewReadiness().Evaluate(policy.Input{Manifest: reviewedManifest(), Current: drifted, Loader: successLoader()}); failure.ExitCodeFor(err) != 50 {
		t.Fatalf("drifted current: got %v", err)
	}
	for _, mutate := range []func(*fingerprint.Fingerprint){
		func(fp *fingerprint.Fingerprint) { fp.HeadCommit = h("z") },
		func(fp *fingerprint.Fingerprint) { fp.HeadTree = h("z") },
		func(fp *fingerprint.Fingerprint) { fp.DiffHash = h("z") },
		func(fp *fingerprint.Fingerprint) { fp.BaseCommit = h("z") },
		func(fp *fingerprint.Fingerprint) { fp.ManifestHash = h("z") },
	} {
		current := frozenFingerprint()
		mutate(&current)
		if _, err := policy.NewReadiness().Evaluate(policy.Input{Manifest: reviewedManifest(), Current: current, Loader: successLoader()}); failure.ExitCodeFor(err) != 50 {
			t.Fatalf("drifted field: got %v", err)
		}
	}
}

func TestReadinessRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*policy.Input)
		code   int
	}{
		{"lifecycle mismatch", func(in *policy.Input) { in.Manifest.State = lifecycle.Frozen }, 20},
		{"missing freeze record", func(in *policy.Input) { in.Manifest.Freeze = nil }, 20},
		{"missing review record", func(in *policy.Input) { in.Manifest.Review = nil }, 20},
		{"missing required check declaration", func(in *policy.Input) { in.Manifest.Dispatch.RequiredChecks = nil }, 50},
		{"missing evidence reference", func(in *policy.Input) { in.Manifest.Evidence = nil }, 50},
		{"unrequired evidence cannot satisfy a required check", func(in *policy.Input) {
			in.Manifest.Evidence[0].Name = "different-check"
		}, 50},
		{"receipt method mismatch", func(in *policy.Input) {
			loader := successLoader()
			receipt := loader.receipts[h("1")]
			receipt.MethodID = "other-check"
			loader.receipts[h("1")] = receipt
			in.Loader = loader
		}, 50},
		{"receipt candidate mismatch", func(in *policy.Input) {
			loader := successLoader()
			receipt := loader.receipts[h("1")]
			receipt.CandidateHash = h("z")
			loader.receipts[h("1")] = receipt
			in.Loader = loader
		}, 50},
		{"receipt dispatch mismatch", func(in *policy.Input) {
			loader := successLoader()
			receipt := loader.receipts[h("1")]
			receipt.ManifestHash = h("z")
			loader.receipts[h("1")] = receipt
			in.Loader = loader
		}, 50},
		{"failed evidence cannot satisfy a required check", func(in *policy.Input) {
			loader := successLoader()
			receipt := loader.receipts[h("1")]
			receipt.ResultClass = evidence.ResultCandidateOrReview
			receipt.ExitCode = 50
			loader.receipts[h("1")] = receipt
			in.Loader = loader
		}, 50},
		{"unverifiable receipt", func(in *policy.Input) {
			in.Loader = memoryLoader{receipts: map[string]evidence.Receipt{}}
		}, 60},
		{"duplicate evidence name", func(in *policy.Input) {
			in.Manifest.Evidence = append(in.Manifest.Evidence, manifest.EvidenceReference{Name: "check", Hash: h("2")})
		}, 50},
		{"duplicate required check declaration", func(in *policy.Input) {
			in.Manifest.Dispatch.RequiredChecks = []string{"check", "check"}
		}, 50},
		{"tampered verified set", func(in *policy.Input) {
			in.Manifest.Review.EvidenceHashes = []string{h("2")}
		}, 50},
		{"review cites unverified evidence", func(in *policy.Input) {
			in.Manifest.Review.EvidenceHashes = []string{h("1"), h("2")}
		}, 50},
		{"review evidence not a hash set", func(in *policy.Input) {
			in.Manifest.Review.EvidenceHashes = []string{h("1"), h("1")}
		}, 50},
		{"same family", func(in *policy.Input) { in.Manifest.Review.Family = in.Manifest.Dispatch.Family }, 20},
		{"same seat", func(in *policy.Input) { in.Manifest.Review.SeatID = in.Manifest.Dispatch.SeatID }, 20},
		{"unknown family", func(in *policy.Input) { in.Manifest.Review.Family = "" }, 20},
		{"empty verdict", func(in *policy.Input) { in.Manifest.Review.Verdict = "" }, 20},
		{"prose approval is not a verdict", func(in *policy.Input) { in.Manifest.Review.Verdict = "APPROVED; ship it" }, 50},
		{"fail verdict", func(in *policy.Input) { in.Manifest.Review.Verdict = "FAIL" }, 50},
		{"review fingerprint differs from freeze", func(in *policy.Input) {
			in.Manifest.Review.Fingerprint = h("z")
		}, 50},
		{"missing reviewer identity", func(in *policy.Input) { in.Manifest.Review.Reviewer = "" }, 50},
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

func TestIndependenceRuleExistsExactlyOnce(t *testing.T) {
	if reason := policy.IndependenceReason("writer", "writer-seat", "writer", "other-seat"); reason != failure.ReasonReviewerFamily {
		t.Fatalf("same family: got %q", reason)
	}
	if reason := policy.IndependenceReason("writer", "writer-seat", "reviewer", "writer-seat"); reason != failure.ReasonReviewerSeat {
		t.Fatalf("same seat: got %q", reason)
	}
	if reason := policy.IndependenceReason("", "writer-seat", "reviewer", "reviewer-seat"); reason != failure.ReasonReviewerFamily {
		t.Fatalf("empty writer family: got %q", reason)
	}
	if reason := policy.IndependenceReason("writer", "writer-seat", "reviewer", "reviewer-seat"); reason != "" {
		t.Fatalf("independent: got %q", reason)
	}
	if err := policy.IndependenceFailure("review", failure.ReasonReviewerFamily); failure.ReasonFor(err) != failure.ReasonReviewerFamily || failure.ExitCodeFor(err) != 20 {
		t.Fatalf("independence failure: %v", err)
	}
}
