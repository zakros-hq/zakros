# Project Zakros — State, Market, and Standards Review (2026-07-04)

Point-in-time actionable report. Inputs: repo audit of `main` @ `ec3f4c9` + working tree, `docs/roadmap.md`, `docs/phase-2-plan.md`, `docs/forge-stack-handoff.md`, and a July-2026 web survey of the commercial/OSS coding-agent landscape (sources inline; unverified claims flagged).

---

## TL;DR

1. **Where the project is:** Phase 1 shipped and verified; Phase 2 is roughly 40% done (Slices F, G, J, H1 committed; H2a sitting **uncommitted for ~2 months** in the working tree, never compiler-checked because the Go toolchain is missing from this workstation). Remaining: H2b, I, K, L1–L5, M.
2. **Market position:** Zakros's exact combination — self-hosted control plane + k8s pod-per-task isolation + chat dispatch + scoped-credential broker + subscription-OAuth economics — **exists nowhere else as one product** (verified across ~30 products). The 2026 consolidation graveyard (Terragon, Codegen, Bloop, Crystal all dead or absorbed) validates the self-hosting bet. The two competitors moving toward this space: **OpenHands** (repositioned as a "self-hosted developer control center" running Claude Code/Codex as workers) and **kubernetes-sigs/agent-sandbox** (upstream CRDs for exactly Zakros's pod-isolation layer).
3. **Standards:** the three house-standard artifacts (repo `CLAUDE.md`, `docs/decisions/` ADRs, `LESSONS.md`) are **all absent**, and `phase-2-plan.md §15` describes a CI pipeline that **has never existed**. ~16 architectural decisions live inline in planning docs where ADRs should be.
4. **Forge handoff:** the Forgejo work slots cleanly as a parallel track alongside the remaining Phase 2 slices; its `forge-broker` should wait for a shared broker-bootstrap extraction first (the ~30-line main.go bootstrap is already copy-pasted 4×; forge-broker would be the 5th).

The prioritized action list is at the bottom.

---

## 1. Current state

### Delivery position

| Milestone | Status |
|---|---|
| Phase 1 (OpenClaw replacement MVP) | **Shipped & verified** end-to-end on real Proxmox (per README + deploy runbook) |
| Slice 0 (Iris closes Phase 1 gate) | Shipped (Iris pod exists; Claude-backed interim per plan) |
| Slice F (Ed25519 JWT + github-broker) | Committed (`80dd241`) |
| Slice G (identity + project registries) | Committed (`aa401e4`) |
| Slice J (Argus extraction + Cerberus verifiers) | Committed (`f8d5912`) |
| Slice H1 (Hecate on OpenBao) | Committed (`b60ba18` + fixes) |
| Slice H2a (Apollo broker, transparent proxy) | **In working tree, uncommitted** — 17 modified files +430/−139, plus new `cmd/apollo/` (~1,010 LOC incl. tests), `minos/core/apollo_mint.go`, deploy plumbing |
| H2b, I, K, L1–L5, M | Not started |

### Risks in the current tree

- **HEAD is 2026-04-27; the H2a work is ~2 months of uncommitted drift.** One `git checkout .` or disk failure loses it. It also spans Iris auth, dispatch builder, minosctl, and the whole deploy path — this is a slice, not a scratch change.
- **No Go toolchain on this workstation** (`go` not found; Docker daemon down). The uncommitted Apollo code has never been built or tested here. `~/go/bin` artifacts imply Go was previously installed and lost.
- `minos/core/apollo_mint.go` mints a **365-day service JWT** for Apollo (self-documented H2a single-tenant simplification). Fine for now; it should be an ADR-recorded exception with an H2b/K revisit, not a code comment.
- Gitignore pattern break: `deploy/{config,secrets,github-broker,argus,hecate}.json` are ignored but **`deploy/apollo.json` and `.infisical.json` are not** — both untracked today, both would be swept in by `git add deploy`/`git add .`. apollo.json is refs-only today; the gap is foot-gun-shaped, not a leak.
- Secrets hygiene otherwise **clean**: gitleaks over all 82 commits found nothing; tfstate/tfvars/secrets JSONs never committed. (The standing memory note claiming checked-in tfstate was stale and has been corrected.)

### Repo health numbers

- 18,733 Go LOC; 83 source files / 41 test files. Zero TODO/FIXME markers. 9 direct deps, no replaces, go 1.25 — lean.
- **21 packages have zero tests.** Notable: `cmd/argus` and `cmd/github-broker` are deployed brokers with no tests (while `cmd/hecate` and `cmd/apollo` both have server tests); `minos/project` (all three sub-packages), `pkg/envelope`, `pkg/audit`, `pkg/provider` untested.
- **No CI has ever existed** — no `.github/` in the tree or history. `phase-2-plan.md §15` describes a "Phase 1 workflow (go vet, go test, golangci-lint, go build, Dockerfiles)" that was never built. The command set exists only as local Make targets, on a machine that currently has no Go.
- README staleness: "Status: Phase 1 functional; Phase 2 planning underway" (four slices are committed); the constraints list still describes HMAC bearers and file-backed secrets as current (replaced by F and H1); repo layout omits `cmd/argus`, `cmd/github-broker`, `cmd/hecate`, `cmd/apollo`.
- ~~Identity drift: git remote is `github.com/GoodOlClint/daedalus.git`, ... local dir is `Project-Daedalus`.~~ **Resolved 2026-07-28** — remote is now `github.com/zakros-hq/zakros.git`, local dir `~/Source/Zakros`; OKF bundle, code-intelligence unit, and Claude memory all re-keyed to `Zakros`. ("Daedalus" as the worker pod-class name is intentional and myth-consistent; it stays.)
- Mild structural debt: the ~30-line broker main.go bootstrap (flag → loadConfig → file secrets provider → resolve/parse signing pubkey → Verifier) is copy-pasted across all four broker binaries with near-identical Config structs. forge-broker would make five.

---

## 2. Commercial landscape (July 2026)

### Comparison table

| Product | Hosting | Isolation | Dispatch | Cross-run memory | Credential model | Cost model |
|---|---|---|---|---|---|---|
| **Zakros** | Fully self-hosted | Hypervisor + k8s pod-per-task + egress allowlists | Discord chat, webhooks | pgvector run records, injected at spawn | Broker-minted scoped short-TTL tokens (OpenBao) | Claude subscription OAuth, $0 marginal |
| GitHub Copilot coding agent | SaaS only (Actions sandbox) | Ephemeral runner + egress firewall (gaps: MCP, setup steps) | Assign issue, Agents panel, mobile | Copilot Memory (preview) + instruction files | Repo-scoped token, draft-PR-only | **Metered AI Credits since 2026-06** |
| OpenAI Codex cloud | SaaS (CLI is OSS) | Container, agent-phase offline by default | Web, CLI, `@codex` | AGENTS.md + cached envs; no episodic memory | GitHub app, setup-phase-only secrets | ChatGPT plans, 5-h windows + credit overage |
| Google Jules | SaaS only | VM per task; egress undocumented | Web, CLI, API, issue labels | Explicit per-repo memory + AGENTS.md | GitHub OAuth, coarse | Google AI plans; paid tiers **gmail-only** |
| Claude Code on the web | SaaS (Action runs on your runners) | VM + security proxy; **git token never enters sandbox**; push restricted to working branch | Web, CLI `--cloud`, mobile, `@claude` | CLAUDE.md + env snapshots; no episodic memory | GitHub App or gh-token sync + credential-translating proxy | Pro/Max rate limits, no VM charge (Action path is API-metered) |
| Cursor cloud agents | SaaS only | VM per agent | Editor, web, Slack, Linear, API, Automations | Rules + Automations Memories | Admin-connected repo r/w, workspace secrets | Pro+; per-token Max Mode |
| Devin (Cognition) | SaaS; Enterprise VPC option | VM per session, snapshots | Web, Slack, API, Linear/Jira | **Knowledge + Playbooks + auto Wiki** (best-in-class) | GitHub app, secrets manager, teamspaces | $20/$200/Teams; ACU→quota migration in flux |
| Factory.ai Droids | Hybrid — SaaS, **BYO machine**, on-prem enterprise | Remote sandboxes; local CLI is agent-enforced only | Web, CLI, Slack, Linear, Jira | Org/team/repo-scoped memory | BYOK, ZDR, audit logs | $20/$100/$200 + credits |
| OpenHands | OSS local + SaaS + **self-hosted Helm (Polyform, paid past 30d)** | Docker/k8s pod runtimes | Web, webhooks, schedules, Slack, GitHub/Linear | Condenser + microagents | BYOK, per-user git tokens, secrets sub-chart | OSS free; enterprise licensed |
| Kube Foundry | OSS k8s operator | **Pod-per-task** (`SoftwareTask` CRD) | CRD/REST only — no chat | None found | Not documented (young project, unverified) | Free |
| GitLab Duo Agent Platform | SaaS **and self-managed incl. air-gapped + BYO models** | CI-integrated flows | Issue → MR flows, chat | Project context | GitLab RBAC/SAML | Premium/Ultimate licenses |

Key sources: GitHub docs/blog (Copilot billing change 2026-06-01, firewall caveats), developers.openai.com/codex, jules.google/docs, code.claude.com docs (fetched 2026-07-04), cursor.com/docs/cloud-agent, devin.ai/pricing, factory.ai + docs, github.com/OpenHands/OpenHands (79.4k★), Kube Foundry via HuggingFace blog 2026-04, GitLab GA press release 2026-01-15. Flagged-unverified items (Copilot Max credit allotment, SpaceX/Anysphere acquisition rumor, terragon-oss license, Kube Foundry maturity) are excluded from conclusions.

### What Zakros does that nothing else does

- **The full combination.** No verified product ships self-hosted control plane + k8s pod-per-task + chat dispatch + webhook-driven state + scoped credential broker. Kube Foundry has the pod-per-task operator but no chat/webhook/credential story; OpenHands has the control-center but Docker/VM-grade isolation and a source-available (not OSS) enterprise plane; GitLab Duo has the self-hosted issue→MR loop but is a monolithic forge platform, not a broker fleet.
- **Credential architecture is ahead of the market.** Broker-minted, scoped, short-TTL tokens with per-credential OpenBao policies exceed every surveyed product except Claude Code's cloud proxy (which is the same idea, SaaS-side). Copilot's firewall explicitly doesn't cover MCP servers; Jules doesn't document egress at all.
- **Subscription-OAuth economics.** Every SaaS competitor is metered or quota'd (Copilot went usage-metered June 2026; Cursor is per-token in Max Mode). Zakros fanning the operator's Claude subscription into pods at $0 marginal cost is unique — and is also the piece with ToS ambiguity (noted already in `build-vs-adopt.md §apollo`).
- **Hypervisor-level blast-radius boundary.** Nobody else puts the control plane and the workers on different VMs under an operator-controlled firewall.

### What the market has that Zakros lacks

- **Episodic product memory UX:** Devin's Knowledge/Playbooks/auto-Wiki is the benchmark; Mnemosyne has the substrate (run records + pgvector) but no curation loop (pin/edit/approve facts). Worth stealing the *shape* when the Phase 2 Mnemosyne maturity work lands.
- **Parallel-fleet UX:** every competitor has a dashboard/agents panel; Zakros has Discord threads + `minosctl`. Slice M's minimal admin UI is the answer; the market confirms it matters.
- **PR-review agents are table stakes now** (Copilot, Codex `@codex`, CodeRabbit, Claude Code review). Momus (L2) is validated, arguably late — it's the highest-leverage remaining pod class.
- **In-repo agent config conventions:** AGENTS.md / CLAUDE.md / `.cursor/rules` are ecosystem-standard; Zakros task envelopes don't yet read repo-committed agent instructions.
- **IDE integration, enterprise SSO** — explicitly out of scope for a single-operator homelab; no action.

### Watch list (could replace Zakros parts)

- **kubernetes-sigs/agent-sandbox** (Sandbox/SandboxTemplate CRDs, k8s blog 2026-03-20; GKE builds on it with gVisor). Today Zakros's dispatcher builds raw pods; when this matures, adopting the CRD could replace custom pod-lifecycle code and add gVisor-class isolation. Re-evaluate at Phase 3.
- **Claude Code Channels** (official Discord/Telegram/webhook bridge, code.claude.com/docs/en/channels-reference). Overlaps Hermes's Discord intake for the *dispatch* half — no isolation, no control plane, but worth reading before building more Hermes surface plugins, if only to match its UX conventions.
- **OpenHands** — the strategic competitor. If it gains real k8s pod-per-task isolation and a credential broker, it becomes "Zakros with a company behind it." Its Polyform-licensed enterprise plane is the moat gap Zakros exploits.
- **mem0** — the only credible OTS Mnemosyne replacement (Apache-2.0, pgvector-native). Still contradicts the run-record-primary decision (extraction-pipeline-primary), so the build-vs-adopt "pass" holds; revisit only if the fact-extraction pipeline becomes a burden.
- **LiteLLM Agent Platform** (open-sourced 2026-05, single-source/unverified): self-hosted k8s sandboxes + harnesses for Claude Code/Codex + vault proxy. If real, it overlaps Apollo+Labyrinth; verify against the BerriAI repo before the H2b design hardens.

### Market dynamics worth recording

2026 has been a consolidation graveyard: Terragon shut down (Jan 2026, code open-sourced), Codegen acquired by ClickUp and discontinued (Jan 2026), Bloop/vibe-kanban hosted service wound down, Crystal deprecated (Feb 2026). Every SaaS orchestrator bet against self-hosting and lost to platform giants. This strengthens the Zakros thesis (own the substrate, ride the subscription) and also means: don't adopt young SaaS-adjacent OSS as load-bearing dependencies — pin versions and assume abandonment (the handoff's "pick and pin one MCP server, the REST API is the stable floor" rule generalizes).

---

## 3. Standards conformance

| House standard | Status | Fix |
|---|---|---|
| Repo-root `CLAUDE.md` (canonical pipelines, retrieval table, public surface) | **Absent** | Author one; the retrieval-lane table plus "Minos is the only dispatcher, Hermes the only surface path" canonical-pipeline declarations |
| `docs/decisions/` ADRs | **Absent** | Create dir; backfill the ~16 inline decisions (list below) via the `adr` skill |
| `LESSONS.md` | **Absent** | Seed with Phase 1/2 operational lessons (e.g. the openbao bootstrap fixes in `661c65a`/`ec3f4c9` imply at least two) |
| One-line-per-paragraph docs | **Conforms** | — |
| Secrets hygiene | **Good** (gitleaks clean, nothing sensitive ever committed) | Close the two gitignore gaps |
| Findings-as-issues (`track-findings-as-issues.md`, PROPOSED) | **Not adopted** — findings live in session docs | Adopt as part of the Forgejo work (handoff Phase 4) |
| Code-intel onboarding | **Done** (`.chunkhound-index` present) | — |
| Doc/reality sync (plan docs describe live system) | **One contradiction** | `phase-2-plan.md §15` claims a CI workflow that never existed — either build the CI or fix the doc |

Decisions currently inline that should be ADRs — from `build-vs-adopt.md §Design-gap questions resolved` (5: single-Postgres/no graph DB; OpenBao hedge; Clio single scope; Asclepius history; research scope) and `phase-2-plan.md §2 + §9` (11+: greenfield no-migration posture; Go; monorepo; **schema-per-project**; Apollo in-process H2a deviation; two-credential system identities; Hecate=OpenBao+vault-mcp-server; Cerberus stays library; Argus early extraction; Slack second surface; webhook multi-identity; H2a/H2b split). Add the Forgejo decision from the handoff (decided in the Athena session, evaluation doc at `~/Source/forge-stack-evaluation-2026-07.md`) as its own ADR when the work starts.

---

## 4. Forge-stack handoff — fit into the roadmap

The handoff (Forgejo as self-hosted forge + issue tracker + kanban, agents drive it via API/CLI/MCP, issues eventually commission Zakros runs) is well-shaped and consistent with existing seams. Assessment:

- **The market survey independently validates it.** Self-hosted issue→agent dispatch exists in exactly one product (GitLab Duo Agent Platform, heavyweight, license-gated). Forgejo+Zakros fills that gap at homelab scale; nothing OTS to adopt instead — Forgejo/Gitea have no first-party agent features, so the webhook+broker glue is genuinely the missing layer.
- **Sequencing:** the handoff's Phase 1 spike (half-day, container, mirror two repos, kill-criteria on auto-close + token scoping) is independent of Zakros slices — it can run **now**, parallel to landing H2a/H2b. The `forge-broker` (handoff Phase 3) touches Zakros code and should sequence **after Slice I** (Hermes extraction) since issue-webhook→commission runs through Cerberus/Hermes shapes that Slice I reworks, and **after** extracting the shared broker bootstrap (don't stamp the 5th copy).
- **Design rule to carry in:** agent-facing work state in labels/milestones (API-complete), boards as human view only — the spike must verify card-moves via API before promising them (handoff already says this; keep it as a kill criterion).
- **Recommended roadmap edit:** add the forge track to `phase-2-plan.md` as a parallel prerequisite-style track (like §3 OpenBao/Slack) with the broker landing as a named slice, rather than letting it live only in a handoff doc. The tracker-of-record migration (handoff Phase 4) is where `track-findings-as-issues.md` gets adopted/amended.
- **Open operator decisions the handoff surfaces** (first repo to migrate tracker-of-record; per-agent bot accounts vs one shared bot; Forgejo Actions dormant vs active) — the audit granularity question rhymes with Slice G's identity registry: per-agent bot accounts map 1:1 onto `system` identities, which argues for per-agent.

---

## 5. Action list

### P0 — this week (protect work, close foot-guns)

1. **Reinstall the Go toolchain** (`brew install go`), then `make vet lint test build` over the working tree — the H2a slice has never been compiler-checked on this machine.
2. **Land Slice H2a**: once green, commit the working tree as the H2a slice (17 modified + new cmd/apollo etc.). Two months of uncommitted, unverified work is the single biggest live risk in the project.
3. **Close gitignore gaps**: add `deploy/apollo.json` and `.infisical.json`; commit `deploy/templates/apollo.json.example` + `apollo.service` with the slice.
4. **Commit `docs/forge-stack-handoff.md`** (and this report) so they survive.

### P1 — standards debt (an afternoon, mostly mechanical)

5. **Author repo `CLAUDE.md`** — canonical pipelines (Minos-only dispatch, Hermes-only surface I/O, envelope schema location), retrieval table, deploy-runbook pointer.
6. **Create `docs/decisions/` and backfill ADRs** for the ~16 inline decisions (prioritize: schema-per-project, OpenBao/Hecate, H2a in-process deviation + 365-day Apollo token, greenfield no-migration posture). Then link them from CLAUDE.md.
7. **Stand up CI** (GitHub Actions running the existing Make targets: vet, lint, test, build, image builds) — or amend `phase-2-plan.md §15` to stop claiming it exists. Building it is ~30 minutes given the Makefile already encodes everything.
8. **README refresh**: status line, constraints list (HMAC/file-secrets claims are behind the code), repo-layout section (four missing binaries).
9. **Seed `LESSONS.md`**; adopt findings-as-issues once Forgejo is live rather than building the habit twice.

### P2 — next slices (ordered)

10. **Forgejo spike** (handoff Phase 1, half day, parallel to everything) → then productionize via `~/Source/homelab` (handoff Phase 2).
11. **H2b** (Apollo rate limits + non-forgeable usage → Argus) — closes `security.md §13`, prerequisite for K.
12. **Extract the shared broker bootstrap** (config-load/pubkey/Verifier ~30-line pattern) into `pkg/brokerauth` or a small `pkg/brokerboot` before writing forge-broker (the 5th copy).
13. **Slice I → K → L1**, per plan. Within L2–L5, **prioritize Momus (L2)** — PR-review agents are now table stakes across the market and it's the highest-leverage pod class remaining.
14. **forge-broker + issue-webhook commissioning** after I and the bootstrap extraction; record the per-agent-bot-vs-shared-bot decision as an ADR (leaning per-agent, mapping onto Slice G `system` identities).

### P3 — opportunistic / watch

15. Re-evaluate **kubernetes-sigs/agent-sandbox** CRDs at Phase 3 as a replacement for hand-rolled pod lifecycle (potential gVisor upgrade).
16. Read **Claude Code Channels** docs before building more Hermes surfaces; verify **LiteLLM Agent Platform** against the BerriAI repo before H2b design hardens.
17. Consider a **fact-curation UX** for Mnemosyne (Devin Knowledge/Playbooks is the shape to steal) when the Phase 2 Mnemosyne maturity work comes up.
18. Resolve the naming residue: rename the GitHub repo `daedalus` → `zakros` (remote/module/registry currently disagree three ways).

---

*Survey data is point-in-time (2026-07-04); pricing and product claims flagged as unverified in the underlying research were excluded from conclusions. Re-check the watch-list items before each phase's design hardens.*
