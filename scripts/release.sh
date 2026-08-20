#!/usr/bin/env bash
set -euo pipefail

usage() {
	printf '%s\n' \
		'usage: scripts/release.sh --structural-only OUTPUT_DIR' \
		'       scripts/release.sh --private-denylist ABS_PATH OUTPUT_DIR' >&2
	exit 2
}

if ! GIT_BIN=$(command -v git); then
	printf '%s\n' 'release: git not found in PATH' >&2
	exit 2
fi
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

hash_file() {
	if ! command -v openssl >/dev/null 2>&1; then
		printf '%s\n' 'release: openssl required for SHA-256' >&2
		exit 2
	fi
	openssl dgst -sha256 -r "$1" | awk '{print $1}'
}

json_field() {
	local json=$1
	local field=$2
	printf '%s' "$json" | go run ./internal/releasecheck/cmd/jsonfield "$field"
}

git_status() {
	git_hardened -c advice.statusHints=false -c core.quotepath=false status --porcelain=v1 -uall
}

require_clean_worktree() {
	if [[ -n "$(git_status)" ]]; then
		printf '%s\n' 'release: dirty worktree (including untracked files)' >&2
		exit 1
	fi
}

private_denylist=""
output_dir=""

if [[ $# -lt 2 ]]; then
	usage
fi

case "$1" in
	--structural-only)
		output_dir=$2
		;;
	--private-denylist)
		if [[ $# -ne 3 ]]; then
			usage
		fi
		private_denylist=$2
		output_dir=$3
		if [[ ! -f "$private_denylist" ]]; then
			printf '%s\n' 'release: private denylist is not a readable regular file' >&2
			exit 2
		fi
		;;
	*)
		usage
		;;
esac

if [[ -e "$output_dir" ]]; then
	printf '%s\n' 'release: output directory already exists' >&2
	exit 1
fi

repo_root=$(git_hardened rev-parse --show-toplevel)
cd "$repo_root"

require_clean_worktree

work=$(mktemp -d)
staging=""
cleanup() {
	rm -rf "$work"
	if [[ -n "$staging" ]]; then
		rm -rf "$staging"
	fi
}
trap cleanup EXIT

archive1="$work/archive1.zip"
archive2="$work/archive2.zip"
expected_list="$work/expected.txt"

git_hardened archive --format=zip --prefix=provehito/ HEAD >"$archive1"
git_hardened archive --format=zip --prefix=provehito/ HEAD >"$archive2"

hash1=$(hash_file "$archive1")
hash2=$(hash_file "$archive2")
if [[ "$hash1" != "$hash2" ]]; then
	printf '%s\n' 'release: archive hashes differ between builds' >&2
	exit 1
fi

git_hardened ls-tree -r --name-only HEAD | LC_ALL=C sort >"$expected_list"
tracked_count=$(wc -l <"$expected_list" | tr -d ' ')

checker_args=(--expected-list "$expected_list")
if [[ -n "$private_denylist" ]]; then
	checker_args+=(--private-denylist "$private_denylist")
fi

result1=$(go run ./internal/releasecheck/cmd/releasecheck "${checker_args[@]}" "$archive1")
result2=$(go run ./internal/releasecheck/cmd/releasecheck "${checker_args[@]}" "$archive2")

structural_status=$(json_field "$result1" structural_status)
private_status=$(json_field "$result1" private_status)
member_count=$(json_field "$result1" member_count)

if [[ "$structural_status" != "PASS" ]]; then
	printf '%s\n' 'release: structural check failed' >&2
	printf '%s\n' "$result1" >&2
	exit 1
fi
if [[ -n "$private_denylist" && "$private_status" != "PASS" ]]; then
	printf '%s\n' 'release: private denylist check failed' >&2
	printf '%s\n' "$result1" >&2
	exit 1
fi
if [[ "$result1" != "$result2" ]]; then
	printf '%s\n' 'release: checker results differ between archives' >&2
	exit 1
fi

head_sha=$(git_hardened rev-parse HEAD)
tree_sha=$(git_hardened rev-parse 'HEAD^{tree}')
git_version=$(git_hardened --version | head -1)
go_version=$(go version | head -1)

output_parent=$(cd "$(dirname "$output_dir")" && pwd)
output_base=$(basename "$output_dir")
staging=$(mktemp -d "$output_parent/${output_base}.staging.XXXXXX")

cp "$archive1" "$staging/provehito.zip"

go run ./internal/releasecheck/cmd/releasereceipt \
	--output "$staging/receipt.json" \
	--head "$head_sha" \
	--tree "$tree_sha" \
	--tracked-count "$tracked_count" \
	--member-count "$member_count" \
	--archive-hash "$hash1" \
	--archive-hash-build1 "$hash1" \
	--archive-hash-build2 "$hash2" \
	--structural-status "$structural_status" \
	--private-status "$private_status" \
	--git-version "$git_version" \
	--go-version "$go_version"

mv "$staging" "$output_dir"
staging=""

printf '%s\n' "release: installed to $output_dir"
