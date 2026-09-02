# Releasing

Stage 2 adds deterministic, git-archive-only source construction with local
structural validation and optional private denylist certification. Source
archives are built from `git archive` at a clean `HEAD`. Binary artifacts are
built locally by a maintainer script; Provehito itself does not upload,
publish, or deploy. A human creates the GitHub Release.

Provehito is a pre-release local CLI. CI currently validates the project on
macOS and Linux. Security automation includes gitleaks, govulncheck and
deterministic SBOM generation. The project has not yet published a stable
release.

## Toolchain

Official versions:

- Go **1.26.7** (`go` and `toolchain` directives in `go.mod`)
- govulncheck **v1.7.0**
- cyclonedx-gomod **v1.11.0**
- **openssl** (required for SHA-256 via `openssl dgst -sha256 -r`)

The language version is Go 1.26. The `toolchain` directive pins **go1.26.7** so
contributor machines, CI, and release archives compile with the same certified
patch. CI workflows must use `go-version: "1.26.7"`. `internal/sourceguard`
checks that these pins stay in sync with this document.

## Modes

### Structural-only (contributors and CI)

```sh
scripts/release.sh --structural-only OUTPUT_DIR
```

Builds `HEAD` twice via `git archive --format=zip --prefix=provehito/`,
requires equal SHA-256 hashes (via OpenSSL), validates both archives with
`internal/releasecheck`, and atomically installs `provehito.zip` plus
`receipt.json` into `OUTPUT_DIR`. **`OUTPUT_DIR` must not exist**; the script
creates a staging directory as a sibling under the parent, writes artifacts
there, then renames staging to `OUTPUT_DIR`. The output directory may be outside
the repository.

### Private denylist (release certification)

```sh
scripts/release.sh --private-denylist /absolute/path/to/denylist OUTPUT_DIR
```

Runs the same deterministic archive construction and adds a private denylist
scan. A missing or unreadable denylist is tooling-incomplete (nonzero exit).
Blank and `#` comment lines in the denylist are ignored. Matching needles are
never printed—only rule and path.

### Maintainer binaries (not uploaded by the engine)

```sh
VERSION=0.1.0 scripts/build-release-binaries.sh OUTPUT_DIR
```

Cross-compiles `CGO_ENABLED=0` binaries for `darwin/arm64`, `darwin/amd64`,
`linux/arm64`, and `linux/amd64`, injects version/commit/build-date via
`-ldflags`, and writes `SHA256SUMS`. **`OUTPUT_DIR` must not exist** and should
live outside the repository (`bin/` is a forbidden path segment in source
archives). The script does not upload artifacts.

## Publishing a GitHub Release

A human publishes. From a clean default-branch commit tagged `v0.1.0`:

1. `scripts/release.sh --structural-only SOURCE_DIR`
2. `VERSION=0.1.0 scripts/build-release-binaries.sh BINARY_DIR`
3. Create the GitHub Release with the four binaries, `SHA256SUMS`,
   `SOURCE_DIR/provehito.zip`, short notes, known limitations, and install
   instructions (`go install github.com/rremedio-web/provehito/cmd/provehito@v0.1.0`).

The hosting platform also attaches a tag source archive. The CLI still does not
push, merge, deploy, or publish.

## Preconditions

`scripts/release.sh` requires a clean worktree including untracked files.

Every Git invocation resolves the absolute `git` executable once, then runs in
an explicit minimal environment with hardened flags:

```sh
GIT_BIN=$(command -v git)
GIT_DIR=$(dirname "$GIT_BIN")

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
  -c advice.statusHints=false \
  -c core.quotepath=false \
  status --porcelain=v1 -uall
```

Local repo config may still be read, but `-c core.fsmonitor=false` prevents
fsmonitor hooks from executing during release construction.

## Receipt

`receipt.json` is deterministic structured output suitable for certification.
It records schema/checker version, `HEAD`, tree SHA, tracked and member counts,
both archive hashes, structural/private scan statuses, and `git`/`go` versions.
It contains no scanned file bytes or denylist needles.

## Release checker

`go run ./internal/releasecheck/cmd/releasecheck` validates a ZIP in memory
without extracting or executing archive contents. Archives larger than 32 MiB
are rejected before reading. Structural checks reject absolute paths, `..`
traversal, backslashes, control names, duplicate and case-fold-colliding names,
encrypted entries, symlinks and other non-regular entries, per-file and total
uncompressed size over 32 MiB, compression ratio over 100:1, NUL-bearing content,
forbidden names/segments, and content patterns for absolute paths, non-example
emails, credential shapes, and non-allowlisted URL hosts. Every regular file is
scanned with no exclusions.

Allowlisted URL hosts: `example.com`, `example.org`, `example.net` (and
subdomains), `localhost`, `127.0.0.1`, `json-schema.org`, `apache.org` (and
subdomains).

## External gates

The following run in `.github/workflows/security.yml` on the remote CI host.
Local absence or unavailability must not be treated as PASS:

| Gate | Tool | Status |
| --- | --- | --- |
| Vulnerability scan | govulncheck v1.7.0 | running in GitHub Actions |
| Secret scan | gitleaks | running in GitHub Actions |
| SBOM | cyclonedx-gomod v1.11.0 | running in GitHub Actions |
| Malware scan | ClamAV / YARA | **not configured** |

Acceptance CI in `.github/workflows/ci.yml` runs `scripts/acceptance.sh` on
macOS and Linux.

## Local verification

From a clean checkout:

```sh
go test ./internal/releasecheck
parent=$(mktemp -d)
scripts/release.sh --structural-only "$parent/out"
scripts/acceptance.sh
```

Ordinary acceptance does not require a private denylist.
