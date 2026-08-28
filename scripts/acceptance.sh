#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"
export GOCACHE="$repo_root/.gocache"

test -z "$(gofmt -l .)"
go vet ./...
go test ./...
go test -race ./...
go test ./internal/sourceguard
go run ./internal/sourceguard .

if [[ -n "${PROVEHITO_PRIVATE_DENYLIST:-}" ]]; then
	scripts/check-neutral.sh "$PROVEHITO_PRIVATE_DENYLIST"
else
	scripts/check-neutral.sh
fi

release_parent=$(mktemp -d)
release_out="$release_parent/out"
release_cleanup() {
	rm -rf "$release_parent"
}
trap release_cleanup EXIT

scripts/release.sh --structural-only "$release_out"
test -f "$release_out/provehito.zip"
test -f "$release_out/receipt.json"

if [[ -n "$(git -c advice.statusHints=false -c core.quotepath=false status --porcelain=v1 -uall)" ]]; then
	printf '%s\n' 'acceptance: release left repository dirty' >&2
	exit 1
fi

git diff --check
