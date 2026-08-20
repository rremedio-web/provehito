package failure_test

import (
	"errors"
	"testing"

	"github.com/provehito-project/provehito/core/failure"
)

func TestExitCodeFor(t *testing.T) {
	cases := []struct {
		class failure.Class
		want  int
	}{
		{failure.UsageOrSchema, 10},
		{failure.PolicyOrTransition, 20},
		{failure.WorkspaceDrift, 30},
		{failure.ToolingOrAdapter, 40},
		{failure.CandidateOrReview, 50},
		{failure.Integrity, 60},
		{failure.Concurrency, 70},
	}
	for _, tc := range cases {
		if got := failure.ExitCodeFor(failure.New(tc.class, "test")); got != tc.want {
			t.Fatalf("class %s: got %d want %d", tc.class, got, tc.want)
		}
	}
}

func TestWrapPreservesCauseAndFormatsContext(t *testing.T) {
	cause := errors.New("disk failure")
	got := failure.Wrap(failure.Integrity, "read", cause)

	if !errors.Is(got, cause) {
		t.Fatal("wrapped failure does not preserve its cause")
	}
	if want := "INTEGRITY: read: disk failure"; got.Error() != want {
		t.Fatalf("error text: got %q want %q", got.Error(), want)
	}
}

func TestExitCodeForUnknownAndNilErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "nil", err: nil, want: 0},
		{name: "ordinary error", err: errors.New("plain failure"), want: 1},
		{name: "unknown class", err: failure.New(failure.Class("UNKNOWN"), "test"), want: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := failure.ExitCodeFor(tc.err); got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
}

func TestNilErrorMethodsAreSafe(t *testing.T) {
	var err *failure.Error

	if got, want := err.Error(), "<nil>"; got != want {
		t.Fatalf("Error() on nil: got %q want %q", got, want)
	}
	if got := err.Unwrap(); got != nil {
		t.Fatalf("Unwrap() on nil: got %v want nil", got)
	}
}
