package lifecycle

import "github.com/rremedio-web/provehito/core/failure"

// ParseEvent parses one exact CLI event token. It deliberately does not
// normalize or interpret prose, so untrusted agent output cannot become an
// authority-bearing lifecycle event.
func ParseEvent(token string) (Event, error) {
	event := Event(token)
	for _, allowed := range allEvents {
		if event == allowed {
			return event, nil
		}
	}
	return "", failure.New(failure.PolicyOrTransition, "lifecycle parse event")
}

// Apply applies one declared event to a snapshot.
func Apply(snapshot Snapshot, event Event) (Snapshot, error) {
	switch snapshot.State {
	case Planned:
		switch event {
		case Activate:
			return Snapshot{State: Active}, nil
		case Block:
			return blocked(snapshot.State), nil
		case Abandon:
			return Snapshot{State: Abandoned}, nil
		case IncidentEvent:
			return Snapshot{State: Incident}, nil
		}
	case Active:
		switch event {
		case Freeze:
			return Snapshot{State: Frozen}, nil
		case Block:
			return blocked(snapshot.State), nil
		case Abandon:
			return Snapshot{State: Abandoned}, nil
		case IncidentEvent:
			return Snapshot{State: Incident}, nil
		}
	case Frozen:
		switch event {
		case RecordReview:
			return Snapshot{State: Reviewed}, nil
		case Block:
			return blocked(snapshot.State), nil
		case Abandon:
			return Snapshot{State: Abandoned}, nil
		case IncidentEvent:
			return Snapshot{State: Incident}, nil
		}
	case Reviewed:
		switch event {
		case MarkReady:
			return Snapshot{State: Ready}, nil
		case Block:
			return blocked(snapshot.State), nil
		case Abandon:
			return Snapshot{State: Abandoned}, nil
		case IncidentEvent:
			return Snapshot{State: Incident}, nil
		}
	case Ready:
		switch event {
		case Close:
			return Snapshot{State: Closed}, nil
		case Block:
			return blocked(snapshot.State), nil
		case Abandon:
			return Snapshot{State: Abandoned}, nil
		case IncidentEvent:
			return Snapshot{State: Incident}, nil
		}
	case Blocked:
		switch event {
		case Resume:
			if BlockableFrom(snapshot.BlockedFrom) {
				return Snapshot{State: snapshot.BlockedFrom}, nil
			}
		case Abandon:
			return Snapshot{State: Abandoned}, nil
		case IncidentEvent:
			return Snapshot{State: Incident}, nil
		}
	case Closed, Abandoned, Incident:
		// Terminal states reject every event, including events that would
		// otherwise be legal from a non-terminal predecessor.
	}
	return Snapshot{}, failure.New(failure.PolicyOrTransition, "lifecycle apply")
}

func blocked(from State) Snapshot { return Snapshot{State: Blocked, BlockedFrom: from} }

var allEvents = []Event{Activate, Freeze, RecordReview, MarkReady, Close, Block, Resume, Abandon, IncidentEvent}
