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

### Memory systems (vs §14)

- **mem0** — Apache-2; extracted-fact-primary architecture contradicts the run-record-primary decision already taken; no pre-persistence sanitization hook; flat `user_id`/`agent_id` scoping. **Pass.**
- **Letta** — Apache-2; memory scoped to agent instances, not projects — wrong orientation for a memory service *behind* multiple agents. Postgres + pgvector supported. **Pass.**
- **Zep / Graphiti** — Zep CE **deprecated April 2025**; self-host path is Graphiti-only. Graphiti requires Neo4j/FalkorDB/Kuzu, **no pgvector**. Storage-incompatible. **Pass for storage; borrow the temporal-KG design** (validity windows) for Mnemosyne directly.
- **Mempalace** — **Prior "defer" call still holds.** v3.1.0 (2026-04-06) addressed operational leakage but did not change interface shape, add Postgres support, introduce a `run_record` primary unit, or add a caller-facing sanitization contract. LongMemEval 96.6% remains an upstream raw-ChromaDB result per the project's own 2026-04-07 correction, not palace-structure lift. Revisit criteria from the memory note unmet.

### Secret providers (vs §6)

- **HashiCorp Vault OSS** — BSL 1.1 (homelab OK; governance risk is real). 4-op abstraction is a clean subset of KV-v2 + sys/audit. JSON per-request audit ships cleanly to Vector.
- **OpenBao** — MPL-2.0, LF-governed, IBM-backed; v2.5.0 Feb 2026; API-compatible with Vault. Same fits as Vault, removes BSL governance risk. **The right second reference next to Infisical.**
- **1Password Connect** — Mandatory 1password.com control plane; fails self-containment. **Pass.**
- **Cloud SMs (AWS/GCP/Azure)** — Not self-hostable. **Pass.**

### Egress proxy (vs §11 Charon)

- **Squid (Peek-and-Splice)** — GPL-2.0, active. `ssl_bump peek step1 / splice all` gives SNI decision without MITM. `logformat` maps to the contract tuple (pod_id derived from per-class listener port, not native). `squid -k reconfigure` reloads ACLs without dropping connections. Dense config; Peek-and-Splice is a historical footgun.
- **Envoy** — Apache-2, CNCF. `tls_inspector` + `filter_chain_match.server_names` = canonical SNI-passthrough. Native JSON access log matches the contract tuple directly. xDS for zero-downtime reloads (overkill; file reload with `drain_time_s` simpler). ~100MB baseline, hundreds of YAML lines for a trivial allowlist.
- **mitmproxy** — MIT; "no-MITM" mode exists but is a minority use — works against the project's primary design. **Pass.**
- **HAProxy** — Strong on SNI L4 routing + hot reload; not a forward-proxy in HTTP CONNECT sense — pods expecting `HTTPS_PROXY` would need transparent iptables interception. Keep warm, don't lead with.

**Recommendation: adopt Squid as Phase 3 reference**; keep Envoy as scale-out fallback if per-task `egress_hosts` dynamics make Squid's reconfigure cost painful.

### Ingress (vs §6 Cerberus plugins)

- **Tailscale Funnel** — Outbound-only matches the no-inbound-ports property. `*.ts.net` hostname only (no custom domains; open FR since 2024, still open). Free tier capped at 3 tailnet users; Funnel gated to free-plan specifically. **Adopt as second plugin** — Cerberus routes by path anyway; `*.ts.net` is acceptable.
- **ngrok** — Self-hostable path effectively dead (v1 unmaintained >6yr; v3/Cloud Edge proprietary SaaS). **Pass.**
- **smee.io** — "Not for production"; one channel == one target, minimal routing; maintenance low. Consider **gosmee** (self-hostable Go rewrite) as a future local-dev plugin. **Pass for production.**

### Health monitor (vs §13 Asclepius)

- **Netdata** — GPL-3.0, very active. Strong liveness/resource/custom via collectors; flow checks need custom collectors. **No Postgres-native history** (own tiered DB); no MCP surface; remediation via scripts only. Data plane excellent — keep as per-node diagnostic alongside Asclepius, not as Asclepius core.
- **Prometheus + Alertmanager + node/blackbox exporters** — Apache-2, CNCF. Best check-kind coverage. TSDB is local — hybrid model required (Prometheus holds metrics; thin Asclepius service holds `asclepius` Postgres schema for transitions + remediation audit + MCP broker; Alertmanager webhooks trigger state transitions). Cross-monitoring via mutual `/health` scrape is native. **Write-adapter; adopt.**
- **Consul** — BSL 1.1, no healthy fork at production maturity. Keeps current state in Raft log, not history — defeats the "transition history in `asclepius` schema" design. **Pass.**
- **Uptime Kuma / Zabbix / checkmk** — **Pass.**

---

## Top 3 adopt-quality candidates (PoC before the relevant phase plan)

1. **`github/github-mcp-server`** — Phase 1 GitHub broker. Only candidate covering PR/issue verbs; vendor-official, MIT, Jan 2026 active. PoC: shim that mints App installation tokens from JWT scopes and spawns the subprocess per session. Tests the generic "Daedalus terminates JWT, wraps upstream MCP" pattern on the highest-traffic broker.
2. **`hashicorp/vault-mcp-server` (+ OpenBao backend)** — Phase 2 Hecate. The only secret-class MCP whose upstream auth model (Vault policies) composes with Daedalus scopes. Pair with OpenBao so the BSL governance risk of Vault OSS doesn't land on the critical path. PoC: one policy per `credentials.fetch:<ref>`, Hecate mints short-lived policy-bound tokens from JWT claims.
3. **`grafana/mcp-grafana`** — Phase 1+ Ariadne queries. The only surveyed MCP with per-call bearer on HTTP and upstream scopes (datasource-scoped SA tokens) that meaningfully compose with the Daedalus JWT/scope model. Handles Loki's `X-Scope-OrgID` multi-tenancy natively. Lowest-risk validation of the shim pattern.

Honorable mention: **Goose** as a second worker-backend plugin if the Phase 2 pluggable-worker-interface work wants a second real implementation beyond Claude Code — its MCP-native design is the one feature Daedalus cannot cheaply replicate. Measure adapter size for envelope-to-recipe translation; under ~400 lines it beats writing a second plugin from scratch.

---

## Design-gap questions surfaced

These came up while scoring candidates; flagging for architecture-side decisions, not proposing changes:

1. **Mnemosyne backend commitment.** Graphiti's temporal-KG shape is architecturally attractive for `learned_facts` but requires Neo4j/FalkorDB/Kuzu. Is the "single shared Postgres LXC" decision binding enough to rule out a KG sidecar, or is a graph backend permissible if it doesn't disturb the Minos/Argus/Mnemosyne DB-consolidation?
2. **Vault governance hedge.** If Hecate's PoC is on `vault-mcp-server`, does the BSL governance risk warrant documenting OpenBao as the reference backend (not just a valid substitute)? Matches the "Infisical or file-backed" pattern already in §17.
3. **Ariadne scope granularity.** The `ariadne` broker has exactly one scope (`query`). Grafana SA tokens naturally express datasource-scoped queries (`datasources:uid:loki-prod`). Is per-datasource scope separation useful for Ariadne (e.g., "Iris can query task logs but not MCP-broker audit logs"), or is one read scope always enough?
4. **Asclepius history location.** Prometheus-as-Asclepius forces a hybrid: metrics in Prometheus TSDB, transition history in the `asclepius` Postgres schema. §13 describes transitions in Postgres but doesn't explicitly rule out Prometheus TSDB as the *metric-history* tier. Is that split OK, or should §13 stake out "all history in Postgres"?
5. **Research-broker scope granularity.** Exa and Firecrawl bundle expensive deep-research tools with cheap search under one upstream key. A single `research.query` scope papers over that. Worth a `research.deep` separation for per-task cost containment, or does budget enforcement at the task envelope level cover it?

---

*This document captures a point-in-time survey. Candidate maintenance, licenses, and feature sets drift; re-evaluate before each phase's broker plan starts.*
