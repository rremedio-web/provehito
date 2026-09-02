package failure_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/rremedio-web/provehito/core/failure"
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
		t.Fatalf("Unwrap() on nil: got %v, want nil", got)
	}
}

func TestCodeForMatchesExitCodeForForEveryClass(t *testing.T) {
	for class, want := range map[failure.Class]int{
		failure.UsageOrSchema:      10,
		failure.PolicyOrTransition: 20,
		failure.WorkspaceDrift:     30,
		failure.ToolingOrAdapter:   40,
		failure.CandidateOrReview:  50,
		failure.Integrity:          60,
		failure.Concurrency:        70,
	} {
		code, ok := failure.CodeFor(class)
		if !ok || code != want {
			t.Fatalf("class %s: got (%d,%v), want (%d,true)", class, code, ok, want)
		}
		if got := failure.ExitCodeFor(failure.New(class, "test")); got != code {
			t.Fatalf("class %s: ExitCodeFor=%d CodeFor=%d diverge", class, got, code)
		}
	}
	if _, ok := failure.CodeFor(failure.Class("UNKNOWN")); ok {
		t.Fatal("unknown class reported as known")
	}
}

func TestIsMatchesClassThroughWrapping(t *testing.T) {
	inner := failure.New(failure.Concurrency, "workspace abandoned lease")
	wrapped := failure.Wrap(failure.Integrity, "outer", inner)
	if !failure.Is(wrapped, failure.Integrity) {
		t.Fatal("Is did not see the outermost class, unlike ExitCodeFor")
	}
	if failure.Is(wrapped, failure.Concurrency) {
		t.Fatal("Is skipped past the outermost classified error")
	}
	if failure.Is(errors.New("plain"), failure.Integrity) {
		t.Fatal("Is accepted an unclassified error")
	}
	if failure.Is(nil, failure.Integrity) {
		t.Fatal("Is accepted nil")
	}
	if !failure.Is(inner, failure.Concurrency) {
		t.Fatal("Is rejected a directly classified error")
	}
}

func TestReasonForSurvivesWrappingAndDefaultsToEmpty(t *testing.T) {
	reasoned := failure.NewReason(failure.PolicyOrTransition, "review reviewer seat", failure.ReasonReviewerSeat)
	wrapped := fmt.Errorf("context: %w", reasoned)
	if got := failure.ReasonFor(wrapped); got != failure.ReasonReviewerSeat {
		t.Fatalf("wrapped reason: got %q want %q", got, failure.ReasonReviewerSeat)
	}
	if got := failure.ReasonFor(failure.New(failure.Integrity, "plain op")); got != "" {
		t.Fatalf("reasonless failure: got %q want empty", got)
	}
	if got := failure.ReasonFor(errors.New("plain")); got != "" {
		t.Fatalf("unclassified failure: got %q want empty", got)
	}
	if got := failure.ReasonFor(nil); got != "" {
		t.Fatalf("nil failure: got %q want empty", got)
	}
}

func TestIsHashAcceptsOnly64LowercaseHex(t *testing.T) {
	valid := []string{
		strings.Repeat("0", 64),
		strings.Repeat("a", 64),
		"0123456789abcdef" + strings.Repeat("0", 48),
	}
	for _, value := range valid {
		if !failure.IsHash(value) {
			t.Fatalf("rejected valid hash %q", value)
		}
	}
	invalid := []string{
		"",
		"short",
		strings.Repeat("0", 63),
		strings.Repeat("0", 65),
		strings.Repeat("A", 64),
		"g" + strings.Repeat("0", 63),
		" " + strings.Repeat("0", 63),
	}
	for _, value := range invalid {
		if failure.IsHash(value) {
			t.Fatalf("accepted invalid hash %q", value)
		}
	}
}
