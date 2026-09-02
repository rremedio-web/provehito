# Provehito Phase 1 Design

*In altum — launch forth into the deep.*

**Status:** Pre-release local CLI. CI validates macOS and Linux. Security
automation includes gitleaks, govulncheck, and SBOM generation.
**Date:** 2026-08-19
**Scope:** Local CLI and reusable core library

## Goal

Provehito is a provider-neutral workflow engine for coordinating local coding
agents without turning agent messages into authority. Phase 1 launches and
supervises local agent CLI processes, binds one writer to each isolated
workspace, freezes exact candidate identities before review, and records
content-addressed evidence.

The system helps a trusted human operator coordinate work. It does not perform
or authorize external actions on the operator's behalf.

## Design principles

1. **State before chat.** Versioned manifests are authoritative; transcripts
   and agent claims are untrusted evidence.
2. **One writer per workspace.** Concurrent writers are rejected, not merely
   discouraged.
3. **Review frozen bytes.** A review applies only to the exact candidate
   fingerprint recorded when the lane freezes.
4. **Human gates stay outside the engine.** Phase 1 does not implement or
   authorize push, merge, deploy, publish, credential, or remote mutation.
5. **Fail closed and name the failure class.** Tool failures, integrity
   failures, policy failures, and candidate failures remain distinct.
6. **Cheapest eligible worker first.** Model and CLI names remain adapter data;
   routing uses capabilities, family, and cost class.
7. **Small trusted core.** No daemon, browser UI, network service, runtime
   plugin loading, or arbitrary shell evaluation in Phase 1.

## Non-goals

Phase 1 does not provide:

- a background service or detached job scheduler;
- a browser dashboard, mobile client, or remote control;
- hosted accounts, telemetry, or cloud storage;
- automatic workspace deletion or cleanup;
- automatic commits, pushes, merges, pull requests, releases, or deployments;
- credential storage or secret injection;
- a general-purpose chat system;
- organization-specific roles, repositories, environments, or policies.

## Technology

Phase 1 is implemented in Go.

Go provides a single cross-platform binary, straightforward process and signal
handling, a small deployment footprint, and a stable path from a local CLI to a
later service. The standard library is preferred. Third-party dependencies are
accepted only for a concrete requirement and must be pinned and audited.

The initial support boundary is macOS and Linux. Windows support waits until its
locking, process-tree cancellation, path containment, and atomic-write behavior
has equivalent fixtures; successful cross-compilation alone does not qualify it
as supported.

JSON is the only authoritative manifest format in Phase 1. Canonical JSON keeps
hashing and cross-platform fixtures deterministic. Human-friendly YAML import
may be considered later, but imported data must normalize to the canonical JSON
schema before it can control workflow state.

## Architecture

The repository contains one Go module with a thin CLI and reusable public core
packages:

```text
provehito/
  cmd/provehito/            CLI argument and output adapter
  core/manifest/            schema, canonicalization, immutable fields
  core/lifecycle/           state machine and transition validation
  core/workspace/           identity, lease, lock, and path containment
  core/adapter/             agent launch contract and profile registry
  core/process/             foreground supervision and output capture
  core/fingerprint/         candidate and diff identity
  core/evidence/            content-addressed receipts and verification
  core/review/              frozen-candidate review binding
  core/policy/              capability and human-gate decisions
  core/failure/             typed error taxonomy and stable exit classes
  schema/v1/                published JSON schemas
  docs/                     user, security, and contributor documentation
  examples/toy-project/     neutral, synthetic walkthrough
  testdata/                 adversarial and golden fixtures
```

Core packages never parse terminal arguments or print output. The CLI translates
arguments into core requests and renders human-readable or machine-readable
results. Phase 3 must consume these packages rather than reimplementing their
rules.

### Smallest vertical slice

The first executable increment supports one lane and one foreground fake agent:
initialize a state root, attach a clean workspace, open a lane, acquire one
writer lease, run the fake process, freeze the clean candidate, add and verify
one receipt, record one review, mark the lane ready, and close it. Real agent
profiles, cost routing, independent-family policy, and parallel lane operation
are added only after this complete path is deterministic.

## Lane lifecycle

The normal lifecycle is:

```text
PLANNED -> ACTIVE -> FROZEN -> REVIEWED -> READY -> CLOSED
```

Additional states are `BLOCKED`, `ABANDONED`, and `INCIDENT`.

- `BLOCKED` records the previous state; the human operator owns the recovery
  decision.
- `ABANDONED` and `INCIDENT` are terminal in Phase 1.
- `READY` means the declared local checks and review requirements are satisfied.
  It is not permission to perform an external action.
- Every transition is explicit. An undeclared transition is rejected.

Dispatch fields lock when a lane enters `ACTIVE`. Freeze fields lock when it
enters `FROZEN`. Review records never alter either set.

## Manifest model

Each lane has one canonical manifest containing:

- schema version and lane identifier;
- current lifecycle state and prior state when blocked;
- workspace identity and source-control identity;
- writer identity, adapter profile, family, and cost class;
- allowed paths, forbidden paths, and non-goals;
- required checks and review policy;
- resource and time limits;
- immutable dispatch fields and their canonical hash;
- frozen candidate, tree, and diff fingerprints;
- evidence receipt references and hashes;
- reviewer identity, family, seat ID, verdict, and reviewed fingerprint;
- timestamps and typed failure records;
- a declaration that external actions are human-only.

The state directory is explicit at initialization and may not live inside an
assigned workspace. Product workspaces therefore remain free of Provehito
runtime state. Relative paths are resolved against the declared state root;
escaping paths, aliases, and symlink traversal fail closed.

## CLI surface

Phase 1 exposes:

```text
provehito init
provehito doctor
provehito lane open|list|validate|status|block|resume|abandon|incident
provehito agent run
provehito freeze
provehito evidence add|verify
provehito review open|record
provehito ready
provehito close
provehito version
provehito help
```

Every command supports deterministic JSON output. Human output begins with the
result, then the evidence or correction. Stable exit classes distinguish usage,
policy, workspace drift, adapter/tooling, candidate, integrity, and concurrency
failures.

`agent run` is a foreground, one-lane supervisor. It launches one configured
process, captures bounded output, enforces timeout and cancellation, records
exit status, and terminates the process group on interruption. Independent CLI
invocations coordinate through an atomic lock in the shared state root plus a
durable lease record. The operating-system lock is held for the full run. A
second writer fails immediately. If the supervisor dies, the OS lock releases;
the next `agent run` records the abandoned durable lease as `BLOCKED` before
any new writer may run. Detached agents, restart recovery, and background
scheduling wait for Phase 3.

## Agent adapter contract

Phase 1 ships one generic local-process adapter. A launch profile declares:

- an executable path and argument array, never a shell command string;
- the assigned working directory;
- an environment-variable allowlist;
- writer or reviewer capability;
- family and cost class;
- read-only enforcement arguments where the target CLI supports them;
- expected version probe, exit behavior, timeout, and output bounds.

The adapter returns structured launch and exit results. It cannot mutate the
lane lifecycle directly. Agent text is stored as untrusted output and cannot
be interpreted as approval, a verdict, or a transition command.

Human-authored launch profiles are trusted configuration. Agent subprocesses are
outside the engine's security boundary: Phase 1 constrains working directory,
arguments, and environment, but cannot prove that an arbitrary third-party
executable will not use host permissions, mutate other paths, or access the
network. Phase 1 itself opens no sockets. Strong subprocess containment requires
an operator-supplied OS sandbox and is not claimed by this design.

## Routing policy

Profiles declare `cost_class`, `capabilities`, and `family`. The default router
selects the cheapest available profile satisfying the requested capability and
policy. Premium profiles are reserved for explicitly declared high-risk work or
final acceptance.

When independent review is required, the reviewer family and seat ID must differ
from the writer family and seat ID. Unknown identity data fails the requirement
rather than acting as an exemption. No provider or model name is hard-coded into
the core.

## Fingerprints, evidence, and review

The built-in fingerprint provider targets Git workspaces in Phase 1. Freeze
requires `git status --porcelain=v1 --untracked-files=all` to be empty and
rejects submodules. Untracked files therefore block freeze; ignored files are
outside candidate identity and may not satisfy checks or evidence requirements.
Uncommitted-candidate support is deferred.

Freeze records the base commit, head commit, tree, canonical binary diff hash,
manifest hash, and timestamp. The diff hash is computed from a full-index binary
diff between the recorded commits under a fixed locale. Git object identity,
including executable and symlink modes, controls the result; checkout line-ending
configuration does not replace object identity.

Evidence is immutable and content-addressed with SHA-256. A receipt contains the
method, bounded probe identity, exit class, optional artifact hashes, observed
fingerprint, timestamp, and content hash. Prose summaries alone cannot satisfy
a required check.

A review opens only against a frozen fingerprint. Recording the review requires
the reviewer identity, family, verdict, and exact fingerprint. If the candidate
or required evidence changes, the review becomes invalid and cannot support
`READY`.

## Authority and security boundary

The trusted human controls configuration and invokes lifecycle commands. Agent
output, peer messages, attachments, imported manifests, tool output, and external
API data are untrusted.

The local host, the operator's user account, and other processes with permission
to rewrite the state root are trusted. Provehito detects ordinary corruption,
unexpected symlinks, and concurrent Provehito commands; it does not claim to
resist a malicious same-user process racing filesystem operations or rewriting
inodes behind an open path. That stronger boundary requires an OS sandbox and is
outside Phase 1.

Phase 1 guarantees that its own core does not implement or authorize external
actions. It can produce a human-readable action packet, but it cannot enact it.
The word "approved" in any agent-controlled input has no authority effect. This
guarantee does not sandbox a configured subprocess; the operator remains
responsible for that executable and its permissions.

Additional requirements:

- redact configured secret-shaped environment variables from logs;
- pass no ambient environment variables except the explicit allowlist;
- avoid shell interpolation;
- enforce output and time limits while recording, but not claiming OS
  enforcement for, declared filesystem, memory, and process limits;
- use atomic writes and fsync for manifest transitions;
- recover or fail clearly after interrupted writes;
- never silently repair corrupted state;
- store no usage telemetry by default.

## Failure model

Failures are typed and preserved:

- `USAGE_OR_SCHEMA` — invalid command or manifest;
- `POLICY_OR_TRANSITION` — disallowed capability or lifecycle move;
- `WORKSPACE_DRIFT` — identity differs from the declared candidate;
- `TOOLING_OR_ADAPTER` — executable, timeout, parsing, or launch failure;
- `CANDIDATE_OR_REVIEW` — a required product check or review failed;
- `INTEGRITY` — hash, path, receipt, or state corruption;
- `CONCURRENCY` — writer lease or lock conflict.

Tooling failure never becomes a positive or negative candidate verdict.

## Testing strategy

The initial suite includes:

1. table-driven tests for every legal and illegal lifecycle transition;
2. immutable-field mutation tests before and after freeze;
3. concurrent and alias-path attempts to obtain a second writer lease;
4. drift tests proving that changed candidate bytes invalidate review;
5. adversarial messages claiming approval, completion, or authority;
6. canonical JSON and SHA-256 golden fixtures across supported platforms;
7. symlink, traversal, corrupted-state, and interrupted-write fixtures;
8. fake-adapter tests for success, timeout, malformed output, cancellation, and
   nonzero exit;
9. tests proving failure-class separation and no false-green conversion;
10. dependency and source checks proving that Phase 1 itself opens no sockets
    and contains no external-action implementation;
11. macOS and Linux fixtures for locks, cancellation, symlinks, atomic writes,
    and interrupted-state recovery.

The neutral example uses a tiny local repository and fake agent executables. It
requires no model account, API key, network connection, or proprietary CLI.

## Documentation set

The public repository includes:

- `README.md` — purpose, non-goals, installation, and ten-minute quickstart;
- `docs/architecture.md` — module and data-flow boundaries;
- `docs/lifecycle.md` — state diagram and transition rules;
- `docs/manifest.md` — schema and canonicalization;
- `docs/adapters.md` — launch-profile contract;
- `docs/evidence.md` — receipts, hashes, and review binding;
- `docs/threat-model.md` — assets, actors, guarantees, and residual risks;
- `SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `GOVERNANCE.md`,
  `CHANGELOG.md`, and an explicit license;
- a complete neutral walkthrough using only synthetic fixtures.

Documentation and fixtures contain no private names, domains, paths, branch
names, credentials, transcripts, or organization-specific policies.

## Phase 3 boundary

Phase 3 may add a long-running service, browser dashboard, authenticated local
or private-network transport, live streaming, background queues, notifications,
and crash-resumable agent sessions. It must depend on the Phase 1 core and use
the same manifests, state machine, evidence model, and authority rules.

Remote agent hosts, multi-tenant storage, credential vaults, and enactment of
external actions require separate threat models and are not implied by a Phase 3
dashboard.

## Possible companion repositories

These remain separate from Phase 1:

1. a local agent communications room built on the stable core;
2. a standalone evidence and receipt specification/CLI;
3. a deterministic A/B capture and replay runner;
4. a separately versioned, opt-in catalog of launch-profile examples, each with
   provenance, licensing, and security review independent of the core;
5. a dual-control collaboration protocol and benchmark in which two peers
   collaborate on design, one later receives the exclusive writer lease, and
   the other remains read-only for challenge and monitoring.

## Acceptance criteria

The Phase 1 implementation is ready for release planning when:

- the CLI completes the neutral example from lane open through close;
- one-writer, immutable-freeze, review-binding, and human-gate invariants have
  adversarial tests;
- all authoritative state uses canonical JSON; hashes exclude presentation
  formatting, timestamps use UTC RFC 3339, and tests inject a fixed clock;
- the engine itself neither implements nor authorizes an external action;
- the fake-adapter suite passes without network or credentials;
- macOS and Linux pass `go test ./...`, `go test -race ./...`, `go vet ./...`,
  and the same golden fixtures;
- public documentation and repository history pass a private-identifier and
  provenance audit.
