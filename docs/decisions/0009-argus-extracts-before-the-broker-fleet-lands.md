# ADR 0009 — Argus extracts before the broker fleet lands

- **Status:** Accepted
- **Date:** 2026-07-29 (backfill; decided in Phase 2 planning, shipped as Slice J, `f8d5912`)
- **Deciders:** operator
- **Context source:** `docs/phase-2-plan.md §2 Argus: early extraction`

## Context

Argus (guardrail watcher) started as logic bundled inside Minos — extraction was originally a "when the broker fleet lands" item. Sequencing question: extract before or after the Phase 2 brokers (Hecate, Apollo, Hermes) ship.

## Decision

Extract Argus into its own binary (`cmd/argus`) **immediately after Slices F and G, before any Phase 2 broker lands**, with JWT-verified push-event ingest (`POST /argus/events`) from day one.

## Rejected alternatives

- **Extract after the broker fleet** — every broker built in the interim would wire against in-process Argus calls and need retrofitting to the push-event path; "wire Argus when extracted" TODOs would accumulate across three slices.
- **Never extract (keep bundled)** — a Minos crash would take the watchdog down with the thing it watches; mutual health-scraping requires separate processes.

## Consequences

- Every Phase 2 broker (Hecate, Apollo, github-broker) pushes JWT-verified events to the extracted Argus from its first commit — no retrofits happened, which was the point.
- Argus and Minos survive each other's crashes; startup reconciliation and mutual `/health` scraping exist (Phase 3 Asclepius consumes this).
- H2b's non-forgeable usage events and runaway-termination rules land on plumbing Slice J already wired.
