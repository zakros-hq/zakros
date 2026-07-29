# Zakros — Reconciled priorities (2026-07-28)

**This list supersedes** the action lists in `project-review-2026-07.md §5` (July 4) and `field-delta-2026-07-28.md §6`. Both stay versioned as evidence; neither is the working list anymore. Every item below was re-verified against the tree at `e435cf0` (H2a committed) on 2026-07-28.

---

## What changed today (verification evidence, not claims)

- `go build ./... && go vet ./... && go test ./...` ran green over the full tree — **the first compiler check H2a ever had**. Nothing broke; no fixes were needed.
- Slice H2a committed as `e435cf0` (28 files, +1,614/−139) and the three review docs committed as `0a7caef`. Zero uncommitted Go files remain.
- Gitignore gaps closed in the H2a commit: `deploy/apollo.json`, `.infisical.json`, `/apollo`.
- **Secrets audit ran for the first time against the publishable tree**: gitleaks over all 82 commits (including the unpushed ones) — clean. Tree scan found 6 hits, all inside the gitignored, operator-local `deploy/secrets.json`; nothing in the publish path.

## Prior recommendations: done / stale / wrong

| Prior item | Verdict | Evidence |
|---|---|---|
| July 4 P0-1 "Reinstall Go toolchain" | **Done before session** | go1.26.5 at `/opt/homebrew/bin/go` |
| July 4 P0-2 / field-delta P0 "Land H2a" | **Done** | `e435cf0`, build/vet/test green |
| July 4 P0-3 "Close gitignore gaps" | **Done** | in `e435cf0` |
| July 4 P0-4 "Commit the handoff + report docs" | **Done** | `0a7caef` |
| "No secrets audit has ever run" (session open thread) | **Done today** | gitleaks history + tree scan above |
| Field-delta P2-7 / "README still claims uniqueness" | **Wrong** | `README.md` (160 lines) contains no uniqueness/"nowhere else" claim — grep for unique/nowhere/nothing-else/no-other/combination finds nothing. The claim lived in `project-review-2026-07.md §2`, which `field-delta §1` already corrects. No README repositioning edit is needed; the README's *other* staleness is real (see P1-4). |
| Field-delta P1-2 "Flip Iris to Athena = base-URL + bearer swap on Iris" | **Stale mechanism** | Written before H2a landed. Iris now posts to Apollo (`ZAKROS_APOLLO_URL/v1/messages`), and Apollo's upstream is config (`apollo.json anthropic_endpoint`). The right seam for an Athena flip is now **Apollo's provider config** (or a second provider entry), keeping usage under Apollo's audit path — not a direct Iris→Athena URL. Also an operator model-quality call, not purely mechanical. |
| Field-delta P1-1 "Purge Ollama fiction — 8 locations" | **Undercount** | grep finds ~25 live locations: the 8 listed plus ~17 more in `architecture.md` (port tables, capability rows, egress matrices describing "Athena Ollama" reaches) and `build-vs-adopt.md`. Still one doc pass, but it's an architecture.md sweep, not 8 line edits. |
| July 4 P2-18 "Rename daedalus → zakros" | **Done before session** | remote is `zakros-hq/zakros`; review already annotated |
| Field-delta demotion of LiteLLM Agent Platform to watch-only | **Stands** | no re-check needed until H2b design review |

Everything else in both lists was re-verified as still true: `CLAUDE.md`, `docs/decisions/`, `LESSONS.md`, `.github/` all absent; README status line still says "Phase 1 functional; Phase 2 planning underway" while five Phase 2 slices are committed; `phase-2-plan.md §15`'s CI claim was false (fixed today to say so).

---

## P0 — before anything else

1. **Pick and commit a LICENSE** — operator decision, blocking in practice: `zakros-hq/zakros` is PUBLIC with `license: null`, meaning default copyright — nobody can legally use, copy, or contribute to what is already published. Recommendation: Apache-2.0 (patent grant, matches Centaur/agent-sandbox ecosystem norms); MIT if minimalism wins. One file + one commit once chosen.
2. **Crete acceptance smoke for H2a** — build/vet/test green is not the slice's acceptance checkpoint. Deploy Apollo on the real Crete deployment (`deploy/rebuild.sh` path), verify: Iris answers through Apollo, a worker pod routes through Apollo, no pod holds the Anthropic credential. Until this passes, H2a is committed but not *done* by the plan's own rule ("no slice lands without its acceptance checkpoint passing on the real Crete deployment").

## P1 — standards debt + cheap corrections (an afternoon, mostly mechanical)

3. **Stand up CI** — GitHub Actions running the existing Make targets (vet, lint, test, build). The Makefile already encodes everything; ~30 minutes. This session proved the cost of not having it: a full slice sat unbuilt for three months. (`phase-2-plan.md §15` no longer claims CI exists.)
4. **README refresh** — status line ("Phase 1 functional; Phase 2 planning underway" → five slices committed), Known-Phase-1-constraints list (HMAC bearers, PAT, file-backed secrets are all replaced by F/H1/H2a), repo-layout section (missing `cmd/argus`, `cmd/github-broker`, `cmd/hecate`, `cmd/apollo`).
5. **Purge the Ollama fiction** (~25 locations, mostly `architecture.md`) — Athena is a Swift/MLX daemon serving `/v1/messages` (Anthropic dialect), no Ollama anywhere. Same pass: rewrite `architecture.md:711`'s budget-forgeability premise (Athena ADR 041 enforces budgets server-side).
6. **Author `CLAUDE.md`, create `docs/decisions/` + backfill ADRs, seed `LESSONS.md`** — all three still absent; Athena is the in-house proven template. Priority ADRs: schema-per-project, Hecate=OpenBao, H2a in-process deviation + 365-day Apollo token, greenfield no-migration posture, H2a/H2b split. This session adds a lesson for `LESSONS.md`: *uncommitted work and unbuilt code are indistinguishable from broken code — land slices when green, and CI would have caught it three months earlier.*

## P2 — next build work (ordered)

7. **H2b (Apollo enforcement)** — rate limits, non-forgeable usage → Argus, runaway termination. Scope as restated in `phase-2-plan.md` slice-status note: external providers only; integrate with Athena's ADR-041 budgets for the local path, don't rebuild them. **Before hardening the design, read Centaur's `iron-proxy`** credential-substitution model and record keep-or-adopt as an ADR. Partially done 2026-07-28 (`field-delta-2026-07-28.md` addendum): verdict is steal the wire-substitution *mechanism*, keep Zakros's per-task *scoping* — Centaur's vault is deployment-scoped by its own admission. The ADR still needs writing when H2b starts. Same addendum: Centaur's `githubbot` (landed July 7–20) now ships an issue→PR→review→CI-fix→auto-merge loop — the Momus comparison and the narrative framing in P3-12 both tightened.
8. **agent-sandbox spike alongside H2b** (pulled forward from Phase 3 per field-delta §2) — half-day, kill criteria: does `SandboxClaim` express the per-task envelope, does `SandboxWarmPool` survive the egress-allowlist requirement. Pin ≥ v0.5.2 (warm-claim race in v0.5.0/v0.5.1). Adoption deletes pod-lifecycle code.
9. **Iris → Athena backend decision** — now an Apollo-config/provider question (see verdict table), plus an operator call on local-model quality for Iris's conversational load. Decide when touching Apollo config in H2b; record as ADR either way.
10. **Forgejo spike** (handoff Phase 1) — independent of all slices, can run any time; `forge-broker` itself waits for Slice I + shared broker-bootstrap extraction (don't stamp the 5th copy of the ~30-line main.go bootstrap).
11. **Slice I → K → L1**, per plan; within L2–L5 prioritize Momus (PR-review agents are table stakes).

## P3 — watch / opportunistic

12. Narrative reposition where it *does* live (planning docs, any future public-facing text): the defensible position is hypervisor blast radius + subscription economics + episodic memory + in-boundary inference appliance — not "nothing else does this."
13. Watch list unchanged: OpenHands, mem0 (pass holds), Claude Code Channels (read before more Hermes surfaces), LiteLLM Agent Platform (watch only, stalling).
14. Mnemosyne fact-curation UX (Devin Knowledge/Playbooks shape) when Mnemosyne maturity work comes up.

---

*Re-verify against the tree before trusting any line of this after the next slice lands. The prior two lists are historical record only.*
