# Threat model

This document describes the Phase 1 local boundary. It is a coordination and
integrity boundary, not a hostile-host sandbox.

## Assets

The principal assets are the explicit state root, canonical lane manifests,
dispatch constraints, writer leases, frozen Git fingerprints, evidence
receipts, review records, and the identity of the assigned workspace. Their
integrity determines whether a lane may reach `READY`.

`core/manifest/model.go`, `core/evidence/receipt.go`, and
`core/review/review.go` define the fields and bindings; their tests cover
hashes, immutability, receipt verification, and review invalidation.

## Actors and trust

The trusted actor is the human operator controlling the local host and account.
The host, the operator account, and other processes that can rewrite the state
root are trusted for Phase 1. Agent output, peer messages, attachments,
imported manifests, tool output, and external API data are untrusted input.

The boundary explicitly does not resist a malicious same-user process that
races filesystem operations, replaces an inode behind an open path, or otherwise
has equivalent access to the state root. Stronger isolation requires an
operator-supplied OS sandbox.

## Guarantees and mitigations

- `core/workspace/paths.go` rejects state/workspace overlap, traversal, and
  symlink aliases; `core/workspace/workspace_test.go` exercises these cases.
- `core/workspace/lease.go` and `core/workspace/lease_unix.go` coordinate one
  writer with an OS lock and durable lease. A second writer or an abandoned
  lease is a concurrency failure, not a silent takeover.
- `core/manifest/store.go` uses canonical bytes, expected prior hashes, atomic
  writes, and filesystem sync. Corruption and stale updates fail closed.
- `core/canon/json.go` rejects duplicate keys and normalizes object ordering;
  `core/canon/json_test.go` covers deterministic hashing.
- `core/process/supervisor.go` passes only an environment allowlist and literal
  arguments, bounds captured output, and reports timeout/cancellation. On
  macOS/Linux, process-group cancellation is implemented in
  `core/process/supervisor_unix.go`. `agent run` receipts store stdout/stderr
  SHA-256 hashes and truncation metadata, not subprocess plaintext.
- `core/lifecycle/transition.go` accepts exact events only. Agent text such as
  “approved” cannot change state; this is tested in
  `core/lifecycle/transition_test.go`.
- `internal/sourceguard/sourceguard.go` and its tests detect prohibited core
  operations, including sockets, credential APIs, shell interpretation, Git
  network mutations, and publishing commands.
- `internal/releasecheck` validates release ZIP archives without extraction:
  structural path rules, size and compression limits, forbidden segments, and
  full-byte content scans. `scripts/release.sh` builds deterministic archives
  from `git archive` and writes a receipt. See `docs/releasing.md`.

## Residual risks and non-guarantees

Configured subprocesses are not sandboxed. They may use host permissions,
access the network, or mutate paths outside the intended workspace. Provehito
itself implements no sockets or outward actions, but this does not constrain a
configured executable.

Allowed paths and forbidden paths are recorded in dispatch state, not enforced
as an OS filesystem sandbox. `max_memory_bytes` is recorded and validated as a
non-negative declaration, but is not an OS-enforced memory limit. Time and
output limits are implemented; process-group cancellation is implemented on
macOS and Linux.

Phase 1 does not provide remote or multi-user isolation, credential storage,
network transport, a background service, automatic cleanup, or external-action
execution. These are separate future scopes and must receive their own threat
models.
