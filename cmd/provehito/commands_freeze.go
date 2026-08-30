package main

import (
	"context"

	"github.com/provehito-project/provehito/core/failure"
	"github.com/provehito-project/provehito/core/fingerprint"
	"github.com/provehito-project/provehito/core/lifecycle"
	"github.com/provehito-project/provehito/core/manifest"
)

func runFreeze(args []string, stdout, stderr interface{ Write([]byte) (int, error) }) int {
	fs := commandFlags("freeze")
	state, jsonOutput := addStateFlags(fs)
	lane := fs.String("lane", "", "lane identifier")
	expected := fs.String("expected-hash", "", "expected manifest hash")
	base := fs.String("base", "HEAD~1", "Git base revision")
	if err := parseFlags(fs, args); err != nil {
		return writeResult(stdout, stderr, "freeze", *jsonOutput, nil, err)
	}
	if *state == "" || *lane == "" {
		return writeResult(stdout, stderr, "freeze", *jsonOutput, nil, usageError("freeze lane required"))
	}
	store, m, hash, err := loadLane(*state, *lane)
	if err != nil {
		return writeResult(stdout, stderr, "freeze", *jsonOutput, nil, err)
	}
	if m.State != lifecycle.Active {
		return writeResult(stdout, stderr, "freeze", *jsonOutput, nil, failure.New(failure.PolicyOrTransition, "freeze lifecycle"))
	}
	fp, err := fingerprint.NewGitProvider().Freeze(context.Background(), m.Dispatch.Workspace, *base, m.DispatchHash)
	if err != nil {
		return writeResult(stdout, stderr, "freeze", *jsonOutput, nil, err)
	}
	record := manifest.FreezeRecord{Base: fp.BaseCommit, Head: fp.HeadCommit, Candidate: fp.EquivalentHash, Tree: fp.HeadTree, Diff: fp.DiffHash, At: fp.FrozenAt.UTC().Format("2006-01-02T15:04:05Z")}
	m, newHash, err := store.Apply(manifest.OptionalHash(*expected, "freeze"), lifecycle.Freeze, func(next *manifest.Manifest) {
		next.Freeze = &record
	})
	if err != nil {
		return writeResult(stdout, stderr, "freeze", *jsonOutput, nil, err)
	}
	return writeResult(stdout, stderr, "freeze", *jsonOutput, map[string]any{
		"candidate_hash": fp.EquivalentHash, "head": fp.HeadCommit, "tree": fp.HeadTree, "diff": fp.DiffHash,
		"dispatch_hash": m.DispatchHash, "previous_hash": hash, "hash": newHash,
	}, nil)
}

func hashList(m manifest.Manifest) []string {
	values := make([]string, 0, len(m.Evidence))
	for _, ref := range m.Evidence {
		values = append(values, ref.Hash)
	}
	return values
}
