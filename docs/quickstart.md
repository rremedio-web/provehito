# Quickstart

This walkthrough uses a temporary, neutral Git repository and a standard
system executable as the configured local process. It leaves the source tree
untouched. Run it from the Provehito repository root with Go 1.26 or later.

The state root and workspace must be different directories. The state root is
private runtime data; the workspace is the Git repository being coordinated.

```bash
set -euo pipefail

TOY=$(mktemp -d)
REPO="$TOY/toy-project"
STATE="$TOY/state"
BIN="$TOY/provehito"
mkdir -p "$REPO"

git -C "$REPO" init -q
git -C "$REPO" config user.email docs@example.invalid
git -C "$REPO" config user.name "Toy Operator"
printf 'one\n' > "$REPO/fixture.txt"
git -C "$REPO" add fixture.txt
git -C "$REPO" commit -qm initial
printf 'two\n' > "$REPO/fixture.txt"
git -C "$REPO" add fixture.txt
git -C "$REPO" commit -qm candidate

go build -o "$BIN" ./cmd/provehito

"$BIN" init \
  --state "$STATE" --workspace "$REPO" --json
"$BIN" doctor \
  --state "$STATE" --workspace "$REPO" --json

OPEN_JSON=$("$BIN" lane open \
  --state "$STATE" --id demo --workspace "$REPO" \
  --writer writer-1 --family family-a --seat-id writer-seat --source-control git \
  --adapter local --cost-class economy \
  --allowed-paths . --forbidden-paths generated \
  --non-goals deploy --required-checks fixture-check \
  --review-policy independent --max-seconds 5 \
  --max-output-bytes 4096 --max-memory-bytes 0 --json)
printf '%s\n' "$OPEN_JSON"
OPEN_HASH=$(printf '%s\n' "$OPEN_JSON" |
  sed -n 's/.*"hash":"\([0-9a-f]\{64\}\)".*/\1/p')

"$BIN" lane status --state "$STATE" --id demo --json

# /bin/echo is a neutral configured executable. Its argument is passed
# literally; Provehito does not invoke a shell.
"$BIN" agent run \
  --state "$STATE" --lane demo --profile /bin/echo \
  --profile-id local --family family-a --seat-id writer-seat --cost-class economy \
  --capability writer --timeout 5s --output-bytes 4096 \
  --arg agent-run --json

FREEZE_JSON=$("$BIN" freeze \
  --state "$STATE" --lane demo --expected-hash "$OPEN_HASH" \
  --base HEAD~1 --json)
printf '%s\n' "$FREEZE_JSON"
CANDIDATE_HASH=$(printf '%s\n' "$FREEZE_JSON" |
  sed -n 's/.*"candidate_hash":"\([0-9a-f]\{64\}\)".*/\1/p')

RECEIPT_JSON=$("$BIN" evidence add \
  --state "$STATE" --lane demo --method fixture-check \
  --probe 'git fixture check' --result pass --json)
printf '%s\n' "$RECEIPT_JSON"
RECEIPT_REF=$(printf '%s\n' "$RECEIPT_JSON" |
  sed -n 's/.*"ref":"\([0-9a-f]\{64\}\)".*/\1/p')
"$BIN" evidence verify \
  --state "$STATE" --ref "$RECEIPT_REF" --json

"$BIN" review open --state "$STATE" --lane demo --json
"$BIN" review record \
  --state "$STATE" --lane demo --reviewer reviewer-1 \
  --family family-b --seat-id reviewer-seat --verdict PASS --fingerprint "$CANDIDATE_HASH" --json
"$BIN" ready --state "$STATE" --lane demo --json
"$BIN" close --state "$STATE" --lane demo --json
"$BIN" lane status --state "$STATE" --id demo --json
```

The final status is `CLOSED`. The `ready` response includes the explicit banner
`READY is not authorization`. Keep the state directory if you need to inspect
the manifest and receipts; remove the temporary directory when finished.

## What each step proves

1. `init` creates `state/lanes` and `state/evidence` with private directory
   permissions and rejects overlap with the workspace.
2. `lane open` records a complete dispatch and returns the active manifest hash.
3. `agent run` acquires one writer lease, runs in the assigned workspace, and
   records a receipt. Output is captured with a byte limit; it is not promised
   as a live stream.
4. `freeze` requires a clean Git workspace and records base, head, tree, and
   diff fingerprints. The expected manifest hash prevents stale updates.
5. `evidence add` stores a SHA-256 receipt bound to the frozen candidate and
   manifest; `verify` checks the immutable bytes.
6. Review is recorded only for the frozen candidate, a different family, and a different seat.
7. `ready` checks the lifecycle, exact candidate, required successful evidence,
   and review binding. It does not call an external action.
