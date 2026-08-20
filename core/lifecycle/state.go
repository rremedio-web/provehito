// Package lifecycle defines the explicit lane state machine.
package lifecycle

// State is a lane lifecycle state.
type State string

const (
	Planned   State = "PLANNED"
	Active    State = "ACTIVE"
	Frozen    State = "FROZEN"
	Reviewed  State = "REVIEWED"
	Ready     State = "READY"
	Closed    State = "CLOSED"
	Blocked   State = "BLOCKED"
	Abandoned State = "ABANDONED"
	Incident  State = "INCIDENT"
)

// Event is a lifecycle event accepted by Apply.
type Event string

const (
	Activate      Event = "activate"
	Freeze        Event = "freeze"
	RecordReview  Event = "record-review"
	MarkReady     Event = "mark-ready"
	Close         Event = "close"
	Block         Event = "block"
	Resume        Event = "resume"
	Abandon       Event = "abandon"
	IncidentEvent Event = "incident"
)

// Snapshot is the observable lifecycle state. BlockedFrom is set only while
// State is Blocked and identifies the exact state to which Resume returns.
type Snapshot struct {
	State       State
	BlockedFrom State
}
