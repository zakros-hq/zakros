# ADR 0008 — Slice H2 splits into H2a transparent proxy and H2b enforcement

- **Status:** Accepted
- **Date:** 2026-07-29 (backfill; decided at H2 implementation time, H2a shipped in `e435cf0`)
- **Deciders:** operator
- **Context source:** `docs/phase-2-plan.md §9 H2a vs H2b split`, `docs/field-delta-2026-07-28.md §4`

## Context

Slice H2 (Apollo) as originally scoped bundled two separable wins: removing the Anthropic credential from pods, and enforcing usage (rate limits, non-forgeable counts to Argus, runaway termination). The credential-isolation half was ready long before the enforcement half's dependencies (the `apollo` Postgres schema, Argus push-event exercise from broker callers).

## Decision

Ship H2 in two passes:

- **H2a** — Apollo binary, JWT-gated `/v1/messages`, transparent Anthropic proxy, Hecate-fetched upstream credential, Iris + worker pods migrated. Usage logged to Clio for visibility only. The credential-isolation win lands here.
- **H2b** — per-project rolling-window rate limits in an `apollo` schema, non-forgeable usage events to `/argus/events`, runaway-loop termination via Argus rules. Closes Phase 2 acceptance bullet 2's cost-enforcement portion and `security.md §13`. Lands before Slice K.

Scope restatement (2026-07-28): H2b covers **external providers only**. Athena enforces local-inference budgets server-side itself (its ADR 041 — per-principal rolling windows, 429 on breach); Zakros integrates with that (read `/api/usage`, honour the 429) rather than rebuilding it.

## Rejected alternatives

- **Ship H2 whole** — holds the credential-isolation win hostage to enforcement plumbing; the two halves have different dependency chains.
- **Meter the local-Athena path in Apollo anyway** — rebuilds an enforcement the appliance already provides, and Apollo fronting local inference adds a hop with no isolation gain (the credential never leaves the trust boundary).

## Consequences

- Between H2a and H2b there is deliberately **no in-system cost enforcement** for external calls — the Anthropic console spend cap remains the outer boundary (documented in README constraints).
- H2b design must read Centaur's `iron-proxy` credential-substitution model first and record keep-or-adopt as its own ADR (`docs/priorities-2026-07-28.md P2-7`).
