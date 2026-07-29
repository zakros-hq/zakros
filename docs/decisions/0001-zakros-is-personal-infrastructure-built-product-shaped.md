# ADR 0001 — Zakros is personal infrastructure, built product-shaped

- **Status:** Accepted
- **Date:** 2026-07-29
- **Deciders:** operator + agent
- **Context source:** `docs/architecture-review-2026-07-28.md §0`; operator interview 2026-07-29

## Context

Every prior review (2026-07-04 survey, 2026-07-28 field delta) implicitly treated Zakros as product-shaped infrastructure without deciding which half governs. The Centaur stress test forced the question: as a product, the market moved (Centaur ships chat-dispatch + k8s isolation + credential proxy + an SDLC loop, Apache-2.0/MIT, corporate-backed) and pace/license/narrative become load-bearing; as personal infrastructure, Centaur is a design reference and none of that applies. LICENSE choice, README ambition, roadmap pace, and how much competitive analysis matters all derive from this one decision.

## Decision

Zakros is **personal SDLC infrastructure for the operator, built product-shaped**: kept clean enough to open-source and show (public repo, real license, truthful README, CI), but carrying no product obligations — no roadmap pressure from competitors, no marketing narrative, no user support, no feature races.

Competitive findings (Centaur, OpenHands, agent-sandbox) are treated as **design references to steal from or adopt**, not threats to answer.

## Rejected alternatives

- **Product ambition** — rejected: observed delivery velocity (~1–1.5 slices/month, single operator) cannot sustain a race against corporate-backed permissive competitors, and the operator does not want the obligations.
- **Private personal infra** — rejected: the repo is already public and the product-shaped discipline (license, CI, honest docs) is cheap and keeps the open-sourcing option alive.

## Consequences

- LICENSE is required (ADR 0002) because the repo stays public.
- The README is documentation, not marketing: it must be truthful about state, and needs no differentiation narrative.
- Roadmap sequencing answers to the operator's triggers, not to competitor velocity (ADR 0003).
- Market watch items (Centaur, agent-sandbox, LiteLLM) stay on the priorities list as adoption/steal candidates only.
