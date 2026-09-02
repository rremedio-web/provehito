package clock_test

import (
	"testing"
	"time"

	"github.com/rremedio-web/provehito/core/clock"
)

func TestFixedReturnsConfiguredTime(t *testing.T) {
	want := time.Date(2026, 8, 19, 12, 34, 56, 0, time.UTC)
	if got := (clock.Fixed{Time: want}).Now(); !got.Equal(want) {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestSystemReturnsAUTCInstant(t *testing.T) {
	if got := (clock.System{}).Now(); got.Location() != time.UTC {
		t.Fatalf("system clock location: got %s want UTC", got.Location())
	}
}
