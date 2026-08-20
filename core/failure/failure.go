// Package failure defines the typed failures shared by Provehito.
package failure

import (
	"errors"
	"fmt"
)

// Class identifies the operational category of a failure.
type Class string

const (
	UsageOrSchema      Class = "USAGE_OR_SCHEMA"
	PolicyOrTransition Class = "POLICY_OR_TRANSITION"
	WorkspaceDrift     Class = "WORKSPACE_DRIFT"
	ToolingOrAdapter   Class = "TOOLING_OR_ADAPTER"
	CandidateOrReview  Class = "CANDIDATE_OR_REVIEW"
	Integrity          Class = "INTEGRITY"
	Concurrency        Class = "CONCURRENCY"
)

// Error is a classified failure with the operation that produced it.
type Error struct {
	Class Class
	Op    string
	Err   error
}

// Error implements error.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err == nil {
		return fmt.Sprintf("%s: %s", e.Class, e.Op)
	}
	return fmt.Sprintf("%s: %s: %v", e.Class, e.Op, e.Err)
}

// Unwrap exposes the underlying cause for errors.Is and errors.As.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// New creates a classified failure without an underlying cause.
func New(class Class, op string) *Error {
	return &Error{Class: class, Op: op}
}

// Wrap creates a classified failure around an underlying cause.
func Wrap(class Class, op string, err error) *Error {
	return &Error{Class: class, Op: op, Err: err}
}

// ExitCodeFor maps a classified failure to its process exit code.
func ExitCodeFor(err error) int {
	if err == nil {
		return 0
	}

	var classified *Error
	if !errors.As(err, &classified) {
		return 1
	}

	switch classified.Class {
	case UsageOrSchema:
		return 10
	case PolicyOrTransition:
		return 20
	case WorkspaceDrift:
		return 30
	case ToolingOrAdapter:
		return 40
	case CandidateOrReview:
		return 50
	case Integrity:
		return 60
	case Concurrency:
		return 70
	default:
		return 1
	}
}
