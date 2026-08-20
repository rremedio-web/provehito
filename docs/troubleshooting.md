# Troubleshooting

Start with machine-readable output and the process exit code:

```bash
provehito doctor --state "$STATE" --workspace "$REPO" --json
provehito lane status --state "$STATE" --id demo --json
```

Successful JSON has `"ok":true`, `"class":"OK"`, and exit code `0`. Errors
have `"ok":false`, a stable `class`, a command-specific `message`, and a
nonzero exit code. Human output begins with `RESULT:` and includes a correction.

## Stable exit classes

| Code | Class | Typical cause | First correction |
| ---: | --- | --- | --- |
| 10 | `USAGE_OR_SCHEMA` | Missing flag, invalid slug, incomplete dispatch, or malformed manifest. | Correct command inputs or schema fields. |
| 20 | `POLICY_OR_TRANSITION` | Illegal lifecycle move, wrong family, or profile exceeds dispatch policy. | Use the declared state transition and matching dispatch. |
| 30 | `WORKSPACE_DRIFT` | Git workspace is dirty or its identity differs from the candidate. | Inspect the workspace and restore a clean, intended candidate. |
| 40 | `TOOLING_OR_ADAPTER` | Missing executable, launch failure, timeout, cancellation, or process supervision failure. | Repair the configured executable or its local tooling. |
| 50 | `CANDIDATE_OR_REVIEW` | Failed required check, candidate drift, missing evidence, or review mismatch. | Re-establish the exact candidate and required successful evidence. |
| 60 | `INTEGRITY` | State, hash, receipt, path, symlink, or prior-hash corruption. | Stop and inspect the state root; do not silently repair bytes. |
| 70 | `CONCURRENCY` | Another writer holds the workspace lease or an abandoned lease remains. | Wait for the active writer or follow the explicit blocked-lane decision. |

Tooling failures are not candidate verdicts. A process timeout is exit 40; a
process that exits nonzero is recorded as candidate/review failure exit 50.

## Common conditions

### “State root must be an absolute path” or “state root missing”

Pass an absolute path to `--state`, initialize it first, and keep it outside
the assigned workspace:

```bash
provehito init --state "$STATE" --workspace "$REPO" --json
```

Initialization rejects overlap, symlinked state directories, and non-directory
workspaces. It creates private `lanes` and `evidence` directories.

### `freeze` returns `WORKSPACE_DRIFT`

Freeze requires an empty Git status, including untracked files. Check the
workspace with Git, remove or commit only the intended changes, then retry with
the current manifest hash:

```bash
git -C "$REPO" status --short --untracked-files=all
provehito lane status --state "$STATE" --id demo --json
```

Do not treat ignored files or a branch label as proof that the candidate is
unchanged; freeze uses exact Git object and diff identities.

### `review record` or `ready` returns `CANDIDATE_OR_REVIEW`

Reopen the frozen record and compare its candidate hash with the fingerprint
passed to `review record`. Verify every required receipt. Any candidate change
after review invalidates readiness. Phase 1 has no rewind transition, so
preserve the failed lane and open a new lane for the corrected candidate rather
than editing the manifest by hand.

### `agent run` returns `TOOLING_OR_ADAPTER`

The profile executable must be an existing absolute path. Arguments are passed
as an array, never as shell text. The configured process is trusted and not
sandboxed; use an operator-supplied OS sandbox for stronger containment.
`agent run` enforces its timeout and captured-output limit, but Phase 1 does not
enforce `allowed_paths`, `forbidden_paths`, or `max_memory_bytes` as OS limits.

### `CONCURRENCY` or an abandoned lease

Only one writer may hold a workspace lease. A second run fails immediately.
If a supervisor dies, its OS lock releases but the durable lease remains; the
next attempted run records the lane as `BLOCKED` before another writer can run.
Phase 1 has no abandoned-lease recovery command. Preserve the state for
inspection and choose `lane abandon` or `lane incident`; use a new isolated
state root for a fresh run. Do not delete lease or manifest files to force
progress.

### “READY” is being treated as permission

That is a boundary violation. `READY` only means the declared local checks,
frozen candidate, evidence, and review requirements passed. Provehito does not
push, merge, deploy, publish, or store credentials; external actions remain
human-only.
