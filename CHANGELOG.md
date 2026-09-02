# Changelog

All notable changes are recorded here.

## Unreleased

- Replaced "certified toolchain" wording with a pin to Go 1.26.7.
- Combined README limitation and scope notes, and added a terminal GIF of the
  dispatch-to-ready lifecycle.

## 0.1.0 — 2026-09-02

- Added the local CLI and reusable Go core for explicit lane lifecycle,
  one-writer workspace leases, clean Git freeze, content-addressed evidence,
  and frozen-candidate review binding.
- Added provider-neutral local-process profiles with literal arguments,
  environment allowlists, deterministic capability/cost routing, bounded
  output, timeouts, and macOS/Linux process-group cancellation.
- Added canonical JSON manifests, typed failure classes, path and symlink
  checks, atomic state writes, source authority guards, and the public
  architecture, adapter, threat-model, security, contribution, conduct, and
  governance documentation.
- Aligned the Go module path with the public repository
  `github.com/rremedio-web/provehito`.
- Added `provehito --version` / `provehito --help`, `go install` instructions,
  and maintainer binary-build steps for macOS and Linux.

This entry describes the Phase 1 scope; it is not a claim that remote actions,
credential storage, sandboxing, or hosted operation are supported.
