# Contributing

Contributions should preserve deterministic local behavior, explicit authority
boundaries, and provider-neutral public content.

## Before submitting a change

From the repository root, run:

```sh
scripts/acceptance.sh
```

This locally runs formatting, vetting, the complete test suite, race tests,
the source guard, the neutral-content check, a structural-only release build,
and whitespace validation. The script is the required acceptance entry point.
Ordinary acceptance does not require a private denylist. Release certification
with a private denylist uses `scripts/release.sh --private-denylist`. See
[docs/releasing.md](docs/releasing.md).

Do not add private names, home paths, credentials, transcripts, non-neutral
domains, or organization-specific policy to public files. Keep state-root and
workspace assumptions explicit. Changes that affect authority boundaries must
include tests; `internal/sourceguard/sourceguard_test.go` is the reference for
no-outward-action checks.

## Change shape

Keep core rules in `core/` packages and CLI parsing/output in
`cmd/provehito/`. Use canonical JSON and typed failures for durable state.
Launch configured processes with executable and argument arrays; do not add
shell interpolation, credential injection, sockets, remote mutations, or
automatic push/merge/deploy behavior.

For a behavior change, explain the boundary, add or update focused tests, and
describe any platform limitation. Documentation should cite the implementation
or test path that supports a security or architecture claim.

## Review checklist

- The acceptance script passes from a clean local checkout.
- Tests cover new failure and authority-boundary cases.
- Public text remains provider-neutral and contains no sensitive material.
- The change does not silently broaden filesystem, process, network, or
  external-action authority.
- Any unsupported platform behavior is stated explicitly.

Keep commits focused and make the result easy to inspect. Do not bypass checks
to obtain a passing result.
