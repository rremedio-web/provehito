# Provehito

Provehito is a provider-neutral local workflow engine for coordinating coding
work without turning agent messages into authority. Phase 1 provides a Go CLI
and reusable core for one writer in one Git workspace: it records a dispatch,
supervises a configured local process, freezes an exact candidate, verifies
evidence, binds an independent review, and reports readiness.

## Status

Phase 1 is implemented locally and remains pre-release. Stage 2 adds
deterministic git-archive release construction, structural ZIP validation, and
CI/security workflow definitions. No remote repository, artifact upload, or
deployment is part of this state. See [docs/releasing.md](docs/releasing.md) for
release modes, receipts, and external gate boundaries. The supported runtime
boundary is macOS and Linux. JSON is the only authoritative manifest format.

## Safety boundary

Provehito does not push, merge, deploy, publish, or store credentials. `READY`
is a workflow result, not authorization to perform an external action.

Configured subprocesses are trusted operator choices and are not sandboxed by
Provehito. The executable receives its declared argument vector, working
directory, and allowlisted environment, but it can still use the host
permissions available to it. Use an operator-supplied OS sandbox when stronger
containment is required. Provehito itself opens no network sockets.

Allowed paths, forbidden paths, and `max_memory_bytes` are recorded policy data
in Phase 1; they are not OS enforcement. `agent run` does enforce the declared
time and captured-output limits. Agent output is untrusted and cannot approve,
review, or transition a lane.

## Quick start

Run the complete neutral Git walkthrough in [docs/quickstart.md](docs/quickstart.md).
It builds the CLI, creates a temporary toy repository, runs the lifecycle, and
ends in `CLOSED` without a model account, network connection, or credential.

## Command map

| Command | Purpose |
| --- | --- |
| `init` | Create a private state root outside the assigned workspace. |
| `doctor` | Read-only checks for the OS, Git, schema, state, and separation. |
| `lane open` | Record a complete dispatch and activate a lane. |
| `lane validate` / `lane status` | Read a manifest and its current hash. |
| `lane block` / `resume` / `abandon` / `incident` | Apply explicit lifecycle events. |
| `agent run` | Run one configured local process in the foreground. |
| `freeze` | Bind a clean Git candidate to exact fingerprints. |
| `evidence add` / `verify` | Add or verify content-addressed receipts. |
| `review open` / `record` | Inspect the frozen candidate and record a verdict. |
| `ready` | Check review and evidence requirements; reports `READY` only. |
| `close` | Close a ready lane. |

Every command has `--json`, with stable `ok`, `command`, `class`, `message`,
and `data` fields. See [docs/lifecycle.md](docs/lifecycle.md),
[docs/manifest.md](docs/manifest.md), [docs/evidence.md](docs/evidence.md), and
[docs/troubleshooting.md](docs/troubleshooting.md).

## Scope boundaries

Phase 1 has no daemon, dashboard, hosted account, telemetry service, chat
system, credential vault, remote control, automatic cleanup, or external-action
integration. It does not promise live output streaming: process output is
bounded in memory for hashing; `agent run` receipts record exit status,
duration, truncation flags, and stdout/stderr SHA-256 hashes only.
