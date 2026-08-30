package manifest_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/provehito-project/provehito/core/clock"
	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/lifecycle"
	"github.com/provehito-project/provehito/core/manifest"
)

func opHash(char string) string { return strings.Repeat(char, 64) }

// newBlockedLane persists a lane blocked from Frozen and returns the store
// plus the hash of the blocked manifest. The setup uses the raw store
// primitives, never the seam under test. This is the shape six of seven
// handlers created risk around when they rebuilt the lifecycle snapshot by
// hand: the recorded predecessor is the data at risk.
func newBlockedLane(t *testing.T) (manifest.Store, string) {
	t.Helper()
	s := manifest.NewStore(filepath.Join(t.TempDir(), "demo.json"), clock.Fixed{Time: fixedTime()})
	m := fixtureManifest(lifecycle.Frozen)
	hash, err := s.Create(m)
	if err != nil {
		t.Fatal(err)
	}
	m.State, m.BlockedFrom = lifecycle.Blocked, lifecycle.Frozen
	blockedHash, err := s.Update(hash, m)
	if err != nil {
		t.Fatal(err)
	}
	return s, blockedHash
}

// TestApplyRecordsTheCompleteLifecycleSnapshot is the regression test for the
// BlockedFrom drop: Apply must carry the recorded predecessor into the
// transition input and must write the transitioned BlockedFrom back, so a
// blocked lane can always resume to its exact predecessor.
func TestApplyRecordsTheCompleteLifecycleSnapshot(t *testing.T) {
	s, _ := newBlockedLane(t)

	resumed, _, err := s.Apply(manifest.ExpectedHash{}, lifecycle.Resume, nil)
	if err != nil {
		t.Fatalf("resume through the operations seam failed: %v", err)
	}
	if resumed.State != lifecycle.Frozen || resumed.BlockedFrom != "" {
		t.Fatalf("resumed snapshot incomplete: state=%s blocked_from=%q", resumed.State, resumed.BlockedFrom)
	}
	stored, _, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != lifecycle.Frozen || stored.BlockedFrom != "" {
		t.Fatalf("stored resume incomplete: state=%s blocked_from=%q", stored.State, stored.BlockedFrom)
	}
}

// TestApplyRecordsBlockedFromWhenBlocking guards the write side of the same
// defect: a Block event must persist the exact predecessor.
func TestApplyRecordsBlockedFromWhenBlocking(t *testing.T) {
	s := manifest.NewStore(filepath.Join(t.TempDir(), "demo.json"), clock.Fixed{Time: fixedTime()})
	hash, err := s.Create(fixtureManifest(lifecycle.Active))
	if err != nil {
		t.Fatal(err)
	}
	blocked, _, err := s.Apply(manifest.RequiredHash(hash), lifecycle.Block, nil)
	if err != nil {
		t.Fatalf("block through the operations seam failed: %v", err)
	}
	if blocked.State != lifecycle.Blocked || blocked.BlockedFrom != lifecycle.Active {
		t.Fatalf("blocked snapshot incomplete: state=%s blocked_from=%q", blocked.State, blocked.BlockedFrom)
	}
	stored, _, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if stored.BlockedFrom != lifecycle.Active {
		t.Fatalf("stored blocked_from=%q, want %s", stored.BlockedFrom, lifecycle.Active)
	}
}

// TestMutateLeavesTheLifecycleSnapshotUntouched pins the event-less chassis:
// field mutations may never disturb state or blocked_from.
func TestMutateLeavesTheLifecycleSnapshotUntouched(t *testing.T) {
	s, _ := newBlockedLane(t)
	updated, _, err := s.Mutate(manifest.ExpectedHash{}, func(m *manifest.Manifest) {
		m.Evidence = append(m.Evidence, manifest.EvidenceReference{Name: "check-2", Hash: opHash("b")})
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != lifecycle.Blocked || updated.BlockedFrom != lifecycle.Frozen {
		t.Fatalf("mutate disturbed lifecycle: state=%s blocked_from=%q", updated.State, updated.BlockedFrom)
	}
}

func TestApplyHashExpectationPolicies(t *testing.T) {
	t.Run("required empty", func(t *testing.T) {
		s, _ := newBlockedLane(t)
		if _, _, err := s.Apply(manifest.RequiredHash(""), lifecycle.Resume, nil); failure.ExitCodeFor(err) != 60 {
			t.Fatalf("required empty: got %v", err)
		}
	})
	t.Run("required stale", func(t *testing.T) {
		s, _ := newBlockedLane(t)
		_, _, err := s.Apply(manifest.RequiredHash(opHash("c")), lifecycle.Resume, nil)
		if failure.ExitCodeFor(err) != 60 {
			t.Fatalf("required stale: got %v", err)
		}
		var classified *failure.Error
		if !errors.As(err, &classified) || classified.Op != "manifest prior hash mismatch" {
			t.Fatalf("required stale op: got %v", err)
		}
	})
	t.Run("optional stale names the operation", func(t *testing.T) {
		s, _ := newBlockedLane(t)
		_, _, err := s.Apply(manifest.OptionalHash(opHash("c"), "freeze"), lifecycle.Resume, nil)
		if failure.ExitCodeFor(err) != 60 {
			t.Fatalf("optional stale: got %v", err)
		}
		var classified *failure.Error
		if !errors.As(err, &classified) || classified.Op != "freeze manifest prior hash mismatch" {
			t.Fatalf("optional stale op: got %v", err)
		}
	})
	t.Run("optional empty proceeds", func(t *testing.T) {
		s, _ := newBlockedLane(t)
		if _, _, err := s.Apply(manifest.OptionalHash("", "freeze"), lifecycle.Resume, nil); err != nil {
			t.Fatalf("optional empty: got %v", err)
		}
	})
	t.Run("second apply from the same prior hash fails", func(t *testing.T) {
		s, blockedHash := newBlockedLane(t)
		if _, _, err := s.Apply(manifest.RequiredHash(blockedHash), lifecycle.Resume, nil); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Apply(manifest.RequiredHash(blockedHash), lifecycle.Resume, nil); failure.ExitCodeFor(err) != 60 {
			t.Fatalf("stale second apply: got %v", err)
		}
	})
}
