package manifest

import (
	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/lifecycle"
)

// ExpectedHash is the caller-facing compare-and-swap expectation of one lane
// mutation. The zero value applies the mutation without a caller
// expectation; the write still replaces only the manifest the store loaded
// under its lock.
type ExpectedHash struct {
	value    string
	required bool
	op       string
}

// OptionalHash expects value to match the loaded manifest when nonempty. op
// names the operation in the stale-hash diagnostic (for example "freeze").
func OptionalHash(value, op string) ExpectedHash { return ExpectedHash{value: value, op: op} }

// RequiredHash rejects an empty value and expects a match. The lane
// transition verbs use it.
func RequiredHash(value string) ExpectedHash { return ExpectedHash{value: value, required: true} }

func (e ExpectedHash) check(hash string) error {
	if e.required && e.value == "" {
		return failure.New(failure.Integrity, "expected prior hash required")
	}
	if e.value != "" && e.value != hash {
		if e.required {
			return failure.New(failure.Integrity, "manifest prior hash mismatch")
		}
		return failure.New(failure.Integrity, e.op+" manifest prior hash mismatch")
	}
	return nil
}

// Apply loads the lane manifest, applies one declared lifecycle event with
// an optional field mutation under expected, and writes the result
// atomically. It owns the compare-and-swap policy, the lifecycle transition,
// and the update timestamp; handlers translate flags and render results.
func (s Store) Apply(expected ExpectedHash, event lifecycle.Event, mutate func(*Manifest)) (Manifest, string, error) {
	m, hash, err := s.Load()
	if err != nil {
		return Manifest{}, "", err
	}
	if err := expected.check(hash); err != nil {
		return Manifest{}, "", err
	}
	snapshot, err := lifecycle.Apply(lifecycle.Snapshot{State: m.State, BlockedFrom: m.BlockedFrom}, event)
	if err != nil {
		return Manifest{}, "", err
	}
	m.State, m.BlockedFrom = snapshot.State, snapshot.BlockedFrom
	if mutate != nil {
		mutate(&m)
	}
	newHash, err := s.Update(hash, m)
	if err != nil {
		return Manifest{}, "", err
	}
	return m, newHash, nil
}

// Mutate changes fields of the loaded lane manifest under expected without a
// lifecycle transition, preserving the lifecycle snapshot untouched.
func (s Store) Mutate(expected ExpectedHash, mutate func(*Manifest)) (Manifest, string, error) {
	m, hash, err := s.Load()
	if err != nil {
		return Manifest{}, "", err
	}
	if err := expected.check(hash); err != nil {
		return Manifest{}, "", err
	}
	if mutate != nil {
		mutate(&m)
	}
	newHash, err := s.Update(hash, m)
	if err != nil {
		return Manifest{}, "", err
	}
	return m, newHash, nil
}
