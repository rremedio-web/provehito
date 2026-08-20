// Package manifest defines the canonical, durable lane manifest.
package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"time"

	"github.com/provehito-project/provehito/core/canon"
	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/lifecycle"
)

// Manifest is the complete durable description of one lane. Declared timestamp
// fields are normalized to UTC RFC3339 seconds before persistence.
type Manifest struct {
	SchemaVersion            int                 `json:"schema_version"`
	LaneID                   string              `json:"lane_id"`
	State                    lifecycle.State     `json:"state"`
	BlockedFrom              lifecycle.State     `json:"blocked_from,omitempty"`
	Dispatch                 Dispatch            `json:"dispatch"`
	DispatchHash             string              `json:"dispatch_hash"`
	Freeze                   *FreezeRecord       `json:"freeze,omitempty"`
	Review                   *ReviewRecord       `json:"review,omitempty"`
	Evidence                 []EvidenceReference `json:"evidence,omitempty"`
	Failures                 []FailureRecord     `json:"failures,omitempty"`
	CreatedAt                string              `json:"created_at"`
	UpdatedAt                string              `json:"updated_at"`
	ExternalActionsHumanOnly bool                `json:"external_actions_human_only"`
}

// Dispatch contains all inputs that determine how a lane may be run.
type Dispatch struct {
	Workspace      string   `json:"workspace"`
	SourceControl  string   `json:"source_control"`
	Writer         string   `json:"writer"`
	Adapter        string   `json:"adapter"`
	Family         string   `json:"family"`
	CostClass      string   `json:"cost_class"`
	AllowedPaths   []string `json:"allowed_paths"`
	ForbiddenPaths []string `json:"forbidden_paths"`
	NonGoals       []string `json:"non_goals"`
	RequiredChecks []string `json:"required_checks"`
	ReviewPolicy   string   `json:"review_policy"`
	MaxSeconds     int64    `json:"max_seconds"`
	MaxOutputBytes int64    `json:"max_output_bytes"`
	MaxMemoryBytes int64    `json:"max_memory_bytes"`
}

// FreezeRecord binds a frozen lane to exact candidate fingerprints.
type FreezeRecord struct {
	Base      string `json:"base"`
	Head      string `json:"head"`
	Candidate string `json:"candidate"`
	Tree      string `json:"tree"`
	Diff      string `json:"diff"`
	At        string `json:"at"`
}

// ReviewRecord binds a review verdict to the frozen diff fingerprint.
type ReviewRecord struct {
	Reviewer       string   `json:"reviewer"`
	Family         string   `json:"family"`
	Verdict        string   `json:"verdict"`
	Fingerprint    string   `json:"fingerprint"`
	EvidenceHashes []string `json:"evidence_hashes"`
	At             string   `json:"at"`
}

// EvidenceReference points at a separately content-addressed receipt.
type EvidenceReference struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
}

// FailureRecord preserves a typed failure observed while operating a lane.
type FailureRecord struct {
	Class string `json:"class"`
	Op    string `json:"op"`
	At    string `json:"at"`
	Error string `json:"error"`
}

// ValidateUpdate enforces the manifest immutability boundaries against the
// expected canonical hash of before. The hash check rejects shallow-copy alias
// mutations before comparing dispatch, freeze, and review fields. It
// deliberately does not apply lifecycle transitions.
func ValidateUpdate(expectedHash string, before, after Manifest) error {
	if expectedHash == "" {
		return failure.New(failure.Integrity, "manifest missing prior hash")
	}
	_, _, actualHash, err := normalize(before)
	if err != nil {
		return failure.Wrap(failure.Integrity, "manifest prior hash", err)
	}
	if actualHash != expectedHash {
		return failure.New(failure.Integrity, "manifest prior hash mismatch")
	}
	if before.SchemaVersion != after.SchemaVersion || before.LaneID != after.LaneID {
		return failure.New(failure.PolicyOrTransition, "manifest immutable identity")
	}
	if before.CreatedAt != after.CreatedAt {
		return failure.New(failure.PolicyOrTransition, "manifest created timestamp immutable")
	}
	if (isAtOrAfterActive(before) || isAtOrAfterActive(after)) && !reflect.DeepEqual(before.Dispatch, after.Dispatch) {
		return failure.New(failure.PolicyOrTransition, "manifest dispatch immutable")
	}
	if (isAtOrAfterActive(before) || isAtOrAfterActive(after)) && before.DispatchHash != after.DispatchHash {
		return failure.New(failure.PolicyOrTransition, "manifest dispatch hash immutable")
	}
	if isAtOrAfterFrozen(before) && !reflect.DeepEqual(before.Freeze, after.Freeze) {
		return failure.New(failure.PolicyOrTransition, "manifest freeze immutable")
	}
	if isAtOrAfterReviewed(before) && !reflect.DeepEqual(before.Review, after.Review) {
		return failure.New(failure.PolicyOrTransition, "manifest review immutable")
	}
	return nil
}

func isAtOrAfterActive(m Manifest) bool {
	return m.State == lifecycle.Active || isAtOrAfterFrozen(m) || isAtOrAfterReviewed(m) ||
		m.State == lifecycle.Ready || m.State == lifecycle.Closed ||
		(m.State == lifecycle.Blocked && isAtOrAfterActiveState(m.BlockedFrom))
}

func isAtOrAfterActiveState(state lifecycle.State) bool {
	switch state {
	case lifecycle.Active, lifecycle.Frozen, lifecycle.Reviewed, lifecycle.Ready, lifecycle.Closed:
		return true
	default:
		return false
	}
}

func isAtOrAfterFrozen(m Manifest) bool {
	return m.State == lifecycle.Frozen || isAtOrAfterReviewed(m) || m.State == lifecycle.Ready || m.State == lifecycle.Closed ||
		(m.State == lifecycle.Blocked && isAtOrAfterFrozenState(m.BlockedFrom))
}

func isAtOrAfterFrozenState(state lifecycle.State) bool {
	switch state {
	case lifecycle.Frozen, lifecycle.Reviewed, lifecycle.Ready, lifecycle.Closed:
		return true
	default:
		return false
	}
}

func isAtOrAfterReviewed(m Manifest) bool {
	return m.State == lifecycle.Reviewed || m.State == lifecycle.Ready || m.State == lifecycle.Closed ||
		(m.State == lifecycle.Blocked && isAtOrAfterReviewedState(m.BlockedFrom))
}

func isAtOrAfterReviewedState(state lifecycle.State) bool {
	switch state {
	case lifecycle.Reviewed, lifecycle.Ready, lifecycle.Closed:
		return true
	default:
		return false
	}
}

func dispatchHash(d Dispatch) (string, error) {
	b, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	return canon.HashJSON(b)
}

func normalize(m Manifest) (Manifest, []byte, string, error) {
	m = cloneManifest(m)
	if err := normalizeDeclaredTimestamps(&m); err != nil {
		return Manifest{}, nil, "", err
	}
	dispatchHash, err := dispatchHash(m.Dispatch)
	if err != nil {
		return Manifest{}, nil, "", err
	}
	if m.DispatchHash == "" {
		m.DispatchHash = dispatchHash
	} else if m.DispatchHash != dispatchHash {
		return Manifest{}, nil, "", failure.New(failure.UsageOrSchema, "manifest dispatch hash mismatch")
	}
	b, err := json.Marshal(m)
	if err != nil {
		return Manifest{}, nil, "", err
	}
	canonical, err := canon.Bytes(b)
	if err != nil {
		return Manifest{}, nil, "", err
	}
	var normalized Manifest
	dec := json.NewDecoder(bytes.NewReader(canonical))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&normalized); err != nil {
		return Manifest{}, nil, "", err
	}
	hash, err := canon.HashJSON(canonical)
	if err != nil {
		return Manifest{}, nil, "", err
	}
	return normalized, canonical, hash, nil
}

func decode(data []byte) (Manifest, string, error) {
	canonical, err := canon.Bytes(data)
	if err != nil {
		return Manifest{}, "", failure.Wrap(failure.Integrity, "manifest canonicalize", err)
	}
	if !bytes.Equal(canonical, data) {
		return Manifest{}, "", failure.New(failure.Integrity, "manifest non-canonical bytes")
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(canonical))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, "", failure.Wrap(failure.Integrity, "manifest decode", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return Manifest{}, "", failure.New(failure.Integrity, "manifest trailing data")
	}
	normalized, normalizedBytes, hash, err := normalize(m)
	if err != nil {
		return Manifest{}, "", failure.Wrap(failure.Integrity, "manifest normalize", err)
	}
	if !bytes.Equal(normalizedBytes, data) {
		return Manifest{}, "", failure.New(failure.Integrity, "manifest noncanonical model bytes")
	}
	if err := validate(normalized); err != nil {
		return Manifest{}, "", failure.Wrap(failure.Integrity, "manifest validate", err)
	}
	return normalized, hash, nil
}

func validate(m Manifest) error {
	if m.SchemaVersion != 1 || m.LaneID == "" || m.Dispatch.Workspace == "" || m.Dispatch.SourceControl == "" ||
		m.Dispatch.Writer == "" || m.Dispatch.Adapter == "" || m.Dispatch.Family == "" || m.Dispatch.CostClass == "" ||
		m.Dispatch.ReviewPolicy == "" || m.Dispatch.AllowedPaths == nil || m.Dispatch.ForbiddenPaths == nil ||
		m.Dispatch.NonGoals == nil || m.Dispatch.RequiredChecks == nil || m.State == "" || m.CreatedAt == "" ||
		m.UpdatedAt == "" || !m.ExternalActionsHumanOnly {
		return failure.New(failure.UsageOrSchema, "manifest required fields")
	}
	if !isCanonicalTimestamp(m.CreatedAt) || !isCanonicalTimestamp(m.UpdatedAt) {
		return failure.New(failure.UsageOrSchema, "manifest timestamps")
	}
	if !isKnownState(m.State) || (m.State == lifecycle.Blocked && !isBlockedFromState(m.BlockedFrom)) ||
		(m.State != lifecycle.Blocked && m.BlockedFrom != "") {
		return failure.New(failure.UsageOrSchema, "manifest lifecycle state")
	}
	if m.Dispatch.MaxSeconds < 0 || m.Dispatch.MaxOutputBytes < 0 || m.Dispatch.MaxMemoryBytes < 0 {
		return failure.New(failure.UsageOrSchema, "manifest resource limits")
	}
	wantDispatchHash, err := dispatchHash(m.Dispatch)
	if err != nil || m.DispatchHash == "" || m.DispatchHash != wantDispatchHash {
		return failure.New(failure.UsageOrSchema, "manifest dispatch hash")
	}
	if m.Freeze != nil {
		if m.Freeze.Base == "" || m.Freeze.Head == "" || m.Freeze.Candidate == "" || m.Freeze.Tree == "" || m.Freeze.Diff == "" || m.Freeze.At == "" {
			return failure.New(failure.UsageOrSchema, "manifest freeze required")
		}
		if !isCanonicalTimestamp(m.Freeze.At) {
			return failure.New(failure.UsageOrSchema, "manifest freeze timestamp")
		}
	}
	if (m.State == lifecycle.Frozen || isAtOrAfterFrozen(m)) && m.Freeze == nil {
		return failure.New(failure.UsageOrSchema, "manifest freeze required")
	}
	if m.Review != nil {
		if m.Review.Reviewer == "" || m.Review.Family == "" || m.Review.Verdict == "" || m.Review.Fingerprint == "" || m.Review.At == "" || !validHashSet(m.Review.EvidenceHashes) {
			return failure.New(failure.UsageOrSchema, "manifest review required")
		}
		if (m.Review.Verdict != "PASS" && m.Review.Verdict != "FAIL") || !isCanonicalTimestamp(m.Review.At) ||
			m.Freeze == nil || m.Review.Fingerprint != m.Freeze.Candidate {
			return failure.New(failure.UsageOrSchema, "manifest review binding")
		}
	}
	if (m.State == lifecycle.Reviewed || isAtOrAfterReviewed(m)) && m.Review == nil {
		return failure.New(failure.UsageOrSchema, "manifest review required")
	}
	for _, evidence := range m.Evidence {
		if evidence.Name == "" || !isLowerHexHash(evidence.Hash) {
			return failure.New(failure.UsageOrSchema, "manifest evidence reference")
		}
	}
	for _, record := range m.Failures {
		if !isFailureClass(record.Class) || record.Op == "" || record.Error == "" || !isCanonicalTimestamp(record.At) {
			return failure.New(failure.UsageOrSchema, "manifest failure record")
		}
	}
	return nil
}

func isCanonicalTimestamp(value string) bool {
	parsed, err := time.Parse(time.RFC3339, value)
	return err == nil && parsed.UTC().Format(time.RFC3339) == value
}

func normalizeDeclaredTimestamps(m *Manifest) error {
	var err error
	if m.CreatedAt, err = normalizeTimestamp(m.CreatedAt); err != nil {
		return fmt.Errorf("created_at: %w", err)
	}
	if m.UpdatedAt, err = normalizeTimestamp(m.UpdatedAt); err != nil {
		return fmt.Errorf("updated_at: %w", err)
	}
	if m.Freeze != nil {
		if m.Freeze.At, err = normalizeTimestamp(m.Freeze.At); err != nil {
			return fmt.Errorf("freeze.at: %w", err)
		}
	}
	if m.Review != nil {
		if m.Review.At, err = normalizeTimestamp(m.Review.At); err != nil {
			return fmt.Errorf("review.at: %w", err)
		}
	}
	for index := range m.Failures {
		if m.Failures[index].At, err = normalizeTimestamp(m.Failures[index].At); err != nil {
			return fmt.Errorf("failures[%d].at: %w", index, err)
		}
	}
	return nil
}

func normalizeTimestamp(value string) (string, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", err
	}
	return parsed.UTC().Format(time.RFC3339), nil
}

func cloneManifest(m Manifest) Manifest {
	m.Dispatch.AllowedPaths = cloneStrings(m.Dispatch.AllowedPaths)
	m.Dispatch.ForbiddenPaths = cloneStrings(m.Dispatch.ForbiddenPaths)
	m.Dispatch.NonGoals = cloneStrings(m.Dispatch.NonGoals)
	m.Dispatch.RequiredChecks = cloneStrings(m.Dispatch.RequiredChecks)
	m.Evidence = cloneEvidence(m.Evidence)
	m.Failures = cloneFailures(m.Failures)
	if m.Freeze != nil {
		freeze := *m.Freeze
		m.Freeze = &freeze
	}
	if m.Review != nil {
		review := *m.Review
		review.EvidenceHashes = cloneStrings(review.EvidenceHashes)
		m.Review = &review
	}
	return m
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func cloneEvidence(values []EvidenceReference) []EvidenceReference {
	if values == nil {
		return nil
	}
	cloned := make([]EvidenceReference, len(values))
	copy(cloned, values)
	return cloned
}

func cloneFailures(values []FailureRecord) []FailureRecord {
	if values == nil {
		return nil
	}
	cloned := make([]FailureRecord, len(values))
	copy(cloned, values)
	return cloned
}

func isKnownState(state lifecycle.State) bool {
	switch state {
	case lifecycle.Planned, lifecycle.Active, lifecycle.Frozen, lifecycle.Reviewed, lifecycle.Ready,
		lifecycle.Closed, lifecycle.Blocked, lifecycle.Abandoned, lifecycle.Incident:
		return true
	default:
		return false
	}
}

func isBlockedFromState(state lifecycle.State) bool {
	switch state {
	case lifecycle.Planned, lifecycle.Active, lifecycle.Frozen, lifecycle.Reviewed, lifecycle.Ready:
		return true
	default:
		return false
	}
}

func isLowerHexHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

func validHashSet(values []string) bool {
	if len(values) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !isLowerHexHash(value) {
			return false
		}
		if _, ok := seen[value]; ok {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func isFailureClass(value string) bool {
	switch failure.Class(value) {
	case failure.UsageOrSchema, failure.PolicyOrTransition, failure.WorkspaceDrift, failure.ToolingOrAdapter,
		failure.CandidateOrReview, failure.Integrity, failure.Concurrency:
		return true
	default:
		return false
	}
}
