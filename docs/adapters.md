# Adapters

An adapter is a provider-neutral launch profile for one local executable.
Profiles are configuration data, not authority-bearing agents. The adapter
layer validates the profile, chooses an eligible profile, and returns process
events; it cannot change lane state directly.

## Profile contract

`core/adapter/profile.go` defines these fields:

- `ID`: non-empty profile identifier.
- `Executable`: an absolute executable path. It is invoked directly, never as
  a shell command string.
- `Args`: literal arguments appended to the launch request.
- `Capabilities`: a non-empty set of unique capability names.
- `Family`: a non-empty family label used by review policy.
- `CostRank`: a non-negative integer used for deterministic routing.
- `EnvAllowlist`: unique, syntactically valid environment-variable names.
- `VersionProbeArgs`: optional literal probe arguments.
- `Timeout` and `OutputLimit`: both must be positive.

Ambient environment variables are not part of the contract. The supervisor
rebuilds the child environment from the explicit allowlist, as tested in
`core/process/supervisor_test.go`. Arguments are passed through
`os/exec` without shell interpolation. A configured executable remains
outside Provehito’s sandbox boundary and retains the host permissions granted
to it.

## Selection

`core/adapter/router.go` exposes capability-based selection. A requirement
names a capability and may exclude a reviewer family. Invalid profiles fail
validation; no eligible profile is a policy failure. Among eligible profiles,
the lowest `CostRank` wins, with the lexicographically smallest `ID` as the
stable tie-break. `core/adapter/adapter_test.go` covers capability mismatch,
family exclusion, invalid fields, and tie-breaking.

The core does not encode model, vendor, account, or service assumptions.
Profile IDs, family labels, capabilities, cost classes, executable paths,
timeouts, and output limits are supplied by the operator’s local
configuration. The generic Phase 1 adapter is suitable for a local fake
process or another explicitly configured executable.

## Supervision and results

`core/process/supervisor.go` accepts a workspace, active writer lease, profile,
and additional literal arguments. It runs one foreground process and returns
exit code, duration, bounded stdout/stderr, stream hashes, truncation state,
timeout, cancellation, and signal information. On macOS and Linux,
`core/process/supervisor_unix.go` places the child in a process group so
timeout or cancellation can terminate the group. Unsupported platforms do
not receive a Phase 1 support claim.

The adapter cannot approve a candidate, mark a lane ready, or authorize an
external action. Process failure, timeout, malformed configuration, and
non-zero candidate exit remain typed results; see `core/failure/` and
`core/evidence/` for their durable classification.
