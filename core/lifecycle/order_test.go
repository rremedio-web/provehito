package lifecycle_test

import (
	"testing"

	"github.com/provehito-project/provehito/core/lifecycle"
)

func TestOrderingPredicatesMatchTheLifecyclePosition(t *testing.T) {
	cases := []struct {
		name              string
		snapshot          lifecycle.Snapshot
		atOrAfterActive   bool
		atOrAfterFrozen   bool
		atOrAfterReviewed bool
	}{
		{"planned", lifecycle.Snapshot{State: lifecycle.Planned}, false, false, false},
		{"active", lifecycle.Snapshot{State: lifecycle.Active}, true, false, false},
		{"frozen", lifecycle.Snapshot{State: lifecycle.Frozen}, true, true, false},
		{"reviewed", lifecycle.Snapshot{State: lifecycle.Reviewed}, true, true, true},
		{"ready", lifecycle.Snapshot{State: lifecycle.Ready}, true, true, true},
		{"closed", lifecycle.Snapshot{State: lifecycle.Closed}, true, true, true},
		{"blocked from planned", lifecycle.Snapshot{State: lifecycle.Blocked, BlockedFrom: lifecycle.Planned}, false, false, false},
		{"blocked from active", lifecycle.Snapshot{State: lifecycle.Blocked, BlockedFrom: lifecycle.Active}, true, false, false},
		{"blocked from frozen", lifecycle.Snapshot{State: lifecycle.Blocked, BlockedFrom: lifecycle.Frozen}, true, true, false},
		{"blocked from reviewed", lifecycle.Snapshot{State: lifecycle.Blocked, BlockedFrom: lifecycle.Reviewed}, true, true, true},
		{"blocked from ready", lifecycle.Snapshot{State: lifecycle.Blocked, BlockedFrom: lifecycle.Ready}, true, true, true},
		{"blocked from closed", lifecycle.Snapshot{State: lifecycle.Blocked, BlockedFrom: lifecycle.Closed}, true, true, true},
		{"blocked from terminal", lifecycle.Snapshot{State: lifecycle.Blocked, BlockedFrom: lifecycle.Abandoned}, false, false, false},
		{"abandoned", lifecycle.Snapshot{State: lifecycle.Abandoned}, false, false, false},
		{"incident", lifecycle.Snapshot{State: lifecycle.Incident}, false, false, false},
		{"unknown", lifecycle.Snapshot{State: "UNKNOWN"}, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := lifecycle.AtOrAfterActive(tc.snapshot); got != tc.atOrAfterActive {
				t.Errorf("AtOrAfterActive: got %t", got)
			}
			if got := lifecycle.AtOrAfterFrozen(tc.snapshot); got != tc.atOrAfterFrozen {
				t.Errorf("AtOrAfterFrozen: got %t", got)
			}
			if got := lifecycle.AtOrAfterReviewed(tc.snapshot); got != tc.atOrAfterReviewed {
				t.Errorf("AtOrAfterReviewed: got %t", got)
			}
		})
	}
}

func TestKnownStateAcceptsExactlyTheDeclaredStates(t *testing.T) {
	for _, state := range allStates() {
		if !lifecycle.KnownState(state) {
			t.Errorf("state %s should be known", state)
		}
	}
	for _, state := range []lifecycle.State{"", "active", "unknown", "blocked"} {
		if lifecycle.KnownState(state) {
			t.Errorf("state %q should not be known", state)
		}
	}
}

func TestBlockableFromAcceptsExactlyTheResumeTargets(t *testing.T) {
	resumable := map[lifecycle.State]bool{
		lifecycle.Planned: true, lifecycle.Active: true, lifecycle.Frozen: true,
		lifecycle.Reviewed: true, lifecycle.Ready: true,
	}
	for _, state := range allStates() {
		if got := lifecycle.BlockableFrom(state); got != resumable[state] {
			t.Errorf("state %s: got %t", state, got)
		}
	}
	if lifecycle.BlockableFrom("") || lifecycle.BlockableFrom("UNKNOWN") {
		t.Error("undeclared states must not be blockable predecessors")
	}
}
