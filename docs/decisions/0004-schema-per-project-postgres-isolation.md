# ADR 0004 — Schema-per-project Postgres isolation

- **Status:** Accepted
- **Date:** 2026-07-29 (backfill; decided in Phase 2 planning, shipped with Slice G)
- **Deciders:** operator
- **Context source:** `docs/phase-2-plan.md §2 Schema-per-project`

## Context

Phase 2 introduces multi-project support, forcing the question of how projects are isolated in the shared Postgres instance. The Mnemosyne storage decision already committed to one Postgres ("one DB, one backup story"); the isolation mechanism inside it was open: row-level predicates, database-per-project, or schema-per-project.

## Decision

**Schema-per-project**: `minos_<project_id>`, `mnemosyne_<project_id>`, etc. Every Postgres-touching component resolves `project_id` → schema at connection time (`SET search_path` on checkout). The cross-project `minos` schema (audit log, identity registry, project registry) stays singleton. Provisioning creates the schemas on project registration; `golang-migrate` applies per-project migrations.

## Rejected alternatives

- **Row-level predicates (`WHERE project_id = …`)** — isolation is only as strong as every query being written correctly; one missed predicate is a cross-project leak. Structural beats disciplined.
- **Database-per-project** — strongest isolation but breaks the one-DB backup story and multiplies connection pools and migration surfaces for a homelab-scale deployment.

## Consequences

- Blast-radius isolation between projects is structural; Mnemosyne embedding indexes partition along the trust boundary; backup/restore can operate per-project.
- Every new Postgres-touching component must take `project_id` at connection time — no schema-unaware data access.
- Single-project deployments are the degenerate case (one schema set), which is how the system runs today.
