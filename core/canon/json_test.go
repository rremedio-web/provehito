package canon_test

import (
	"strings"
	"testing"

	"github.com/provehito-project/provehito/core/canon"
)

func TestHashIgnoresPresentationFormatting(t *testing.T) {
	a := []byte(`{"schema_version":1,"lane_id":"demo","state":"PLANNED"}`)
	b := []byte(`{
  "state": "PLANNED", "lane_id": "demo", "schema_version": 1
}`)
	ha, err := canon.HashJSON(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := canon.HashJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Fatalf("hashes differ: %s %s", ha, hb)
	}
}

func TestCanonicalBytesRejectDuplicateKeysRecursively(t *testing.T) {
	for _, input := range []string{
		`{"a":1,"a":2}`,
		`{"outer":{"a":1,"a":2}}`,
		`{"outer":[{"a":1,"a":2}]}`,
	} {
		if _, err := canon.Bytes([]byte(input)); err == nil {
			t.Errorf("input %s: expected duplicate-key error", input)
		}
	}
}

func TestCanonicalBytesSortsKeysAndPreservesArrays(t *testing.T) {
	got, err := canon.Bytes([]byte(`{"z":["2026-08-19T11:00:00+01:00",{"b":2,"a":1}],"a":true}`))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":true,"z":["2026-08-19T11:00:00+01:00",{"a":1,"b":2}]}`
	if string(got) != want {
		t.Fatalf("canonical bytes: got %s want %s", got, want)
	}
}

func TestCanonicalBytesPreservesTimestampLookingStrings(t *testing.T) {
	got, err := canon.Bytes([]byte(`{"message":"2026-08-19T11:00:00+01:00"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"message":"2026-08-19T11:00:00+01:00"}`
	if string(got) != want {
		t.Fatalf("canonical bytes: got %s want %s", got, want)
	}
}

func TestCanonicalNumbersNormalizeEquivalentLexemes(t *testing.T) {
	for _, input := range []string{`1`, `1.0`, `1e0`} {
		got, err := canon.Bytes([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != `1` {
			t.Fatalf("%s canonicalized to %s, want 1", input, got)
		}
	}
}

func TestCanonicalNumbersHandleExactLargeNegativeAndZeroValues(t *testing.T) {
	tests := map[string]string{
		`1e400`:  `1` + repeatZeroes(400),
		`-12e-2`: `-0.12`,
		`-0.0`:   `0`,
	}
	for input, want := range tests {
		got, err := canon.Bytes([]byte(input))
		if err != nil {
			t.Fatalf("%s: %v", input, err)
		}
		if string(got) != want {
			t.Fatalf("%s canonicalized to %s, want %s", input, got, want)
		}
	}
}

func TestCanonicalNumbersRejectExpansionBeyondLimit(t *testing.T) {
	if _, err := canon.Bytes([]byte(`1e100000`)); err == nil {
		t.Fatal("expected canonical expansion-limit error")
	}
}

func repeatZeroes(count int) string {
	return strings.Repeat("0", count)
}
