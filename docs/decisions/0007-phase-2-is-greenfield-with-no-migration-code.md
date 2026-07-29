# ADR 0007 — Phase 2 is greenfield with no migration code

- **Status:** Accepted
- **Date:** 2026-07-29 (backfill; decided in Phase 2 planning, applied in every slice since)
- **Deciders:** operator
- **Context source:** `docs/phase-2-plan.md §2 Greenfield posture`

## Context

Phase 2 replaces several Phase 1 mechanisms wholesale: HMAC bearers → Ed25519 JWTs, `AdminIdentity` scalar → identity registry, `ProjectConfig` singleton → project registry, Minos-push credentials → Hecate pull. A conventional rollout would build dual-path cutovers, feature flags, and drain windows for each.

## Decision

**No migration code.** There is no production deployment with in-flight state to preserve — the deployment is rebuilt from `deploy/rebuild.sh` at slice boundaries. When a Phase 1 mechanism is replaced, the old code path is deleted in the same slice. No dual-path cutover, no feature flags, no backward-compatibility shims.

## Rejected alternatives

- **Dual-path cutovers with flags** — pays an ongoing complexity tax to protect state that does not exist. Every flag is a combination CI doesn't test.
- **Versioned upgrade paths** — right answer the day a second deployment exists that can't be rebuilt; wrong answer before then.

## Consequences

- Slices stay small and deletions are real (the PAT path, HMAC paths, and singleton config are gone, not flagged off).
- The posture is load-bearing and explicit: **if a deployment ever acquires state that must survive upgrades, this ADR gets superseded first**, then slices grow migration steps.
- Deploy scripts are the compatibility story — they must always produce a working system from scratch (see `LESSONS.md` on end-to-end verification of them).
