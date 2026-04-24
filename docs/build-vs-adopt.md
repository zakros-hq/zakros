# Project Daedalus — Build-vs-Adopt Survey

*Version 0.1 — Draft*

---

## Purpose

Before Phase 1 implementation starts, every broker and infrastructure component in `architecture.md` is checked against existing open-source products and community MCP servers. Where an upstream fits the Daedalus broker contract, adopt. Where the contract composes poorly with available options, write in-house with the survey as justification.

Survey date: 2026-04-20. Every candidate scored against the broker contract: MCP protocol, per-call auth (Phase 1 bearer, Phase 2 Minos-minted Ed25519 JWT with `aud`/`exp`/`jti`/`mcp_scopes`), deny-by-default scope granularity, structured audit to Ariadne, subprocess isolation, permissive OSS license, <6-month maintenance signal. Gaps only — fits are not enumerated.

Recurring pattern across the MCP ecosystem: candidates use one static env-var credential and expose all tools to whoever holds it. Per-operation scope enforcement derived from a caller's JWT is Daedalus-broker territory; almost no upstream does it. The survey outcome is therefore overwhelmingly "wrap an upstream MCP behind a Daedalus shim" rather than "adopt one wholesale."

---

## MCP brokers

### github

Candidates: `github/github-mcp-server` (official, MIT, vendor-backed, Jan 2026 changelog), `cyanheads/git-mcp-server` (Apache-2, local-only), `modelcontextprotocol/servers#git` (MIT, reference, local-only).

- **github/github-mcp-server** — Auth is OAuth/PAT only; no Ed25519 JWT validation. Scopes map to GitHub OAuth scopes (`repo`, `issues:write`), not Daedalus verbs (`pr.create` vs `pr.comment`); `--toolsets` gives coarse grouping only. Audit is stdout + GitHub server-side, no Vector-ingest-ready emission. One subprocess per token is the only isolation story.
- **cyanheads/git-mcp-server, reference#git** — Local-only; don't cover `pr.*`/`issue.*`.

**Recommendation: adopt upstream behind Daedalus shim.** The only candidate covering PR/issue verbs; maintenance is vendor-backed; fork adds nothing. Shim mints App installation tokens per-call from the JWT scope.

### proxmox

Candidates: `canvrno/ProxmoxMCP`, `RekklesNA/ProxmoxMCP-Plus`, `gilby125/mcp-proxmox`, `heybearc/mcp-server-proxmox`, `husniadil/proxmox-mcp-server`.

All five share the same gap shape: single Proxmox API token in env, no MCP-layer bearer verification, no scope separation between `vm.list` and `vm.destroy`, audit is stdout only. Several have unclear licenses; canvrno is stale against PVE 8/9; gilby125's basic/elevated toggle isn't granular enough for the scope table; husniadil uses SSH-to-LXC (wrong transport for `vm.*`).

**Recommendation: write in-house.** Roadmap's "check existing first" box closes negative. Community servers are useful as a tool-schema reference — copy the tool names and `proxmoxer` patterns, implement auth/scope/audit natively. ~2 days of work.

### athena

Candidates: `rawveg/ollama-mcp` (restrictive custom license — **blocker**, prohibits incorporation into third-party services), `emgeee/mcp-ollama` (too narrow), `qdrant/mcp-server-qdrant` (Apache-2, covers only corpus ops), `patruff/ollama-mcp-bridge` (wrong direction — MCP client not server).

**Recommendation: write in-house.** Scope surface spans Ollama + Qdrant + whisper + sandbox lifecycle; no single server covers it, and the Ollama candidate with best coverage is license-hostile. Keep `qdrant/mcp-server-qdrant` as an optional under-the-hood reference for corpus operations.

### apollo

Candidates: `BerriAI/litellm` (MIT, vendor-backed), `Portkey-AI/gateway` (MIT core + proprietary UI features), `stabgan/openrouter-mcp-multimodal`, `matdev83/llm-wrapper-mcp-server`.

- **LiteLLM / Portkey** — Both run one async process fanning out to all providers; a compromised gateway holds every provider's key. Violates "per-provider subprocess isolation." Auth is vendor virtual-keys, no Ed25519 verification (custom auth hooks exist — adapter-reachable but not native). Usage accounting is self-reported by the gateway, not a non-forgeable relay of `x-ratelimit-*` / `anthropic-ratelimit-*` response headers — the exact gap Apollo's "non-forgeable token counts" design targets.
- **OpenRouter MCPs** — Single-provider, fails multi-provider isolation.

**Recommendation: write in-house, study LiteLLM's permission model.** Apollo is the highest-risk broker to outsource. Borrow LiteLLM's allowlist semantics and Portkey's audit shape as implementation references.

### mnemosyne

Candidates: `qdrant/mcp-server-qdrant` (Apache-2), `getzep/graphiti` (Apache-2, 20k★, Zep-backed), `modelcontextprotocol/servers#memory` (ref, JSON-file), `subhashdasyam/mem0-server-mcp`, `elvismdev/mem0-mcp-selfhosted`, `iachilles/memento`.

- **qdrant/mcp-server-qdrant** — Two verbs (`store`/`find`); can't express `memory.lookup` vs `memory.project_context` granularity. No bearer auth.
- **Graphiti MCP** — Bearer-token auth is an RFC (issue #1379), not shipped; HTTP endpoint unauthenticated today. No scope model (`graph.clear` and `search_facts` ride same door). Backend is Neo4j/FalkorDB/Kuzu — **no pgvector support** (FR open, no roadmap). Conflicts with the single-Postgres decision.
- **mem0 / elvismdev / memento** — Licenses unclear or mem0's extracted-fact-primary model is the opposite of the "run record primary, facts derived" decision already taken.

**Recommendation: write in-house on Postgres + pgvector.** Borrow Graphiti's temporal-KG design (validity windows on `learned_facts`) as an architectural reference. Keep `iachilles/memento` (SQLite + sqlite-vec) in mind for the local-dev reference-impl path.

### research

Candidates: `tavily-ai/tavily-mcp`, `brave/brave-search-mcp-server`, `exa-labs/exa-mcp-server`, `firecrawl/firecrawl-mcp-server`, `spences10/mcp-omnisearch`.

All official vendor repos are MIT and actively maintained. Common gaps: single env-var API key, no scope subdivision between cheap search and expensive deep-research tools (Exa's `deep_researcher_start` shares a key with `web_search`), audit via vendor account only. omnisearch carries five upstream keys in one process — 5x blast radius.

**Recommendation: adopt Brave (or Tavily fallback) behind Daedalus shim.** `research.query` is a single scope — the granularity gap collapses. Validates the "wrap upstream as subprocess, enforce JWT at shim" pattern with minimal integration risk. Do **not** adopt omnisearch.

### hermes

Candidates per surface: Slack (`korotovsky/slack-mcp-server` MIT; official reference; Slack-hosted SaaS), Discord (`IQAIcom/mcp-discord` MIT — maintenance at 4-month edge of the window; alternates), Matrix (`ricelines/matrix-mcp` — license unclear, self-described "learning project"), Telegram (`fast-mcp-telegram` most recent at 2026-04-14; `sparfenyuk/mcp-telegram` read-only; `chigwell/telegram-mcp`).

Every surveyed server: one static credential, all tools exposed, stdout audit. `ricelines/matrix-mcp`'s default/opt-in scope split is the closest any candidate gets to Daedalus scope shape but it's compile-time, not JWT-driven.

**Recommendation: write the hermes broker in-house; adopt per-surface MCPs as untrusted subprocesses behind it.** The plugin-per-surface design already assumes this shape.

### hecate

Candidates: `hashicorp/vault-mcp-server` (MIT, vendor-official), `@infisical/mcp`, 1Password community MCPs, community Vault MCPs, Keeper PAM MCP.

- **vault-mcp-server** — Accepts `Authorization: Bearer` on HTTP but as a Vault-token passthrough (not a Minos JWT). Scope enforcement delegates to Vault policies attached to the token — **this is the only secret-class MCP in the survey where upstream auth composes with Daedalus scopes**: one Vault policy per `credentials.fetch:<ref>`, Hecate mints short-lived policy-bound Vault tokens from JWT claims. Vault's own audit devices produce cleaner Ariadne-ingestible JSON than any MCP-layer log.
- **Infisical MCP** — Universal-auth machine-identity creds, not per-call bearer. Inherits Infisical project/env RBAC but no MCP-layer per-op check.
- **1Password MCPs** — Static Service Account token; 1P themselves warn "automation vaults only, avoid high-stakes secrets."

**Recommendation: adopt vault-mcp-server under Hecate.** The one broker where the upstream auth model *helps* instead of fighting.

### asclepius

Candidates: `pab1it0/prometheus-mcp-server`, AWS Labs Prometheus MCP, `DavidFuchs/mcp-uptime-kuma`, Netdata built-in MCP.

- **Prometheus MCPs** — Metrics-only; no `check.run` or `remediate` concept.
- **uptime-kuma MCP** — 9 tools all exposed; has `pauseMonitor`/`resumeMonitor` (write-ish) un-gated. Best fit for `status`+`history`.
- **Netdata built-in MCP** — Two-tier coarse scope (sensitive vs. non-sensitive) is the best upstream scope match, but **served by the Netdata agent itself**, not as a Daedalus-supervised subprocess — contract mismatch on isolation.

**Recommendation: write in-house, consume Prometheus + Uptime Kuma HTTP APIs directly.** No candidate covers all four scopes (`status`/`history`/`check.run`/`remediate`); retrofitting three MCPs with broker middleware costs more than native implementation.

### ariadne

Candidates: `grafana/mcp-grafana` (Apache-2, vendor-official, LogQL + Prometheus + Pyroscope + multi-tenant `X-Scope-OrgID`), `grafana/loki-mcp`, `mo-silent/loki-mcp-server`, `elastic/mcp-server-elasticsearch`.

- **grafana/mcp-grafana** — Accepts service-account tokens via `Authorization` header per-call on HTTP transport — **the only surveyed MCP that natively matches the Phase 1 bearer-on-HTTP shape**. Grafana datasource/permission scopes (`datasources:uid:loki-prod / datasources:query`) compose with tenant-scoped SA tokens — closest upstream scope mapping in the survey. Multi-tenancy via Loki's `X-Scope-OrgID` ships.
- **loki-mcp** — Narrower sibling; fallback if mcp-grafana's surface is too broad.
- **elastic MCP** — Off-path; Daedalus log store is Loki.

**Recommendation: adopt grafana/mcp-grafana behind Daedalus shim.**

---

## Non-MCP components

### Worker backends (vs §8)

- **Aider** — Apache-2, active. No native MCP client (third-party shims only), status is raw stdout, no structured run-record extraction hook. **Pass.**
- **OpenHands** — MIT, very active; typed-event SDK + REST/WS; MCP-native tool system. Gap: adapter embeds the runtime, ~10 event types to translate; no pre-built run-record extractor; trust-boundary still a Phase 2 add. **Write-adapter candidate.**
- **SWE-agent / mini-swe-agent** — MIT. Purpose-built for headless issue-fix (envelope-shaped), but no MCP client — tools are bash + file-editor built-ins; `mcp_endpoints[]` has nowhere to plug in. **Pass for production**; mini-swe-agent's ~100-line auditability is a useful reference for the trust-boundary primitive.
- **Goose** — Apache-2, Linux Foundation AAIF, very active; MCP-native by design — `capabilities.mcp_endpoints[]` maps directly onto extensions. `goose serve` closes the headless gap. Gap: recipes are YAML-parameterized, not arbitrary JSON briefs — adapter translates envelope → recipe parameters; status redirect needs a goose-side MCP client pointed at the `thread` sidecar. **Write-adapter candidate, best pure MCP fit.**
- **Cline** — Apache-2; 2026 CLI + gRPC closes the VS Code requirement. TypeScript/Node runtime adds a dep surface that offers nothing OpenHands/Goose don't. **Pass.**

### Memory systems (vs §19)

- **mem0** — Apache-2; extracted-fact-primary architecture contradicts the run-record-primary decision already taken; no pre-persistence sanitization hook; flat `user_id`/`agent_id` scoping. **Pass.**
- **Letta** — Apache-2; memory scoped to agent instances, not projects — wrong orientation for a memory service *behind* multiple agents. Postgres + pgvector supported. **Pass.**
- **Zep / Graphiti** — Zep CE **deprecated April 2025**; self-host path is Graphiti-only. Graphiti requires Neo4j/FalkorDB/Kuzu, **no pgvector**. Storage-incompatible. **Pass for storage; borrow the temporal-KG design** (validity windows) for Mnemosyne directly.
- **Mempalace** — **Prior "defer" call still holds.** v3.1.0 (2026-04-06) addressed operational leakage but did not change interface shape, add Postgres support, introduce a `run_record` primary unit, or add a caller-facing sanitization contract. LongMemEval 96.6% remains an upstream raw-ChromaDB result per the project's own 2026-04-07 correction, not palace-structure lift. Revisit criteria from the memory note unmet.

### Secret providers (vs §6)

- **HashiCorp Vault OSS** — BSL 1.1 (homelab OK; governance risk is real). 4-op abstraction is a clean subset of KV-v2 + sys/audit. JSON per-request audit ships cleanly to Vector.
- **OpenBao** — MPL-2.0, LF-governed, IBM-backed; v2.5.0 Feb 2026; API-compatible with Vault. Same fits as Vault, removes BSL governance risk. **The right second reference next to Infisical.**
- **1Password Connect** — Mandatory 1password.com control plane; fails self-containment. **Pass.**
- **Cloud SMs (AWS/GCP/Azure)** — Not self-hostable. **Pass.**

### Egress proxy (vs §16 Charon)

- **Squid (Peek-and-Splice)** — GPL-2.0, active. `ssl_bump peek step1 / splice all` gives SNI decision without MITM. `logformat` maps to the contract tuple (pod_id derived from per-class listener port, not native). `squid -k reconfigure` reloads ACLs without dropping connections. Dense config; Peek-and-Splice is a historical footgun.
- **Envoy** — Apache-2, CNCF. `tls_inspector` + `filter_chain_match.server_names` = canonical SNI-passthrough. Native JSON access log matches the contract tuple directly. xDS for zero-downtime reloads (overkill; file reload with `drain_time_s` simpler). ~100MB baseline, hundreds of YAML lines for a trivial allowlist.
- **mitmproxy** — MIT; "no-MITM" mode exists but is a minority use — works against the project's primary design. **Pass.**
- **HAProxy** — Strong on SNI L4 routing + hot reload; not a forward-proxy in HTTP CONNECT sense — pods expecting `HTTPS_PROXY` would need transparent iptables interception. Keep warm, don't lead with.

**Recommendation: adopt Squid as Phase 3 reference**; keep Envoy as scale-out fallback if per-task `egress_hosts` dynamics make Squid's reconfigure cost painful.

### Ingress (vs §6 Cerberus plugins)

- **Tailscale Funnel** — Outbound-only matches the no-inbound-ports property. `*.ts.net` hostname only (no custom domains; open FR since 2024, still open). Free tier capped at 3 tailnet users; Funnel gated to free-plan specifically. **Adopt as second plugin** — Cerberus routes by path anyway; `*.ts.net` is acceptable.
- **ngrok** — Self-hostable path effectively dead (v1 unmaintained >6yr; v3/Cloud Edge proprietary SaaS). **Pass.**
- **smee.io** — "Not for production"; one channel == one target, minimal routing; maintenance low. Consider **gosmee** (self-hostable Go rewrite) as a future local-dev plugin. **Pass for production.**

### Health monitor (vs §18 Asclepius)

- **Netdata** — GPL-3.0, very active. Strong liveness/resource/custom via collectors; flow checks need custom collectors. **No Postgres-native history** (own tiered DB); no MCP surface; remediation via scripts only. Data plane excellent — keep as per-node diagnostic alongside Asclepius, not as Asclepius core.
- **Prometheus + Alertmanager + node/blackbox exporters** — Apache-2, CNCF. Best check-kind coverage. TSDB is local — hybrid model required (Prometheus holds metrics; thin Asclepius service holds `asclepius` Postgres schema for transitions + remediation audit + MCP broker; Alertmanager webhooks trigger state transitions). Cross-monitoring via mutual `/health` scrape is native. **Write-adapter; adopt.**
- **Consul** — BSL 1.1, no healthy fork at production maturity. Keeps current state in Raft log, not history — defeats the "transition history in `asclepius` schema" design. **Pass.**
- **Uptime Kuma / Zabbix / checkmk** — **Pass.**

### Code review pods (vs §12 Momus)

Candidates: CodeRabbit (proprietary SaaS), GitHub Copilot for PRs (proprietary, GitHub-hosted), Reviewpad (archived Feb 2024), `danielchalef/automated-code-reviewer` (MIT, GPT-only, single tool), `mattzcarey/code-review-gpt` (MIT, last release 2024-Q4), `sourcery-ai` (CLI + SaaS hybrid).

- **CodeRabbit / Copilot** — SaaS-only. Fails self-containment; reviewer LLM hosted outside Daedalus's inference plane; no way to route the escalation tier through Apollo. **Pass.**
- **Reviewpad** — Rules-DSL approach, no AI review component; archived upstream. **Pass.**
- **code-review-gpt / automated-code-reviewer** — Single-tier (no local triage), hardcoded OpenAI client, no MCP surface, no structured verdict output — just diff-context prompts. Useful as *prompt references* for the escalation tier. **Pass, keep prompts.**
- **Sourcery** — CLI component is self-hostable but rule-based (no LLM-review tier); SaaS tier is where the AI review lives. Split-model doesn't compose with Apollo-on-internal-network. **Pass.**

**Recommendation: write in-house.** Two-stage local-triage + Apollo-escalation architecture is the load-bearing economic decision (60–70% Claude-call reduction); no surveyed candidate splits local/remote this way. Architectural-drift detection against in-repo ADRs is also a Daedalus-specific surface — needs to read `docs/adr/accepted/` and flag divergence, which upstream reviewers don't model.

### Documentation pods (vs §13 Clio)

Candidates: Mintlify (SaaS, proprietary), docusaurus-based generators (framework, not generator), `TheodoreGalanos/SAID` (stale), `openai/swarm` doc examples, Doxygen / Sphinx / MkDocs (non-AI, format tools), `continuedev/autodev` (IDE-embedded, not headless).

- **Mintlify** — SaaS; LLM-review tier hosted externally; no way to scope write path to `docs/**`. **Pass.**
- **Sphinx / MkDocs / Doxygen** — Format tools, not generators. These are *dependencies* of a Clio implementation (the doc format target), not alternatives to it. **Keep as output-format references.**
- **continuedev/autodev** — IDE-embedded, not designed as a headless pod that opens PRs against a doc path. **Pass.**

**Recommendation: write in-house.** Clio's contract — scheduled rollup, `docs/**`-only GitHub scope, Mnemosyne-informed project glossary — has no upstream match. The actual *doc content* work (README section generation, CHANGELOG formatting, API-doc scaffolding) is template-heavy and well-suited to the local `qwen3.5:27b` tier.

### DevOps / release pods (vs §14 Prometheus)

Candidates: semantic-release (MIT, release-only), release-please (Apache-2, Google-backed, release-only), Dagger (Apache-2, pipeline DSL), Earthly (MPL-2.0, pipeline DSL), Spinnaker (Apache-2, multi-env promotion), Argo Rollouts (Apache-2, k8s-native promotion).

- **semantic-release / release-please** — Version-bump and changelog-cut only. Useful as *components* Prometheus invokes, not alternatives to Prometheus. **Adopt as underlying tools.**
- **Dagger / Earthly** — Pipeline DSLs; the "how to build" layer, not "what to release when." Orthogonal. **Keep as pipeline-authoring references.**
- **Spinnaker** — Full promotion orchestrator but heavy (JVM stack, multi-service deploy). Overkill for a single-operator homelab; Phase 2 target is single-project multi-environment, not multi-project enterprise. **Pass for Phase 2; reconsider if multi-project scope arrives.**
- **Argo Rollouts** — k8s-native canary/blue-green for Labyrinth-deployed workloads. Useful *target* for the Prometheus production-promotion MCP, not a replacement for Prometheus's decision layer. **Keep as promotion-target reference.**

**Recommendation: write in-house, adopt release-please + semantic-release as sub-components.** Prometheus's role is orchestration (read CHANGELOG → decide bump → sequence environments → gate prod on confirmation token); the arithmetic of semver bumps and changelog formatting is offloaded to existing tools.

### Architectural-assistant pods (vs §15 Hephaestus)

Candidates: Structurizr (C4-model DSL, non-AI), archunit / dependency-cruiser (static analyzers, non-AI), `adr-tools` (Nygard's shell scripts, non-AI), `log4brains` (ADR-rendering static site, non-AI).

No AI-architectural-assistant candidates surveyed. All upstream tools in this space are either (a) static dependency/coupling analyzers with no reasoning layer, or (b) ADR *formatters* without authoring capability.

**Recommendation: write in-house.** Hephaestus is a Claude-heavy pod; its value is the reasoning, not the format. Adopt `adr-tools` structure (numbered `docs/adr/NNNN-slug.md`) as the output convention. Consume archunit-style static analysis (language-appropriate tools per project) as *input* to the coupling reports.

### Project-management pods (vs §11 Themis)

Candidates: `AgentGPT` / `AutoGPT` / `BabyAGI` (task-decomposition demos from 2023), `CrewAI` (Apache-2, agent orchestration framework), `LangGraph` (MIT, agent graph framework), `modal-labs/devlooper` (task-scoped agents), `microsoft/autogen` (MIT, multi-agent orchestration).

- **AgentGPT / AutoGPT / BabyAGI** — Demo-grade; no production-ready task persistence, no scope enforcement, no external command surface. **Pass.**
- **CrewAI / AutoGen / LangGraph** — Agent orchestration frameworks. These model *how agents talk to each other*, not *how a backlog decomposes to a team of pod classes with MCP capabilities*. The envelope-schema decomposition Themis does is a Daedalus-specific shape. **Keep as decomposition-pattern references.**
- **devlooper** — Scope-narrowed task runner, single-task orientation. **Pass.**

**Recommendation: write in-house.** Themis commissions through Minos's existing task API; its job is decomposition into `task_type` envelopes and cross-pod state tracking, not generic agent coordination. CrewAI/AutoGen are interesting as references for the decomposition prompting but don't compose with Daedalus's capability model.

---

## Top 3 adopt-quality candidates (PoC before the relevant phase plan)

1. **`github/github-mcp-server`** — Phase 1 GitHub broker. Only candidate covering PR/issue verbs; vendor-official, MIT, Jan 2026 active. PoC: shim that mints App installation tokens from JWT scopes and spawns the subprocess per session. Tests the generic "Daedalus terminates JWT, wraps upstream MCP" pattern on the highest-traffic broker.
2. **`hashicorp/vault-mcp-server` (+ OpenBao backend)** — Phase 2 Hecate. The only secret-class MCP whose upstream auth model (Vault policies) composes with Daedalus scopes. Pair with OpenBao so the BSL governance risk of Vault OSS doesn't land on the critical path. PoC: one policy per `credentials.fetch:<ref>`, Hecate mints short-lived policy-bound tokens from JWT claims.
3. **`grafana/mcp-grafana`** — Phase 1+ Ariadne queries. The only surveyed MCP with per-call bearer on HTTP and upstream scopes (datasource-scoped SA tokens) that meaningfully compose with the Daedalus JWT/scope model. Handles Loki's `X-Scope-OrgID` multi-tenancy natively. Lowest-risk validation of the shim pattern.

Honorable mention: **Goose** as a second worker-backend plugin if the Phase 2 pluggable-worker-interface work wants a second real implementation beyond Claude Code — its MCP-native design is the one feature Daedalus cannot cheaply replicate. Measure adapter size for envelope-to-recipe translation; under ~400 lines it beats writing a second plugin from scratch.

---

## Design-gap questions resolved

These came up while scoring candidates and were resolved with the operator on 2026-04-20.

1. **Mnemosyne backend commitment.** *Resolved — single-Postgres stays.* Graphiti's temporal-KG shape is architecturally attractive for `learned_facts`, but adopting Neo4j/FalkorDB/Kuzu as a sidecar is a service-expansion decision. Any such expansion requires an explicit justification case (operational evidence that pgvector alone is inadequate) and lands no earlier than Phase 3.
2. **Vault governance hedge.** *Resolved — OpenBao is the named Vault-compatible alternative.* The Vault OSS BSL 1.1 license is acceptable for homelab use but carries governance risk; OpenBao (MPL-2.0, LF-governed, IBM-backed, API-compatible with Vault 1.14) is the preferred backend if Hecate's PoC goes the Vault-API route. Documented in `environment.md §3`.
3. **Ariadne scope granularity.** *Resolved — single `query` scope stays through Phase 2.* Per-datasource or per-log-class scope separation can be added in a later phase without structural change if operational need emerges. Grafana SA tokens can be narrowed when that time comes.
4. **Asclepius history location.** *Resolved — Postgres is not exclusive for future service history.* Adding a second history store (Prometheus TSDB, or any other) is treated as a service-expansion decision requiring a justification case, Phase 3+. When Asclepius lands, the hybrid (metrics in Prometheus, transitions in `asclepius` Postgres schema) will need that justification written out, not assumed.
5. **Research-broker scope granularity.** *Resolved — single `research.query` scope at Phase 3 landing.* Task envelope budget (`budget.max_tokens`, `max_wall_clock_seconds` in §8) is the containment mechanism for deep-research cost. A `research.deep` split is deferred pending operational evidence that deep-research is dramatically costlier than ordinary queries; premature split complicates the common case.

---

*This document captures a point-in-time survey. Candidate maintenance, licenses, and feature sets drift; re-evaluate before each phase's broker plan starts.*
