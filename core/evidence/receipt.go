// Package evidence stores immutable, content-addressed execution receipts.
package evidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/provehito-project/provehito/core/canon"
	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/fingerprint"
)

const (
	ResultSuccess = "SUCCESS"

	maxMethodID = 128
	maxProbeID  = 256
	maxArtifact = 32
	maxName     = 256
)

// Result classes are the successful outcome and the stable typed failures
// emitted by the core. A non-success class must carry its corresponding exit
// code; SUCCESS is the only class that may carry exit code zero.
const (
	ResultUsageOrSchema      = string(failure.UsageOrSchema)
	ResultPolicyOrTransition = string(failure.PolicyOrTransition)
	ResultWorkspaceDrift     = string(failure.WorkspaceDrift)
	ResultToolingOrAdapter   = string(failure.ToolingOrAdapter)
	ResultCandidateOrReview  = string(failure.CandidateOrReview)
	ResultIntegrity          = string(failure.Integrity)
	ResultConcurrency        = string(failure.Concurrency)
)

// Reference identifies either a stored receipt (Hash and Path) or a bounded
// artifact named by a receipt (Name and Hash). Path is deliberately not part
// of an on-disk receipt: paths are store-local capabilities, not evidence.
type Reference struct {
	Name string `json:"name,omitempty"`
	Hash string `json:"hash"`
	Path string `json:"-"`
}

// String returns the stable content address, suitable for manifests and CLI
// output without exposing the store's local path.
func (r Reference) String() string { return r.Hash }

// Receipt is the v1 evidence record. Timestamp and CanonicalHash are owned by
// Store.Add; callers may leave both empty. CandidateHash is the frozen
// fingerprint equivalent hash and ManifestHash binds the exact manifest.
type Receipt struct {
	// Candidate is an optional caller convenience. When supplied, its
	// EquivalentHash and ManifestHash must agree with the explicit hash fields.
	// It is not serialized because the compact receipt binds those hashes.
	Candidate     fingerprint.Fingerprint `json:"-"`
	SchemaVersion int                     `json:"schema_version"`
	MethodID      string                  `json:"method_id"`
	SeatID        string                  `json:"seat_id,omitempty"`
	Probe         string                  `json:"probe"`
	CandidateHash string                  `json:"candidate_hash"`
	ManifestHash  string                  `json:"manifest_hash"`
	ResultClass   string                  `json:"result_class"`
	ExitCode      int                     `json:"exit_code"`
	Artifacts     []Reference             `json:"artifacts,omitempty"`
	Timestamp     string                  `json:"timestamp"`
	CanonicalHash string                  `json:"canonical_hash"`
}

type receiptPayload struct {
	SchemaVersion int         `json:"schema_version"`
	MethodID      string      `json:"method_id"`
	SeatID        string      `json:"seat_id,omitempty"`
	Probe         string      `json:"probe"`
	CandidateHash string      `json:"candidate_hash"`
	ManifestHash  string      `json:"manifest_hash"`
	ResultClass   string      `json:"result_class"`
	ExitCode      int         `json:"exit_code"`
	Artifacts     []Reference `json:"artifacts,omitempty"`
	Timestamp     string      `json:"timestamp"`
}

func (r Receipt) payload() receiptPayload {
	return receiptPayload{
		SchemaVersion: r.SchemaVersion,
		MethodID:      r.MethodID,
		SeatID:        r.SeatID,
		Probe:         r.Probe,
		CandidateHash: r.CandidateHash,
		ManifestHash:  r.ManifestHash,
		ResultClass:   r.ResultClass,
		ExitCode:      r.ExitCode,
		Artifacts:     cloneReferences(r.Artifacts),
		Timestamp:     r.Timestamp,
	}
}

func cloneReferences(refs []Reference) []Reference {
	if refs == nil {
		return nil
	}
	result := make([]Reference, len(refs))
	copy(result, refs)
	for i := range result {
		result[i].Path = ""
	}
	return result
}

func (r Receipt) validateInput() error {
	r = normalizeIdentity(r)
	if r.Candidate.EquivalentHash != "" || r.Candidate.ManifestHash != "" {
		if r.CandidateHash == "" {
			r.CandidateHash = r.Candidate.EquivalentHash
		}
		if r.ManifestHash == "" {
			r.ManifestHash = r.Candidate.ManifestHash
		}
		if r.CandidateHash != r.Candidate.EquivalentHash || r.ManifestHash != r.Candidate.ManifestHash {
			return fmt.Errorf("receipt candidate identity mismatch")
		}
	}
	if r.SchemaVersion != 1 || r.MethodID == "" || len(r.MethodID) > maxMethodID || hasControl(r.MethodID) ||
		r.Probe == "" || len(r.Probe) > maxProbeID || hasControl(r.Probe) || !isHash(r.CandidateHash) || !isHash(r.ManifestHash) {
		return fmt.Errorf("receipt required fields")
	}
	if len(r.SeatID) > maxMethodID || hasControl(r.SeatID) {
		return fmt.Errorf("receipt seat id")
	}
	if len(r.Artifacts) > maxArtifact || len(r.Artifacts) == 0 && r.Artifacts != nil {
		return fmt.Errorf("receipt artifact presence")
	}
	for _, artifact := range r.Artifacts {
		if artifact.Path != "" || artifact.Name == "" || len(artifact.Name) > maxName ||
			hasControl(artifact.Name) || !isHash(artifact.Hash) {
			return fmt.Errorf("receipt artifact reference")
		}
	}
	expected, ok := resultExitCode(r.ResultClass)
	if !ok || r.ExitCode != expected {
		return fmt.Errorf("receipt result class and exit code")
	}
	return nil
}

func hasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func normalizeIdentity(r Receipt) Receipt {
	if r.CandidateHash == "" {
		r.CandidateHash = r.Candidate.EquivalentHash
	}
	if r.ManifestHash == "" {
		r.ManifestHash = r.Candidate.ManifestHash
	}
	return r
}

func resultExitCode(class string) (int, bool) {
	switch class {
	case ResultSuccess:
		return 0, true
	case ResultUsageOrSchema:
		return 10, true
	case ResultPolicyOrTransition:
		return 20, true
	case ResultWorkspaceDrift:
		return 30, true
	case ResultToolingOrAdapter:
		return 40, true
	case ResultCandidateOrReview:
		return 50, true
	case ResultIntegrity:
		return 60, true
	case ResultConcurrency:
		return 70, true
	default:
		return 0, false
	}
}

func isHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, c := range value {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func timestampValid(value string) bool {
	parsed, err := time.Parse(time.RFC3339, value)
	return err == nil && parsed.Location() == time.UTC && parsed.Format(time.RFC3339) == value
}

func payloadBytes(r Receipt) ([]byte, error) {
	if err := r.validateInput(); err != nil {
		return nil, err
	}
	if !timestampValid(r.Timestamp) {
		return nil, fmt.Errorf("receipt timestamp")
	}
	r.Artifacts = cloneReferences(r.Artifacts)
	data, err := json.Marshal(r.payload())
	if err != nil {
		return nil, err
	}
	return canon.Bytes(data)
}

func canonicalHash(r Receipt) (string, error) {
	data, err := payloadBytes(r)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func receiptBytes(r Receipt) ([]byte, error) {
	if err := r.validateInput(); err != nil {
		return nil, err
	}
	if !timestampValid(r.Timestamp) || !isHash(r.CanonicalHash) {
		return nil, fmt.Errorf("receipt identity")
	}
	want, err := canonicalHash(r)
	if err != nil {
		return nil, err
	}
	if want != r.CanonicalHash {
		return nil, fmt.Errorf("receipt canonical hash")
	}
	data, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return canon.Bytes(data)
}

func decodeReceipt(data []byte) (Receipt, error) {
	canonical, err := canon.Bytes(data)
	if err != nil {
		return Receipt{}, failure.Wrap(failure.Integrity, "evidence canonicalize", err)
	}
	if !bytes.Equal(canonical, data) {
		return Receipt{}, failure.New(failure.Integrity, "evidence non-canonical bytes")
	}
	var receipt Receipt
	dec := json.NewDecoder(bytes.NewReader(canonical))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&receipt); err != nil {
		return Receipt{}, failure.Wrap(failure.Integrity, "evidence decode", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return Receipt{}, failure.New(failure.Integrity, "evidence trailing data")
	}
	if _, err := receiptBytes(receipt); err != nil {
		return Receipt{}, failure.Wrap(failure.Integrity, "evidence validate", err)
	}
	return receipt, nil
}
