# ADR 0003 — Phase 2 tail is re-derived from roadmap triggers

- **Status:** Accepted
- **Date:** 2026-07-29
- **Deciders:** operator + agent
- **Context source:** `docs/architecture-review-2026-07-28.md §5–6`; operator interview 2026-07-29

## Context

`phase-2-plan.md` carries ~9 remaining slices as a committed queue (H2b, I, K, L1–L5, M) — 6–9 months at observed velocity. But `roadmap.md §Phase 2` defines explicit **trigger conditions** for the phase's scope (second surface needed, second operator, second provider, injection resistance needed, review volume beyond one human, backlog coordination load-bearing), and at most one has fired: injection resistance, because the repo is now public. The queue was being inherited, not derived. Meanwhile Crete is offline pending the homelab remodel, so no slice can pass its acceptance checkpoint on real hardware regardless of code state.

## Decision

Split the Phase 2 tail into a **committed core** and a **trigger-attached options list**:

- **Committed core, in order: H2b → K → L1 → L2.** Cost enforcement (H2b) and injection defenses (K) answer fired or near-fired triggers; L1 (Themis) and L2 (Momus) are the smallest set that makes Zakros its own first customer (Momus reviews Zakros PRs).
- **Options list (build when the named trigger fires):** L3 Calliope, L4 Prometheus, L5 Hephaestus (their task-type demand), the Slack-plugin half of Slice I (a second surface actually needed), Slice M's admin UI and break-glass minting (a second operator or identity-registry growth). The Hermes-extraction half of I may be pulled forward independently when subprocess credential isolation matters.
- **Charon is not built as designed**: the H2b-time ADR on Centaur's iron-proxy substitution model reshapes it first.
- Acceptance checkpoints requiring Crete are **blocked, not waived** — slices reaching code-complete hold at "committed, not accepted" until Crete returns.

## Rejected alternatives

- **Keep the full committed queue** — rejected: sequencing derived from a product race that ADR 0001 opted out of; commits ~6–9 months against triggers that haven't fired.
- **Cut deeper (H2b + K only)** — rejected: leaves the dogfood loop (Momus on Zakros PRs) uncommitted, which is the highest-leverage verification improvement available.

## Consequences

- `roadmap.md §Phase 2` (authoritative for phase scope) and `phase-2-plan.md` are amended to match in the tighten-up; the Phase 2 acceptance gate bullets tied to L3–L5 move with their slices to the options list.
- De-committed slices are not deleted: their plan sections remain, marked as options with named triggers.
- Re-adding an option to the committed core requires naming the fired trigger — one line in the plan doc, not a new ADR.
