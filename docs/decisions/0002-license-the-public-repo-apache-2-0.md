# ADR 0002 — License the public repo Apache-2.0

- **Status:** Accepted
- **Date:** 2026-07-29
- **Deciders:** operator + agent
- **Context source:** `docs/priorities-2026-07-28.md P0-1`; operator interview 2026-07-29

## Context

`zakros-hq/zakros` has been public with `license: null` — default copyright — meaning nobody can legally use, copy, or contribute to the published tree. ADR 0001 keeps the repo public, so a license is mandatory, and the surrounding ecosystem Zakros is measured against is permissively licensed (Centaur: Apache-2.0 OR MIT; kubernetes-sigs/agent-sandbox: Apache-2.0; OpenHands core: MIT).

## Decision

License the repository **Apache-2.0**: add the standard `LICENSE` file at the repo root. New files need no per-file headers (single-author personal infra; headers add friction without benefit at this scale).

## Rejected alternatives

- **MIT** — workable, but no patent grant; Apache-2.0's explicit patent license is the safer default for infrastructure code at zero practical cost.
- **AGPL-3.0** — protects against SaaS capture, but Zakros is personal infrastructure (ADR 0001) with no capture concern, and copyleft deters casual reuse of exactly the pieces (broker patterns, deploy scripts) most worth sharing.
- **Stay unlicensed** — rejected: contradicts keeping the repo public at all.

## Consequences

- Anyone may use, fork, or adapt the published tree; contributions inbound are implicitly Apache-2.0.
- Adopted third-party code must be Apache-2.0-compatible (the existing 9 direct deps all are).
- The LICENSE file lands in the first tighten-up commit.
