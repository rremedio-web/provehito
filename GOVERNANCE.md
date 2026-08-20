# Governance

Provehito is maintained through transparent, evidence-based review of focused
changes. Governance protects the project’s authority boundary; it does not
delegate authority to agent output.

## Maintainers

Maintainers are responsible for reviewing changes, preserving the public
scope, coordinating releases, and responding to security or conduct reports.
They may request tests, documentation, or a narrower implementation before
accepting a change.

## Decisions

Routine changes are proposed through the project’s normal review process and
should include tests and a clear scope. Changes to the manifest contract,
lifecycle, process boundary, evidence binding, security guarantees, or
supported platforms require explicit maintainer review and corresponding
documentation.

Consensus is preferred. When consensus is unavailable, maintainers record the
decision, rationale, alternatives considered, and follow-up work in the
project’s public decision record.

## Authority

Agents, adapters, subprocess output, peer messages, and imported data have no
governance authority. They cannot approve a change, alter lifecycle state,
grant permissions, or authorize push, merge, deploy, publish, credential, or
remote-mutation actions. A human operator must make those decisions outside
the Phase 1 engine.

## Changes to governance

Governance changes require maintainer review, a documented rationale, and an
updated security or architecture note when the trust boundary changes.
