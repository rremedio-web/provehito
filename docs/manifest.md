# Manifest

Each lane has one canonical JSON manifest in the state root:

```text
<state-root>/lanes/<lane-id>.json
```

The published schema is [schema/v1/manifest.schema.json](../schema/v1/manifest.schema.json).
YAML and unknown JSON fields are not authoritative in Phase 1.

## Top-level fields

| Field | Purpose |
| --- | --- |
| `schema_version` | Schema version; Phase 1 requires `1`. |
| `lane_id` | Lowercase slug identifying the lane. |
| `state` | Current lifecycle state. |
| `blocked_from` | Prior state, present only while `state` is `BLOCKED`. |
| `dispatch` | Workspace, identities, policy lists, review policy, and limits. |
| `dispatch_hash` | SHA-256 hash of canonical dispatch JSON. |
| `freeze` | Base/head/tree/diff candidate fingerprints and timestamp. |
| `review` | Reviewer, family, verdict, fingerprint, evidence hashes, timestamp. |
| `evidence` | Named references to immutable receipt hashes. |
| `failures` | Typed failure records, when recorded by a caller. |
| `created_at`, `updated_at` | UTC RFC3339 timestamps to the second. |
| `external_actions_human_only` | Must be `true`. |

## Dispatch

`dispatch` records the assigned workspace and source-control identity, writer,
adapter, family, cost class, allowed paths, forbidden paths, non-goals,
required checks, review policy, and three numeric limits:
`max_seconds`, `max_output_bytes`, and `max_memory_bytes`.

These values are policy data. Phase 1 uses the time and output values to gate an
`agent run`; it records `allowed_paths`, `forbidden_paths`, and
`max_memory_bytes` but does not enforce them as an OS sandbox. The configured
executable remains a trusted operator choice and is not sandboxed by Provehito.

## Freeze and review binding

When `freeze` succeeds, the manifest records:

- the chosen Git base and head commits;
- the head tree identity;
- a canonical binary diff hash;
- the candidate equivalent hash;
- the freeze timestamp.

When a review is recorded, its `fingerprint` must equal the frozen candidate.
Its evidence hashes must identify verified successful receipts bound to the
same candidate and dispatch. Changing the candidate or required evidence makes
the review unusable for `READY`.

## Canonicalization and integrity

Manifest JSON is canonicalized before hashing and persistence. Reads reject
non-canonical bytes, unknown fields, invalid hashes, invalid lifecycle shapes,
timestamp formats other than UTC RFC3339 seconds, symlinked state paths, and
hash mismatches. Updates are atomic and compare the expected prior hash before
changing the record.

Initialization creates the state root and its `lanes` and `evidence`
directories with mode `0700`. The state root must be separate from the assigned
workspace; runtime data is not placed inside product workspaces.

## Inspecting a lane

```bash
provehito lane status --state "$STATE" --id demo --json
provehito lane validate --state "$STATE" --id demo --json
```

Both commands return the lane identifier, current state, manifest hash, and
manifest path. They do not repair or rewrite a manifest. `doctor` is also
read-only and checks the state root, schema readability, Git, and workspace
separation.
