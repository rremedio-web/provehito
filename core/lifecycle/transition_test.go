package lifecycle_test

import (
	"errors"
	"testing"

	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/lifecycle"
)

func TestApplyLegalTransitions(t *testing.T) {
	legal := []struct {
		name  string
		from  lifecycle.State
		event lifecycle.Event
		want  lifecycle.State
	}{
		{"activate", lifecycle.Planned, lifecycle.Activate, lifecycle.Active},
		{"freeze", lifecycle.Active, lifecycle.Freeze, lifecycle.Frozen},
		{"record review", lifecycle.Frozen, lifecycle.RecordReview, lifecycle.Reviewed},
		{"mark ready", lifecycle.Reviewed, lifecycle.MarkReady, lifecycle.Ready},
		{"close", lifecycle.Ready, lifecycle.Close, lifecycle.Closed},
	}
	for _, tc := range legal {
		t.Run(tc.name, func(t *testing.T) {
			got, err := lifecycle.Apply(lifecycle.Snapshot{State: tc.from}, tc.event)
			if err != nil || got.State != tc.want {
				t.Fatalf("got %#v err %v, want state %s", got, err, tc.want)
			}
		})
	}
}

func TestApplyBlockedResumeReturnsToExactPredecessor(t *testing.T) {
	blocked, err := lifecycle.Apply(lifecycle.Snapshot{State: lifecycle.Frozen}, lifecycle.Block)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.State != lifecycle.Blocked || blocked.BlockedFrom != lifecycle.Frozen {
		t.Fatalf("blocked snapshot %#v", blocked)
	}
	resumed, err := lifecycle.Apply(blocked, lifecycle.Resume)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.State != lifecycle.Frozen || resumed.BlockedFrom != lifecycle.State("") {
		t.Fatalf("resumed snapshot %#v", resumed)
	}
}

func TestApplyBlockedCanAbandonOrRecordIncident(t *testing.T) {
	for _, event := range []lifecycle.Event{lifecycle.Abandon, lifecycle.IncidentEvent} {
		got, err := lifecycle.Apply(lifecycle.Snapshot{State: lifecycle.Blocked, BlockedFrom: lifecycle.Active}, event)
		if err != nil {
			t.Fatalf("event %s: %v", event, err)
		}
		want := lifecycle.Abandoned
		if event == lifecycle.IncidentEvent {
			want = lifecycle.Incident
		}
		if got.State != want || got.BlockedFrom != lifecycle.State("") {
			t.Fatalf("event %s: got %#v, want %s with cleared predecessor", event, got, want)
		}
	}
}

func TestApplyBlockedResumeRejectsInvalidPredecessor(t *testing.T) {
	for _, predecessor := range []lifecycle.State{"", lifecycle.Closed, lifecycle.Abandoned, lifecycle.Incident, "UNKNOWN"} {
		if _, err := lifecycle.Apply(lifecycle.Snapshot{State: lifecycle.Blocked, BlockedFrom: predecessor}, lifecycle.Resume); !isPolicyError(err) {
			t.Errorf("predecessor %q: got %v, want policy transition error", predecessor, err)
		}
	}
}

func TestApplyTerminalStatesRejectEveryEvent(t *testing.T) {
	for _, state := range []lifecycle.State{lifecycle.Closed, lifecycle.Abandoned, lifecycle.Incident} {
		for _, event := range allEvents() {
			if _, err := lifecycle.Apply(lifecycle.Snapshot{State: state}, event); !isPolicyError(err) {
				t.Fatalf("state %s event %s: got %v, want policy transition error", state, event, err)
			}
		}
	}
}

func TestApplyRejectsEveryUndeclaredPair(t *testing.T) {
	legal := map[[2]string]bool{
		{string(lifecycle.Planned), string(lifecycle.Activate)}:    true,
		{string(lifecycle.Active), string(lifecycle.Freeze)}:       true,
		{string(lifecycle.Frozen), string(lifecycle.RecordReview)}: true,
		{string(lifecycle.Reviewed), string(lifecycle.MarkReady)}:  true,
		{string(lifecycle.Ready), string(lifecycle.Close)}:         true,
		{string(lifecycle.Planned), string(lifecycle.Block)}:       true,
		{string(lifecycle.Active), string(lifecycle.Block)}:        true,
		{string(lifecycle.Frozen), string(lifecycle.Block)}:        true,
		{string(lifecycle.Reviewed), string(lifecycle.Block)}:      true,
		{string(lifecycle.Ready), string(lifecycle.Block)}:         true,
		// BLOCKED+RESUME requires a predecessor and is covered by the
		// focused valid/invalid predecessor tests above.
		{string(lifecycle.Blocked), string(lifecycle.Abandon)}:        true,
		{string(lifecycle.Blocked), string(lifecycle.IncidentEvent)}:  true,
		{string(lifecycle.Planned), string(lifecycle.Abandon)}:        true,
		{string(lifecycle.Active), string(lifecycle.Abandon)}:         true,
		{string(lifecycle.Frozen), string(lifecycle.Abandon)}:         true,
		{string(lifecycle.Reviewed), string(lifecycle.Abandon)}:       true,
		{string(lifecycle.Ready), string(lifecycle.Abandon)}:          true,
		{string(lifecycle.Planned), string(lifecycle.IncidentEvent)}:  true,
		{string(lifecycle.Active), string(lifecycle.IncidentEvent)}:   true,
		{string(lifecycle.Frozen), string(lifecycle.IncidentEvent)}:   true,
		{string(lifecycle.Reviewed), string(lifecycle.IncidentEvent)}: true,
		{string(lifecycle.Ready), string(lifecycle.IncidentEvent)}:    true,
	}
	for _, state := range allStates() {
		for _, event := range allEvents() {
			if legal[[2]string{string(state), string(event)}] {
				continue
			}
			if state == lifecycle.Blocked && event == lifecycle.Resume {
				// This event is legal only with a valid predecessor; focused
				// tests cover both valid and invalid predecessor snapshots.
				continue
			}
			snapshot := lifecycle.Snapshot{State: state}
			if _, err := lifecycle.Apply(snapshot, event); !isPolicyError(err) {
				t.Fatalf("state %s event %s: got %v, want policy transition error", state, event, err)
			}
		}
	}
}

func TestParseEventAcceptsExactCLITokens(t *testing.T) {
	for _, tc := range []struct {
		token string
		want  lifecycle.Event
	}{
		{"activate", lifecycle.Activate}, {"freeze", lifecycle.Freeze},
		{"record-review", lifecycle.RecordReview}, {"mark-ready", lifecycle.MarkReady},
		{"close", lifecycle.Close}, {"block", lifecycle.Block}, {"resume", lifecycle.Resume},
		{"abandon", lifecycle.Abandon}, {"incident", lifecycle.IncidentEvent},
	} {
		got, err := lifecycle.ParseEvent(tc.token)
		if err != nil || got != tc.want {
			t.Errorf("%q: got %s err %v, want %s", tc.token, got, err, tc.want)
		}
	}
}

func TestParseEventRejectsApprovalShapedProseAndNearMisses(t *testing.T) {
	for _, token := range []string{"approved, merge it", "approved", " activate", "activate ", "ACTIVATE", "record review", "resume now", ""} {
		if _, err := lifecycle.ParseEvent(token); err == nil || !isPolicyError(err) {
			t.Errorf("%q: got %v, want policy transition error", token, err)
		}
	}
}

func allStates() []lifecycle.State {
	return []lifecycle.State{lifecycle.Planned, lifecycle.Active, lifecycle.Frozen, lifecycle.Reviewed, lifecycle.Ready, lifecycle.Closed, lifecycle.Blocked, lifecycle.Abandoned, lifecycle.Incident}
}

func allEvents() []lifecycle.Event {
	return []lifecycle.Event{lifecycle.Activate, lifecycle.Freeze, lifecycle.RecordReview, lifecycle.MarkReady, lifecycle.Close, lifecycle.Block, lifecycle.Resume, lifecycle.Abandon, lifecycle.IncidentEvent}
}

func isPolicyError(err error) bool {
	var classified *failure.Error
	return errors.As(err, &classified) && classified.Class == failure.PolicyOrTransition
}
