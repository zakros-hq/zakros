# Zakros — agent instructions

Zakros is **personal SDLC infrastructure, built product-shaped** ([ADR 0001](docs/decisions/0001-zakros-is-personal-infrastructure-built-product-shaped.md)). Apache-2.0, public repo. Single operator, homelab deployment on Crete (Proxmox). Go monorepo, module `github.com/zakros-hq/zakros`.

## Canonical pipelines (binding — a parallel implementation is a defect)

- **Minos is the only dispatcher.** All task commissioning flows through Minos's commissioning API; nothing else creates worker pods.
- **Hermes is the only surface I/O path.** All human-facing chat traffic (inbound commands, thread posts) goes through Hermes and its per-surface plugins; pods never call a chat surface directly.
- **Apollo is the only external-LLM egress.** Since Slice H2a, every pod's Anthropic traffic routes through Apollo; no pod holds a provider credential.
- **Hecate is the only credential fetch path** (production). Consumers pull from Hecate (OpenBao-backed) on JWT-authenticated fetch; file/Infisical providers are local-dev only.
- **Cerberus verifies every inbound webhook** (library inside Minos; pluggable verifiers).
- **Task envelopes** are the unit of work; schema in `schemas/`. An agent's reach is defined by its task type, not by what it can run.

## Current state (2026-07-29)

- Phase 1 shipped and verified. Phase 2 slices F, G, J, H1, H2a committed. **Committed core remaining: H2b → K → L1 → L2** ([ADR 0003](docs/decisions/0003-phase-2-tail-is-re-derived-from-roadmap-triggers.md)); everything else Phase 2 is a trigger-attached option.
- **Crete is offline (homelab remodel).** No acceptance smokes can run; slices hold at "committed, not accepted." Do not claim a slice done without its Crete checkpoint.
- CI: `.github/workflows/ci.yml` (vet/build/test + docs-lint). Local: `make vet build test`, or `go build ./... && go vet ./... && go test ./...`.

## Retrieval

| Question | Source |
|---|---|
| What ships in which phase? | `docs/roadmap.md` (authoritative), then `docs/phase-2-plan.md` (slice sequencing + status table) |
| Why was X decided? | `docs/decisions/` (ADRs), then planning docs' Structural Decisions sections |
| Full target architecture | `docs/architecture.md` (design across all phases — roadmap wins on what actually ships) |
| Threat model, accepted risks | `docs/security.md` (every Phase 1 exception is stated explicitly) |
| Build vs adopt calls | `docs/build-vs-adopt.md` |
| Deploy/operate | `deploy/README.md` runbook; `deploy/rebuild.sh` for teardown-rebuild |
| Current priorities | `docs/priorities-2026-07-28.md` + `docs/tighten-up-plan-2026-07.md` |
| Operational lessons | `LESSONS.md` |

## Rules

- **Docs-as-authority**: when implementation diverges from a doc, update the doc in the same change. The Ollama fiction (25 stale references, caught 2026-07-28) is the cautionary tale; CI's docs-lint now guards the known phrases.
- **Slice discipline**: land slices when green — never let work sit uncommitted (H2a sat unbuilt for three months; see `LESSONS.md`).
- **Decisions become ADRs** in `docs/decisions/` (house format via the `adr` skill), linked from this file when they bind future work.
- **Athena facts**: Athena is a Swift/MLX daemon on `:7447` serving `/v1/messages` (Anthropic dialect) with bearer RBAC and server-side per-principal budgets. There is no Ollama. Verify against its `/openapi.json`, not old docs.
- Human approval is the product position: PR merge is the gate; high-blast operations get confirmation tokens (Slice K). Do not add auto-merge paths.

## ADR index

- [0001 — personal infrastructure, product-shaped](docs/decisions/0001-zakros-is-personal-infrastructure-built-product-shaped.md)
- [0002 — Apache-2.0](docs/decisions/0002-license-the-public-repo-apache-2-0.md)
- [0003 — Phase 2 tail re-derived from triggers](docs/decisions/0003-phase-2-tail-is-re-derived-from-roadmap-triggers.md)
