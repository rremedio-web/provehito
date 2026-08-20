# Evidence and review

Evidence is immutable, content-addressed data stored outside the assigned
workspace. A receipt is addressed by its lowercase SHA-256 hash at:

```text
<state-root>/evidence/sha256/<first-two-hash-characters>/<hash>.json
```

The path is an implementation detail; use the hash returned by the CLI as the
portable reference.

## Receipt contents

A v1 receipt binds:

- `method_id` and a bounded `probe` description;
- the frozen `candidate_hash`;
- the canonical dispatch hash in `manifest_hash`;
- `result_class` and its matching `exit_code`;
- optional artifact references;
- a UTC timestamp and canonical content hash.

`SUCCESS` is the only result class with exit code `0`. Typed failures retain
their class and stable code. A receipt is not a free-form approval: prose is
not converted into a verdict or lifecycle event.

## CLI operations

Add a required check after the lane is frozen:

```bash
provehito evidence add \
  --state "$STATE" --lane demo --method fixture-check \
  --probe 'git fixture check' --result pass --json
```

Use the returned `ref` with:

```bash
provehito evidence verify \
  --state "$STATE" --ref "$RECEIPT_REF" --json
```

`evidence add` rejects writes before `FROZEN`, duplicate methods, and invalid
result/class combinations. `evidence verify` reloads and validates the
content-addressed receipt; tampering is an `INTEGRITY` failure (exit 60).

`agent run` creates and returns an `agent-run` receipt whose probe records exit
status, duration, truncation flags, and stdout/stderr SHA-256 hashes. Receipt
artifact references name `stdout` and `stderr` with those hashes. Provehito does
not automatically persist subprocess output as plaintext. CLI JSON for `agent
run` returns the same hashes and flags, never raw child bytes. It does not
attach that receipt to the lane's required evidence. Add each declared required
check explicitly after freeze. Phase 1 does not promise a live user-facing
output stream.

Manual `evidence add` probe text is operator-authored and persisted. Do not
place secrets in `--probe` values.

## Review binding

Open the frozen candidate and inspect the exact fingerprints and evidence:

```bash
provehito review open --state "$STATE" --lane demo --json
```

Record a human-authored or independently supplied verdict only after checking
the returned candidate hash:

```bash
provehito review record \
  --state "$STATE" --lane demo --reviewer reviewer-1 \
  --family family-b --verdict PASS \
  --fingerprint "$CANDIDATE_HASH" --json
```

The review family must differ from the writer family when independent review is
required. `PASS` is bound to the exact frozen candidate and verified required
evidence. Candidate drift, missing evidence, failed evidence, or a mismatched
fingerprint produces `CANDIDATE_OR_REVIEW` (exit 50), not a false green.

## Readiness

`ready` rechecks lifecycle state, current candidate identity, review binding,
required receipt hashes, and independent-family policy. A successful response
contains the banner `READY is not authorization`. Provehito never invokes a
push, merge, deploy, publish, or other external action as a consequence.
