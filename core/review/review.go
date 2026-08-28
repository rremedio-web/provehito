// Package review binds an explicit, independent review to a frozen candidate.
package review

import (
	"encoding/hex"
	"sort"

	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/fingerprint"
)

// Verdict is the only review decision understood by the core.
type Verdict string

const (
	Pass Verdict = "PASS"
	Fail Verdict = "FAIL"
)

// Record contains authority-bearing review fields. SourceText is retained only
// as untrusted context; it is never interpreted as a verdict.
type Record struct {
	ReviewerID              string
	ReviewerFamily          string
	ReviewerSeatID          string
	Verdict                 Verdict
	CandidateEquivalentHash string
	ManifestHash            string
	EvidenceHashes          []string
	SourceText              string
}

// Validate checks the review's explicit identity and decision against freeze.
func Validate(frozen fingerprint.Fingerprint, record Record) error {
	if record.ReviewerID == "" || record.ReviewerFamily == "" || record.ReviewerSeatID == "" ||
		(record.Verdict != Pass && record.Verdict != Fail) ||
		record.CandidateEquivalentHash == "" || frozen.EquivalentHash == "" ||
		record.ManifestHash == "" || frozen.ManifestHash == "" ||
		!isHash(record.CandidateEquivalentHash) || !isHash(frozen.EquivalentHash) ||
		!isHash(record.ManifestHash) || !isHash(frozen.ManifestHash) ||
		record.CandidateEquivalentHash != frozen.EquivalentHash ||
		record.ManifestHash != frozen.ManifestHash {
		return failure.New(failure.CandidateOrReview, "review binding")
	}
	if !validSet(record.EvidenceHashes) {
		return failure.New(failure.CandidateOrReview, "review evidence binding")
	}
	return nil
}

func validSet(values []string) bool {
	if len(values) == 0 {
		return false
	}
	copyValues := append([]string(nil), values...)
	sort.Strings(copyValues)
	for i, value := range copyValues {
		if !isHash(value) || i > 0 && copyValues[i-1] == value {
			return false
		}
	}
	return true
}

func isHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}

// EvidenceEqual reports equality of two content-address sets.
func EvidenceEqual(left, right []string) bool {
	if !validSet(left) || !validSet(right) || len(left) != len(right) {
		return false
	}
	a, b := append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
