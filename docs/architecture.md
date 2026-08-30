# Architecture

Provehito Phase 1 is a local command-line workflow engine and reusable Go
core. It coordinates work in a trusted local environment; it does not turn
agent messages into authority and it does not perform external actions.

## Components

The CLI in `cmd/provehito/` parses commands, loads state, and renders stable
results. `cmd/provehito/run.go` is the command dispatcher; command handlers
translate flags into requests for the core packages rather than implementing
workflow rules themselves.

The core is divided by responsibility:

- `core/manifest/` owns the canonical JSON lane manifest, hashes, immutable
  dispatch and freeze fields, and crash-safe persistence.
- `core/lifecycle/` owns the explicit lane state machine. Legal transitions
  are applied by `core/lifecycle/transition.go`; prose is not an event.
- `core/workspace/` canonicalizes paths, keeps the state root separate from
  assigned workspaces, and provides one-writer leases and locks.
- `core/adapter/` validates launch profiles and selects the cheapest eligible
  capability with a deterministic ID tie-break.
- `core/process/` supervises one configured local process in the foreground,
  bounds captured output, and reports exit, timeout, and cancellation results.
- `core/fingerprint/` obtains Git candidate identity; `core/evidence/` stores
  content-addressed receipts; and `core/review/` binds review to a frozen
  fingerprint.
- `core/policy/` evaluates readiness requirements and reviewer-family policy;
  `core/failure/` preserves typed failure classes and exit codes.

These boundaries are exercised by package tests such as
`core/manifest/manifest_test.go`, `core/workspace/workspace_test.go`,
`core/process/supervisor_test.go`, `core/fingerprint/git_test.go`,
`core/evidence/evidence_test.go`, and `core/review/review_test.go`.

## State and data flow

An operator chooses an explicit state root outside every assigned workspace.
`lane open` creates an `ACTIVE` manifest after validating dispatch. A writer
lease is acquired before a process runs. A clean Git workspace is frozen into
base, head, tree, diff, candidate, and manifest hashes. Receipts are added by
hash, reviews name the exact frozen fingerprint, and `ready` evaluates the
declared checks. `READY` is a local workflow result, not permission to push,
merge, deploy, publish, or mutate a remote system.

Manifest writes are canonical JSON and compare-and-swap updates. The store in
`core/manifest/store.go` uses a `0600` temporary file, sync, atomic rename,
and parent-directory sync; it refuses stale hashes, malformed bytes, and
temporary-file residue rather than silently repairing state.

The normal lifecycle is:

```text
PLANNED -> ACTIVE -> FROZEN -> REVIEWED -> READY -> CLOSED
```

`BLOCKED` records the state from which it may resume. `ABANDONED` and
`INCIDENT` are terminal. `core/lifecycle/transition_test.go` covers legal and
illegal state/event pairs and verifies that approval-like prose cannot become
an event.

## Authority boundary

The core implements no sockets and no outward actions. The source-level guard
in `internal/sourceguard/sourceguard.go`, tested by
`internal/sourceguard/sourceguard_test.go`, rejects engine implementations of
socket calls, credential APIs, shell interpretation, Git network mutations,
and publishing commands. Configured subprocesses are launched by executable
path plus literal argument arrays; they are not sandboxed by Provehito.

Worker output is untrusted text. A spawned worker's rendered results, and any
start or stop request it emits, enter a queue and are read by the coordinator;
they are not events. A lifecycle transition occurs only when the coordinator
records an explicit lifecycle event, so worker prose cannot move a lane's
state, no matter how authoritative it reads.

Phase 1 trusts the local host, the operator account, and processes that can
rewrite the state root. It detects ordinary corruption, unexpected symlinks,
path escapes, and concurrent Provehito commands. It does not resist a
malicious same-user process racing filesystem operations or replacing inodes.
The state model records allowed paths, forbidden paths, and
`max_memory_bytes`; these are workflow declarations, not OS-enforced memory or
filesystem sandbox limits. Time and output bounds, plus process-group
cancellation, are implemented for macOS and Linux in `core/process/`.

Phase 1 has no daemon, browser UI, network service, credential store, remote
transport, or automatic cleanup. Those would require a separate design and
threat model.
