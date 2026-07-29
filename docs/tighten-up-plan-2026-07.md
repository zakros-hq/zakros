# Zakros tighten-up — change plan (2026-07-29)

Remediation plan per the brownfield gate. Inputs: `architecture-review-2026-07-28.md`, `priorities-2026-07-28.md`, ADRs 0001–0003 (identity, license, Phase 2 tail). Crete is offline pending the homelab remodel, so nothing here requires the live deployment; the H2a acceptance smoke stays blocked and is explicitly out of scope.

**Approval state: PENDING operator review. No implementation commit lands until approved.**

---

## Scope

**In:** LICENSE; CI; README truth pass; Ollama/doc-drift purge; CLAUDE.md + LESSONS.md; ADR backfill (top inline decisions); roadmap/phase-2-plan amendment per ADR 0003; tests for the two deployed-but-untested brokers.

**Out:** any committed-core slice work (H2b, K, L1, L2 — separate sessions); the H2a Crete smoke (blocked on hardware); broker-bootstrap extraction (deferred to just-before-forge-broker per July 4 review — stamping it now serves nothing); Mnemosyne curation, agent-sandbox spike, Centaur/iron-proxy ADR (all H2b-adjacent).

## Commits (small, ordered, each independently green)

Each commit runs `go build ./... && go vet ./... && go test ./...` before landing; from commit 2 onward, CI enforces the same remotely.

| # | Commit | Contents | Definition of done (discriminating) |
|---|---|---|---|
| 1 | `legal: Apache-2.0 license + ADRs 0001-0003` | `LICENSE` (standard Apache-2.0 text), `docs/decisions/` with the three ADRs, this plan doc | `gh repo view` reports `licenseInfo: Apache-2.0` after push (before: `null`) |
| 2 | `ci: vet/build/test/lint workflow` | `.github/workflows/ci.yml` running the existing Make targets (`vet`, `build`, `test`; `lint` if golangci-lint config is trivial, else vet-only first pass) on push + PR. Include a docs-lint step: `grep -r "Athena Ollama" docs/ agents/ deploy/` must return nothing (added *after* commit 4 lands, else it fails — see sequencing note) | First green Actions run on a pushed commit (before: `.github/` does not exist — any run at all is new evidence) |
| 3 | `docs: README truth pass` | Status section (five Phase 2 slices committed, Crete offline noted), Known-Phase-1-constraints list corrected (HMAC/PAT/file-secrets → replaced by F/H1/H2a), repo layout (+`cmd/argus`, `cmd/github-broker`, `cmd/hecate`, `cmd/apollo`), tone per ADR 0001 (documentation, not marketing) | `grep -E "HMAC bearer|GitHub PAT for worker push" README.md` returns nothing (before: both present); layout section names all six binaries |
| 4 | `docs: purge the Ollama fiction` | ~25 locations: `architecture.md` (port table, Iris/Themis/Momus/Calliope/Prometheus backend rows, egress matrices, §7 budget-forgeability premise → Athena ADR-041 server-side budgets), `deploy/README.md:420`, `iris-deployment.yaml:5`, `agents/iris/Dockerfile:4`, `main.go:8`, `conversation.go:4`, `roadmap.md:63`, `build-vs-adopt.md` Athena-MCP section gets a dated correction note. Replace with "Athena `/v1/messages` (Anthropic dialect)" framing | `grep -ri "ollama" --include="*.md" --include="*.go" --include="*.yaml" --include="Dockerfile*" . | grep -v decisions/ | grep -v review` returns only historical-review hits (before: ~25 live-doc hits). CI docs-lint step activates here |
| 5 | `plan: re-derive Phase 2 tail (ADR 0003)` | `roadmap.md §Phase 2`: acceptance-gate bullets for L3–L5/M features annotated as trigger-attached options; `phase-2-plan.md`: committed core H2b→K→L1→L2, options section with named triggers, Crete-offline note on all acceptance checkpoints, Charon-reshape note | Slice-status table shows committed core + options split; roadmap and plan agree (before: 9-slice inherited queue) |
| 6 | `standards: CLAUDE.md + LESSONS.md` | Repo-root `CLAUDE.md`: canonical pipelines (Minos is the only dispatcher; Hermes the only surface I/O path; Apollo the only external-LLM egress; envelope schema location), retrieval table, ADR back-links, deploy-runbook pointer, "Crete offline" operational note. `LESSONS.md` seeded: the H2a three-month-unbuilt lesson, the two openbao bootstrap fixes (`661c65a`, `ec3f4c9`), the Ollama doc-drift lesson, the Centaur missed-then-overcorrected lesson | Files exist with content a fresh session can act on; CLAUDE.md links all ADRs (before: none of the three house artifacts existed) |
| 7 | `adr: backfill inline decisions` | Highest-value ~6 from the planning docs: schema-per-project; Hecate=OpenBao+vault-mcp-server; H2a in-process provider deviation + 365-day Apollo token (with H2b/K revisit condition); greenfield no-migration posture; H2a/H2b split; Argus early extraction. Each links back to the planning-doc section it lifts from | `docs/decisions/` has 0004–0009; the 365-day-token exception is ADR-recorded, closing the July 4 finding (before: all six live only inline) |
| 8 | `test: cmd/argus + cmd/github-broker` | Server-level tests mirroring `cmd/apollo/server_test.go` / `cmd/hecate` shape: JWT rejection (bad signature, wrong `aud`, missing scope), happy-path handler, config-load failure. No new frameworks, table-driven, stdlib + existing test helpers only | `go test ./cmd/argus ./cmd/github-broker` shows real tests passing (before: `[no test files]` on two deployed brokers); a deliberately broken verifier fails the new tests |

Sequencing notes: 2 before 3–8 so every later commit gets CI coverage; the CI docs-lint step is added in commit 4 (with the purge) to avoid a red window. Commits 3–5 are pure docs and can collapse into fewer commits if review prefers; kept separate so each diff stays reviewable.

## Test bar

- Commits 1–7: full local suite green (docs commits can't break it, but the bar stays uniform); CI green from commit 2 on.
- Commit 8 is the only code commit: new tests must fail against a deliberately broken build (verified once during development) and pass against the real one — the dod-verify discipline, evidenced in the commit message.

## Rollout

No deployment surface — Crete is offline; everything lands on `main` via push (operator pre-authorized pushes for this effort at approval time, or per-commit if preferred). Remaining after this plan, in order, when work resumes: H2b (with Centaur/iron-proxy ADR + agent-sandbox spike alongside) → K → L1 → L2; H2a Crete smoke as soon as the homelab remodel completes.

## Estimate

Commits 1–6: one session. Commit 7: half a session (ADR prose). Commit 8: half a session (two test files). Total ≈ 2 sessions.
