# Lifecycle

A lane is an explicit state machine. The normal path is:

```text
PLANNED -> ACTIVE -> FROZEN -> REVIEWED -> READY -> CLOSED
```

`lane open` validates the dispatch, constructs the planned record, and persists
it as `ACTIVE` in one operation. `PLANNED` remains part of the schema and is
also the source state recorded when a planned lane is blocked.

## States

| State | Meaning |
| --- | --- |
| `PLANNED` | A lane definition before activation. |
| `ACTIVE` | Dispatch is fixed and a writer may run. |
| `FROZEN` | A clean Git candidate is bound to exact fingerprints. |
| `REVIEWED` | A verdict is bound to the frozen candidate and evidence. |
| `READY` | Declared checks and review requirements are satisfied. This is not authorization. |
| `CLOSED` | The lane is complete. Terminal. |
| `BLOCKED` | Work stopped; `blocked_from` records the state to resume. |
| `ABANDONED` | Deliberately ended. Terminal. |
| `INCIDENT` | An incident was recorded. Terminal. |

## Events and commands

| Command | Required transition |
| --- | --- |
| `lane open` | `PLANNED -> ACTIVE` |
| `freeze` | `ACTIVE -> FROZEN`; the Git workspace must be clean. |
| `review record` | `FROZEN -> REVIEWED`; the candidate and evidence must still match. |
| `ready` | `REVIEWED -> READY`; all required evidence must be verified success. |
| `close` | `READY -> CLOSED` |
| `lane block` | Any non-terminal state -> `BLOCKED` |
| `lane resume` | `BLOCKED -> blocked_from` when that source state is resumable |
| `lane abandon` | Any non-terminal state -> `ABANDONED` |
| `lane incident` | Any non-terminal state -> `INCIDENT` |

`lane validate` and `lane status` read the manifest and current hash; they do
not transition it. `lane list` reads all canonical lane manifests and returns
sorted current-state rows without creating a persisted index. Every mutation uses an expected prior hash where the
command exposes one, so a stale operator cannot silently overwrite a newer
manifest. An undeclared transition fails with `POLICY_OR_TRANSITION` (exit
20).

## Freeze and review invariants

Freeze records the base commit, head commit, tree identity, binary diff hash,
dispatch hash, and time. A dirty workspace, untracked file, unsupported Git
output, or later candidate drift prevents the next gate from passing.

Review is about those frozen bytes, not a branch name or a prose claim. An
independent-family policy requires the reviewer family and seat ID to differ
from the writer family and seat ID. Seat IDs are operator/orchestrator-declared
identifiers: they make separation explicit within the manifest, but do not
authenticate the host process. Agent output containing words such as “approved”
has no effect.

## Blocking and terminal states

`BLOCKED` preserves the prior resumable state in `blocked_from`. A subsequent
`lane resume` returns to exactly that state after the operator addresses the
blocker. A stale or abandoned writer lease causes the next attempted run to
block the lane before another writer can use the workspace.

`ABANDONED`, `INCIDENT`, and `CLOSED` are terminal in Phase 1. They reject
further lifecycle events. Recovery from an incident is an operator decision;
the engine does not infer or authorize it.

## Readiness boundary

`READY` is a recorded result of local checks, candidate identity, evidence, and
review policy. It is never permission to push, merge, deploy, publish, or make
another external change. Those actions are outside Phase 1 and remain human
only.
