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

// Reason is a typed, rename-safe cause a presenter may dispatch on. Unlike
// Op, which is free-text diagnostics, a Reason is a declared constant, so a
// correction keyed on it fails to compile rather than silently degrading.
type Reason string

const (
	// ReasonReviewerFamily marks a review rejected because the reviewer
	// family is not independent of the writer family.
	ReasonReviewerFamily Reason = "reviewer_family"
	// ReasonReviewerSeat marks a review rejected because the reviewer seat
	// is not independent of the writer seat.
	ReasonReviewerSeat Reason = "reviewer_seat"
	// ReasonWriterSeat marks an agent run rejected because the caller seat
	// is not the writer seat declared by the dispatch.
	ReasonWriterSeat Reason = "writer_seat"
)

// Error is a classified failure with the operation that produced it.
type Error struct {
	Class  Class
	Op     string
	Reason Reason
	Err    error
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

// NewReason creates a classified failure carrying a typed reason a presenter
// may dispatch on.
func NewReason(class Class, op string, reason Reason) *Error {
	return &Error{Class: class, Op: op, Reason: reason}
}

// ReasonFor returns the typed reason of a classified failure. It returns the
// empty reason for unclassified failures and classified failures without one.
func ReasonFor(err error) Reason {
	var classified *Error
	if !errors.As(err, &classified) {
		return ""
	}
	if classified == nil {
		return ""
	}
	return classified.Reason
}

// Is reports whether err is a classified failure of the given class. It
// inspects wrapped causes and returns false for unclassified errors.
func Is(err error, class Class) bool {
	var classified *Error
	if !errors.As(err, &classified) {
		return false
	}
	return classified != nil && classified.Class == class
}

// CodeFor maps a failure class to its stable exit code. This is the only
// class-to-exit-code table in the codebase; the second return reports whether
// the class is known.
func CodeFor(class Class) (int, bool) {
	switch class {
	case UsageOrSchema:
		return 10, true
	case PolicyOrTransition:
		return 20, true
	case WorkspaceDrift:
		return 30, true
	case ToolingOrAdapter:
		return 40, true
	case CandidateOrReview:
		return 50, true
	case Integrity:
		return 60, true
	case Concurrency:
		return 70, true
	default:
		return 0, false
	}
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

	code, ok := CodeFor(classified.Class)
	if !ok {
		return 1
	}
	return code
}

// IsHash reports whether value is a 64-character lowercase hexadecimal
// SHA-256 content hash. It is the one hash-shape predicate in the codebase.
func IsHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
