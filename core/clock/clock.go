// Package clock provides injectable time sources for deterministic operations.
package clock

import "time"

// Clock is the time source used by timestamp-producing APIs.
type Clock interface {
	Now() time.Time
}

// System reads the current system clock and returns UTC instants.
type System struct{}

func (System) Now() time.Time { return time.Now().UTC() }

// Fixed always returns Time. It is intended for tests and deterministic runs.
type Fixed struct {
	Time time.Time
}

func (f Fixed) Now() time.Time { return f.Time }
