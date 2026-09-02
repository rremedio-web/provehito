# Security

Provehito Phase 1 is a local CLI and Go core for coordinating configured local
processes. Security claims are limited to the trusted local host, operator
account, and state-root boundary.

## Boundary

Phase 1 detects ordinary state corruption, unexpected symlinks, path escapes,
and concurrent Provehito commands. It does not provide malicious same-user
race resistance. Configured subprocesses are not sandboxed and may use their
host permissions, including network access. The core implements no sockets and
no outward actions. Allowed paths, forbidden paths, and `max_memory_bytes` are
recorded declarations, not OS-enforced limits. Time and output limits and
process-group cancellation are implemented on macOS and Linux. Provehito does
not manage, inject, or automatically persist configured subprocess stdout/stderr
as plaintext evidence; manual `evidence add` probe text is operator-authored
and must not contain secrets.

Relevant checks live in `core/workspace/workspace_test.go`,
`core/process/supervisor_test.go`, `internal/sourceguard/sourceguard_test.go`,
and `internal/releasecheck/releasecheck_test.go`.

## Release security (Stage 2)

Releases are built from `git archive` only. `scripts/release.sh` requires a
clean worktree, builds two identical archives, validates them with
`internal/releasecheck`, and writes a deterministic receipt. Structural scans
run in contributors/CI; private denylist scans are for release certification
only. Remote gates (govulncheck, gitleaks, SBOM) are defined in
`.github/workflows/security.yml` and currently run on GitHub Actions. ClamAV
and YARA are not configured in this stage. See [docs/releasing.md](docs/releasing.md).

Provehito is a pre-release local CLI. CI currently validates the project on
macOS and Linux. Security automation includes gitleaks, govulncheck and
deterministic SBOM generation. The project has not yet published a stable
release.

## Reporting a vulnerability

Do not include secrets, private data, or exploit details in a public issue.
Use the hosting platform’s private vulnerability-reporting workflow. This
document does not claim that GitHub private vulnerability reporting is
currently enabled.

Please provide the affected version or commit, operating system, minimal
reproduction, impact, and any relevant logs with sensitive values removed.
Allow maintainers reasonable time to investigate and coordinate a fix before
public disclosure.

## Scope notes

Reports about a configured executable’s own behavior, a malicious same-user
filesystem race, or an absent OS sandbox are outside the Phase 1 guarantees,
though they may still help improve documentation or future designs.
