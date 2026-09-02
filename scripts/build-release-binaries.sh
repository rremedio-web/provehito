#!/usr/bin/env bash
set -euo pipefail

usage() {
	printf '%s\n' 'usage: scripts/build-release-binaries.sh OUTPUT_DIR' >&2
	exit 2
}

if [[ $# -ne 1 ]]; then
	usage
fi

output_dir=$1

if [[ -e "$output_dir" ]]; then
	printf '%s\n' 'build-release-binaries: output directory already exists' >&2
	exit 1
fi

if ! command -v go >/dev/null 2>&1; then
	printf '%s\n' 'build-release-binaries: go not found in PATH' >&2
	exit 2
fi

if ! GIT_BIN=$(command -v git); then
	printf '%s\n' 'build-release-binaries: git not found in PATH' >&2
	exit 2
fi

if ! command -v openssl >/dev/null 2>&1; then
	printf '%s\n' 'build-release-binaries: openssl required for SHA-256' >&2
	exit 2
fi

hash_file() {
	openssl dgst -sha256 -r "$1" | awk '{print $1}'
}

GIT_DIR=$(dirname "$GIT_BIN")

git_hardened() {
	env \
		PATH="$GIT_DIR" \
		LC_ALL=C \
		LANG=C \
		TZ=UTC \
		GIT_CONFIG_NOSYSTEM=1 \
		GIT_CONFIG_GLOBAL=/dev/null \
		GIT_ATTR_NOSYSTEM=1 \
		GIT_OPTIONAL_LOCKS=0 \
		"$GIT_BIN" \
		--no-optional-locks \
		-c core.fsmonitor=false \
		-c core.untrackedCache=false \
		"$@"
}

repo_root=$(git_hardened rev-parse --show-toplevel)
cd "$repo_root"

version=${VERSION:-0.1.0}
commit=$(git_hardened rev-parse HEAD)
date=$(date -u +%Y-%m-%dT%H:%M:%SZ)
ldflags="-s -w -X main.version=${version} -X main.commit=${commit} -X main.date=${date}"

output_parent=$(cd "$(dirname "$output_dir")" && pwd)
output_base=$(basename "$output_dir")
staging=$(mktemp -d "$output_parent/${output_base}.staging.XXXXXX")
cleanup() {
	if [[ -n "${staging:-}" ]]; then
		rm -rf "$staging"
	fi
}
trap cleanup EXIT

targets=(darwin/arm64 darwin/amd64 linux/arm64 linux/amd64)
for spec in "${targets[@]}"; do
	os=${spec%/*}
	arch=${spec#*/}
	out="$staging/provehito-${os}-${arch}"
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "$ldflags" -o "$out" ./cmd/provehito
done

checksums="$staging/SHA256SUMS"
: >"$checksums"
for binary in "$staging"/provehito-*; do
	printf '%s  %s\n' "$(hash_file "$binary")" "$(basename "$binary")" >>"$checksums"
done
LC_ALL=C sort -o "$checksums" "$checksums"

mv "$staging" "$output_dir"
staging=""

printf '%s\n' "build-release-binaries: installed to $output_dir"
