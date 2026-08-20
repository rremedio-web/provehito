#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

if (( $# > 1 )); then
	printf '%s\n' 'usage: scripts/check-neutral.sh [private-denylist]' >&2
	exit 2
fi

failed=0

decode_pattern() {
	printf '%s' "$1" | base64 -d 2>/dev/null || printf '%s' "$1" | base64 -D
}

check_content() {
	local label=$1
	local pattern_b64=$2
	local pattern
	pattern=$(decode_pattern "$pattern_b64")
	local matches
	if matches=$(git grep -nEi -- "$pattern" -- . ':(exclude).superpowers/**' 2>/dev/null); then
		printf 'neutral check: %s\n' "$label" >&2
		failed=1
	fi
}

# Patterns are base64-encoded so this script can ship in release archives.
check_content 'absolute home path found' 'KF58W15bOmFsbnVtOl1fXSkvKFVzZXJzfGhvbWUpL1teWzpzcGFjZTpdIjw+XSs='
check_content 'credential-shaped value found' 'KC0tLS0tQkVHSU5bWzpzcGFjZTpdXStbQS1aMC05IF0qUFJJVkFURSBLRVktLS0tLXxcYihBS0lBfEFTSUEpW0EtWjAtOV17MTZ9XGJ8XGJnaFtwb3Vzcl1fW0EtWmEtejAtOV9dezIwLH1cYnxcYmdpdGh1Yl9wYXRfW0EtWmEtejAtOV9dezIwLH1cYnxcYnhveFtiYXByc10tW0EtWmEtejAtOS1dezE2LH1cYnxcYkJlYXJlcltbOnNwYWNlOl1dK1tBLVphLXowLTkuX34rLz0tXXsyMCx9XGJ8Oi8vW146L1s6c3BhY2U6XV0rOlteQFs6c3BhY2U6XV0rQCk='
check_content 'transcript artifact found' 'QkVHSU5bWzpzcGFjZTpdXSsoVFJBTlNDUklQVHxDSEFUfENPTlZFUlNBVElPTil8KF58W15bOmFsbnVtOl1fXSl0cmFuc2NyaXB0X2lkKFteWzphbG51bTpdX118JCk='

while IFS= read -r url; do
	url=${url%%[\"\'\)\],;]*}
	host=${url#*://}
	host=${host%%/*}
	host=${host%%:*}
	case "$host" in
		example.com|example.org|example.net|*.example.com|*.example.org|*.example.net|localhost|127.0.0.1|json-schema.org|*.json-schema.org|apache.org|*.apache.org)
			;;
		*)
			printf '%s\n' 'neutral check: non-example domain found' >&2
			failed=1
			;;
	esac
done < <(git grep -hEo -- 'https?://[^[:space:]"<>]+' -- . ':(exclude).superpowers/**' 2>/dev/null || true)

while IFS= read -r path; do
	case "$path" in
		.superpowers/*) continue ;;
		.env|.env.*|*/.env|*/.env.*|id_rsa|id_dsa|id_ecdsa|id_ed25519|*/id_rsa|*/id_dsa|*/id_ecdsa|*/id_ed25519|*.pem|*.key|*.p12|*.pfx|*.kdbx)
			printf '%s\n' 'neutral check: secret-shaped file name found' >&2
			failed=1
			;;
	esac
done < <(git ls-files)

if (( $# == 1 )); then
	denylist=$1
	if [[ ! -f "$denylist" ]]; then
		printf '%s\n' 'neutral check: private denylist is not a regular file' >&2
		exit 2
	fi
	while IFS= read -r needle || [[ -n "$needle" ]]; do
		[[ -z "$needle" || "$needle" == \#* ]] && continue
		if git grep -F -l -- "$needle" -- . ':(exclude).superpowers/**' >/dev/null 2>&1; then
			printf '%s\n' 'neutral check: private denylist match found' >&2
			failed=1
		fi
	done < "$denylist"
fi

if (( failed != 0 )); then
	exit 1
fi
printf '%s\n' 'neutral check: PASS'
