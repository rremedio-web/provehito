package main

import (
	"strings"

	"github.com/provehito-project/provehito/core/evidence"
	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/lifecycle"
	"github.com/provehito-project/provehito/core/manifest"
)

func runEvidence(operation string, args []string, stdout, stderr interface{ Write([]byte) (int, error) }) int {
	if operation == "verify" {
		return runEvidenceVerify(args, stdout, stderr)
	}
	fs := commandFlags("evidence add")
	state, jsonOutput := addStateFlags(fs)
	lane := fs.String("lane", "", "lane identifier")
	method := fs.String("method", "", "receipt method")
	probe := fs.String("probe", "", "bounded probe text")
	result := fs.String("result", "", "pass or fail")
	resultClass := fs.String("result-class", "", "typed result class")
	exitCode := fs.Int("exit-code", -1, "typed result exit code")
	if err := parseFlags(fs, args); err != nil {
		return writeResult(stdout, stderr, "evidence add", *jsonOutput, nil, err)
	}
	if *state == "" || *lane == "" || *method == "" {
		return writeResult(stdout, stderr, "evidence add", *jsonOutput, nil, usageError("evidence method and lane required"))
	}
	store, m, _, err := loadLane(*state, *lane)
	if err != nil {
		return writeResult(stdout, stderr, "evidence add", *jsonOutput, nil, err)
	}
	if m.State != lifecycle.Frozen {
		return writeResult(stdout, stderr, "evidence add", *jsonOutput, nil, failure.New(failure.PolicyOrTransition, "evidence add lifecycle"))
	}
	if m.Freeze == nil {
		return writeResult(stdout, stderr, "evidence add", *jsonOutput, nil, failure.New(failure.Integrity, "evidence add freeze"))
	}
	class, code, err := evidenceResult(*result, *resultClass, *exitCode)
	if err != nil {
		return writeResult(stdout, stderr, "evidence add", *jsonOutput, nil, err)
	}
	if *probe == "" {
		*probe = "manual evidence: " + *method
	}
	for _, ref := range m.Evidence {
		if ref.Name == *method {
			return writeResult(stdout, stderr, "evidence add", *jsonOutput, nil, failure.New(failure.PolicyOrTransition, "duplicate evidence"))
		}
	}
	receipt, err := evidence.NewStore(*state).Add(evidence.Receipt{SchemaVersion: 1, MethodID: *method, Probe: *probe, CandidateHash: m.Freeze.Candidate, ManifestHash: m.DispatchHash, ResultClass: class, ExitCode: code})
	if err != nil {
		return writeResult(stdout, stderr, "evidence add", *jsonOutput, nil, err)
	}
	for _, ref := range m.Evidence {
		if ref.Hash == receipt.Hash {
			return writeResult(stdout, stderr, "evidence add", *jsonOutput, nil, failure.New(failure.PolicyOrTransition, "duplicate evidence"))
		}
	}
	reference := manifestEvidence(*method, receipt.Hash)
	_, newHash, err := store.Mutate(manifest.ExpectedHash{}, func(next *manifest.Manifest) {
		next.Evidence = append(next.Evidence, reference)
	})
	if err != nil {
		return writeResult(stdout, stderr, "evidence add", *jsonOutput, nil, err)
	}
	return writeResult(stdout, stderr, "evidence add", *jsonOutput, map[string]any{"ref": receipt.Hash, "hash": newHash, "evidence_hash": receipt.Hash}, nil)
}

func manifestEvidence(name, hash string) manifest.EvidenceReference {
	return manifest.EvidenceReference{Name: name, Hash: hash}
}

func evidenceResult(result, class string, code int) (string, int, error) {
	if class == "" {
		switch strings.ToLower(result) {
		case "pass", "success", "ok":
			class, code = evidence.ResultSuccess, 0
		case "fail", "failed":
			class, code = evidence.ResultCandidateOrReview, 50
		default:
			return "", 0, usageError("evidence result required")
		}
	}
	if code < 0 {
		switch class {
		case evidence.ResultSuccess:
			code = 0
		case evidence.ResultCandidateOrReview, evidence.ResultToolingOrAdapter:
			code, _ = failure.CodeFor(failure.Class(class))
		default:
			return "", 0, usageError("evidence exit code required")
		}
	}
	if class != evidence.ResultSuccess && code == 0 {
		return "", 0, usageError("evidence result exit code")
	}
	return class, code, nil
}

func runEvidenceVerify(args []string, stdout, stderr interface{ Write([]byte) (int, error) }) int {
	fs := commandFlags("evidence verify")
	state, jsonOutput := addStateFlags(fs)
	ref := fs.String("ref", "", "hash-only receipt reference")
	if err := parseFlags(fs, args); err != nil {
		return writeResult(stdout, stderr, "evidence verify", *jsonOutput, nil, err)
	}
	if *state == "" || *ref == "" {
		return writeResult(stdout, stderr, "evidence verify", *jsonOutput, nil, usageError("evidence reference required"))
	}
	if err := evidence.NewStore(*state).Verify(evidence.Reference{Hash: *ref}); err != nil {
		return writeResult(stdout, stderr, "evidence verify", *jsonOutput, nil, err)
	}
	return writeResult(stdout, stderr, "evidence verify", *jsonOutput, map[string]any{"ref": *ref}, nil)
}
