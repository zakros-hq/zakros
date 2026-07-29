# ADR 0005 — Hecate adopts OpenBao behind vault-mcp-server

- **Status:** Accepted
- **Date:** 2026-07-29 (backfill; decided in Phase 2 planning, shipped as Slice H1, `b60ba18`)
- **Deciders:** operator
- **Context source:** `docs/phase-2-plan.md §2`, `docs/build-vs-adopt.md §hecate`

## Context

Hecate (credentials broker) needs a secrets backend whose authorization model composes with Zakros JWT scopes — one `credentials.fetch:<ref>` scope must map to exactly one backend policy. Candidates: build a custom store, HashiCorp Vault (BSL 1.1 since 2023), or OpenBao (the LF-governed MPL-2.0 fork, API-compatible with Vault 1.14).

## Decision

Adopt **OpenBao** as the backend, fronted by `hashicorp/vault-mcp-server` as a Hecate-supervised subprocess. OpenBao runs in its own Proxmox LXC with Raft storage; per-credential Vault policies are managed declaratively (`deploy/openbao/policies/`, one file per fetch scope). Hecate validates caller JWTs, maps scopes to policies, mints short-lived policy-bound tokens, and proxies fetches.

## Rejected alternatives

- **HashiCorp Vault OSS** — BSL 1.1 licensing risk for a public Apache-2.0 project; otherwise equivalent. Remains the documented fallback if OpenBao governance stalls (the Hecate abstraction makes the swap a service-level change).
- **Custom credential store** — re-implements policy engines, audit, and seal/unseal that OpenBao ships; the highest-value secret store is the worst place for novel code.
- **Infisical as production backend** — already integrated for local dev, but per-credential policy granularity mapping to JWT scopes is the load-bearing requirement and Vault-style policies fit it directly.

## Consequences

- One more service to operate (OpenBao LXC, unseal procedure, bootstrap scripts — see `LESSONS.md` on the bootstrap fixes).
- Per-credential ACLs are declarative files in the repo; adding a credential means adding a policy file plus a seed entry.
- Watch item: OpenBao release cadence through Phase 2; fallback documented above.
