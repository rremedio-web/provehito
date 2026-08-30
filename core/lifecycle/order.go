package lifecycle

// forwardOrder is the single lifecycle position table. Every exported
// ordering predicate derives from it, so the order of states is declared in
// exactly one place.
func rank(state State) (int, bool) {
	switch state {
	case Planned:
		return 0, true
	case Active:
		return 1, true
	case Frozen:
		return 2, true
	case Reviewed:
		return 3, true
	case Ready:
		return 4, true
	case Closed:
		return 5, true
	default:
		return 0, false
	}
}

// AtOrAfterActive reports whether the snapshot is Active or any later state
// in the lifecycle order, resolving a Blocked snapshot to its recorded
// predecessor. Abandoned and Incident are terminal side exits and never
// order past a predecessor.
func AtOrAfterActive(snapshot Snapshot) bool { return atOrAfter(snapshot, Active) }

// AtOrAfterFrozen reports whether the snapshot is Frozen or any later state
// in the lifecycle order, resolving a Blocked snapshot to its recorded
// predecessor.
func AtOrAfterFrozen(snapshot Snapshot) bool { return atOrAfter(snapshot, Frozen) }

// AtOrAfterReviewed reports whether the snapshot is Reviewed or any later
// state in the lifecycle order, resolving a Blocked snapshot to its recorded
// predecessor.
func AtOrAfterReviewed(snapshot Snapshot) bool { return atOrAfter(snapshot, Reviewed) }

func atOrAfter(snapshot Snapshot, floor State) bool {
	state := snapshot.State
	if state == Blocked {
		state = snapshot.BlockedFrom
	}
	position, ok := rank(state)
	if !ok {
		return false
	}
	floorPosition, _ := rank(floor)
	return position >= floorPosition
}

// KnownState reports whether state is declared by this package.
func KnownState(state State) bool {
	if _, ok := rank(state); ok {
		return true
	}
	return state == Blocked || state == Abandoned || state == Incident
}

// BlockableFrom reports whether state may be recorded as a BlockedFrom
// predecessor: the non-terminal states to which Resume can return.
func BlockableFrom(state State) bool {
	_, ok := rank(state)
	return ok && state != Closed
}
