#!/usr/bin/env bash
# Record docs/assets/lifecycle.gif from a real local CLI run of the toy
# walkthrough. Requires Go, Python 3 with Pillow, and optionally ImageMagick.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

output=${1:-docs/assets/lifecycle.gif}
export GOCACHE="${GOCACHE:-$repo_root/.gocache}"

toy=$(mktemp -d)
cleanup() {
	rm -rf "$toy"
}
trap cleanup EXIT

repo=$toy/toy-project
state=$toy/state
bin=$toy/bin
mkdir -p "$repo" "$bin"

git -C "$repo" init -q
git -C "$repo" config user.email docs@example.invalid
git -C "$repo" config user.name "Toy Operator"
printf 'one\n' >"$repo/fixture.txt"
git -C "$repo" add fixture.txt
git -C "$repo" commit -qm initial
printf 'two\n' >"$repo/fixture.txt"
git -C "$repo" add fixture.txt
git -C "$repo" commit -qm candidate

GOTOOLCHAIN="${GOTOOLCHAIN:-local}" go build -o "$bin/provehito" ./cmd/provehito
PATH="$bin:$PATH"

provehito init --state "$state" --workspace "$repo" >/dev/null

run() {
	local out
	out=$(provehito "$@")
	printf '%s\n' "$out"
}

steps_json=$toy/steps.json

append_step() {
	local label=$1
	local displayed=$2
	local output=$3
	python3 - "$steps_json" "$label" "$displayed" "$output" <<'PY'
import json, sys
path, label, command, output = sys.argv[1:5]
try:
    doc = json.loads(open(path).read())
except FileNotFoundError:
    doc = {"steps": []}
doc["steps"].append({"label": label, "command": command, "output": output.rstrip() + "\n"})
open(path, "w").write(json.dumps(doc))
PY
}

open_out=$(run lane open \
	--state "$state" --id demo --workspace "$repo" \
	--writer writer-1 --family family-a --seat-id writer-seat --source-control git \
	--adapter local --cost-class economy \
	--allowed-paths . --forbidden-paths generated \
	--non-goals deploy --required-checks fixture-check \
	--review-policy independent --max-seconds 5 \
	--max-output-bytes 4096 --max-memory-bytes 0)
append_step "dispatch" \
	'provehito lane open --id demo --workspace "$REPO" $OPEN_FLAGS' \
	"$open_out"

run_out=$(run agent run \
	--state "$state" --lane demo --profile /bin/echo \
	--profile-id local --family family-a --seat-id writer-seat --cost-class economy \
	--capability writer --timeout 5s --output-bytes 4096 \
	--arg agent-run)
append_step "run" \
	'provehito agent run --lane demo --profile /bin/echo $RUN_FLAGS --arg agent-run' \
	"$run_out"

freeze_out=$(run freeze --state "$state" --lane demo --base HEAD~1)
append_step "freeze" \
	'provehito freeze --lane demo --base HEAD~1' \
	"$freeze_out"

candidate=$(python3 - "$state" <<'PY'
import json, sys
from pathlib import Path
manifest = json.loads(Path(sys.argv[1], "lanes", "demo.json").read_text())
print(manifest["freeze"]["candidate"])
PY
)

evidence_out=$(run evidence add \
	--state "$state" --lane demo --method fixture-check \
	--probe 'git fixture check' --result pass)
append_step "evidence" \
	'provehito evidence add --lane demo --method fixture-check --result pass' \
	"$evidence_out"

review_out=$(run review record \
	--state "$state" --lane demo --reviewer reviewer-1 \
	--family family-b --seat-id reviewer-seat --verdict PASS \
	--fingerprint "$candidate")
append_step "review" \
	'provehito review record --lane demo --verdict PASS --fingerprint $CANDIDATE' \
	"$review_out"

ready_out=$(run ready --state "$state" --lane demo)
append_step "ready" \
	'provehito ready --lane demo' \
	"$ready_out"

python3 - "$steps_json" "$output" <<'PY' | python3 scripts/render-lifecycle-gif.py
import json, sys
from pathlib import Path
src, dest = sys.argv[1], sys.argv[2]
doc = json.loads(Path(src).read_text())
doc["output"] = dest
print(json.dumps(doc))
PY

if command -v magick >/dev/null 2>&1; then
	optimized=$toy/lifecycle.opt.gif
	if magick "$output" -layers optimize -loop 0 "$optimized" \
		&& [[ -f "$optimized" ]] \
		&& [[ $(wc -c <"$optimized") -gt 0 ]] \
		&& [[ $(wc -c <"$optimized") -lt $(wc -c <"$output") ]]; then
		mv "$optimized" "$output"
	fi
elif command -v convert >/dev/null 2>&1; then
	optimized=$toy/lifecycle.opt.gif
	if convert "$output" -layers optimize -loop 0 "$optimized" \
		&& [[ -f "$optimized" ]] \
		&& [[ $(wc -c <"$optimized") -gt 0 ]] \
		&& [[ $(wc -c <"$optimized") -lt $(wc -c <"$output") ]]; then
		mv "$optimized" "$output"
	fi
fi

python3 - "$output" <<'PY'
from pathlib import Path
import sys
path = Path(sys.argv[1])
size = path.stat().st_size
print(f"wrote {path} ({size} bytes)")
if size > 500_000:
    raise SystemExit("lifecycle gif is larger than 500KB")
PY
