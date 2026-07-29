# ADR 0006 — Apollo ships in-process providers with a long-lived service JWT for H2a

- **Status:** Accepted
- **Date:** 2026-07-29 (backfill; decided at H2a implementation time, shipped in `e435cf0`)
- **Deciders:** operator
- **Context source:** `docs/phase-2-plan.md §2 Apollo plugin isolation`, `minos/core/apollo_mint.go`

## Context

The Apollo design targets one OS subprocess per provider plugin, each holding only its own credential (matching the Hermes plugin-subprocess pattern via a shared `pkg/supervisor`). At H2a implementation time: Phase 2 ships exactly one provider (Anthropic), and `pkg/supervisor` does not exist (Slice G deferred it). Separately, Apollo needs its own identity to fetch the upstream credential from Hecate at startup — before any pod JWT exists.

## Decision

Two H2a simplifications, both with named upgrade paths:

1. **In-process `Provider` interface** with the Anthropic provider compiled in. The interface is shaped for an HTTP-RPC subprocess variant; promotion is a contained refactor. Upgrade trigger: a real second provider (different credential), or Slice K requiring the isolation.
2. **365-day Apollo service JWT** (`minosctl mint-apollo-token`) as Apollo's identity to Hecate — a single-tenant simplification instead of a rotation flow. Revisit trigger: H2b (which touches Apollo's Hecate path anyway) or Slice K's trust-boundary work, whichever lands first.

## Rejected alternatives

- **Build `pkg/supervisor` for H2a** — real complexity purchased for theoretical isolation: with one provider, the subprocess split protects one credential from itself.
- **Short-TTL Apollo token with auto-refresh** — a refresh loop and failure mode added to the broker startup path for a single-tenant deployment where the token never leaves the Minos VM. Deferred, not rejected forever; the 365-day token is the accepted interim.

## Consequences

- A second real provider cannot land without either accepting shared-process credentials or doing the subprocess promotion first — the promotion is the plan.
- The 365-day token is a standing exception to the short-TTL credential posture and is tracked here rather than only in a code comment; H2b/K must explicitly re-decide it.
- The synthetic second-provider acceptance test runs against a stub `Provider` registered at startup, exercising routing and JWT-scope enforcement (the load-bearing properties).
