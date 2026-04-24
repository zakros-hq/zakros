# Project Daedalus — Security Design

*Version 0.1 — Draft*

---

## Purpose

This document enumerates security and access-control items that the architecture document identifies as deferred. Each item describes the threat, the gap, and the minimum questions that must be answered before implementation. This is a companion to `architecture.md` and tracks security design work in one place.

Resolutions land here as they are made. Until an item has a resolution section, it is unresolved.

### Phase

This document describes the full security posture the architecture targets across all phases. **Phase 1 (MVP) implements only the controls tagged as Phase 1 below.** Controls marked "Addressed architecturally" describe the intended design — not what Phase 1 actually ships. The phase each control lands in is the phase listed in [`roadmap.md`](roadmap.md); when the phase tag below disagrees with the roadmap, the roadmap wins.

Phase 1 deliberately accepts a lax posture on most of these controls in exchange for MVP velocity. That is called out explicitly per item so no one reads "Addressed architecturally" as "shipped."

---

## 1. Command Intake Authentication

**Threat:** Anyone with access to the dispatch channel (Discord, Telegram) can commission agents. Channel membership is not an identity check.

**Phase: 1 (minimal), 2 (full).** Phase 1 ships a **single hardcoded admin** `(surface, surface_id)` tuple in Minos config — every command intake checks "is this the admin?" against that config and nothing else. The full identity model below is Phase 2.

**Status: Addressed architecturally for Phase 2.** `architecture.md §6` defines the identity model (`(surface, surface_id)` tuples with role and status), a capability-based authorization model (`task.commission.*`, `task.direct`, `task.query_state`, `identity.approve_pairing`, `identity.manage`), preset roles (`admin`, `commissioner`, `observer`, and `system` for internal pods that commission autonomously under a persisted identity — see `architecture.md §11 Authority Model`) with per-identity capability overrides, the pairing flow (`/pair` → admin approval with role specification → active; `system` identities bypass pairing and are provisioned at pod-deployment time), bootstrap via config-file or CLI at install time for human admins (and via the `system_identities` block in `deploy/config.json` for `system` identities), revocation semantics including last-admin protection (human roles only — `system` identities carry no last-identity protection), and the audit trail. Phase 3 adds an admin web UI and per-project scoping.

**Remaining implementation work:**
- Pairing token expiration window and whether single-admin approval is sufficient or a quorum is required (open question in `architecture.md §23`)
- Rate limiting on `/pair` to prevent spam from a single source
- Identity-registry schema migrations for Phase 2 scope expansion
- Audit-log retention policy for pairing events (ties to Ariadne retention, `architecture.md §23`)

---

## 2. Webhook Inbound Surface

**Threat:** GitHub webhooks cause Minos to tear down pods and post to task threads. Hermes surface plugins (Slack, Teams, Discord in interactions mode) receive webhook events that control how tasks run. An unauthenticated endpoint is a trivial DoS and confused-deputy target.

**Phase: 1 (minimal), 2 (full broker).** Phase 1 ships **Cerberus as a library inside Minos** with one ingress path (Cloudflare Tunnel) and one verifier (GitHub HMAC with `X-GitHub-Delivery` replay protection). Phase 2 extracts Cerberus as a standalone broker and adds additional ingress and verification plugins.

**Status: Addressed architecturally.** `architecture.md §6 Webhook Ingress: Cerberus` defines a two-plugin-layer ingress broker:

- **Ingress plugins** govern how external traffic reaches Crete — Phase 1 ships Cloudflare Tunnel as the reference implementation (outbound tunnel from Crete, TLS terminated at Cloudflare, no inbound ports exposed on Crete's edge — preserves self-containment). **Phase 1 trust placement:** because Cloudflare Tunnel terminates TLS at Cloudflare's edge and forwards plaintext over the tunnel, every inbound webhook body (GitHub commit diffs, PR titles, issue text, private-repo metadata, and the HMAC-signed bytes Cerberus verifies) transits Cloudflare infrastructure in the clear. Phase 1 accepts Cloudflare as a trusted intermediary for webhook transit; deployments that cannot accept that exposure must wait for Phase 2 alternate ingress plugins. Phase 2 adds Tailscale Funnel, operator-managed direct port-forward, and any custom plugin implementing the ingress contract. (Local development can run the direct port-forward plugin as a convenience in Phase 1; it is not a supported production ingress before Phase 2.)
- **Verification plugins** govern how each source authenticates — Phase 1 ships GitHub HMAC with `X-GitHub-Delivery` replay protection, plus a generic HMAC verifier. Phase 2 adds Slack signing, Discord Ed25519, and per-source verifiers as Hermes plugins land.

Every inbound request passes through one ingress plugin and the verification plugin matching its route. Rejected requests are logged to Ariadne and pushed to Argus as events. Route table and replay-ID state live in the shared Postgres instance.

**Remaining implementation work:**

- Replay-window defaults — how long Cerberus retains delivery IDs before reusing the storage; trade-off against DB size and replay-window strictness
- Webhook-body audit policy — logs retain metadata only, or full bodies (privacy/compliance concern for webhook payloads that may contain user data)
- Ingress-plugin failover — when Cloudflare Tunnel is unavailable, is there a fallback to direct port-forward, or does inbound block until tunnel recovers? Default is to block (no fallback) so auth posture doesn't degrade silently
- Cerberus rate limiting per source IP and per verification-plugin — prevent bypass attempts from flooding the verification path
- Argus thresholds for sustained rejection patterns (when repeated HMAC failures mean "probing" vs "broken deployment")

---

## 3. Per-Pod Credential Scoping

**Threat:** If a pod holds long-lived credentials that grant access to shared secrets, compromising one pod compromises every secret that identity can fetch.

**Phase: 1.** Secret provider abstraction, Minos-only provider calls, per-pod credential injection, GitHub App with per-task installation tokens, and Mnemosyne sanitization at extraction are all Phase 1. In-pod credential refresh (for tasks that run past the GitHub token's 1-hour TTL) arrives in Phase 2 via **Hecate**, the credentials broker that fronts the secret provider under JWT MCP broker auth. Phase 1 Daedalus pods also receive the operator's `claude-code` credential (Anthropic API key or OAuth token) via the same injection path.

**Status: Addressed architecturally for most credentials, but with a Phase 1 exception called out below.** `architecture.md §6 Credential Handling` defines the identity tiers (Minos provider-identity, per-project config, per-pod injection), the GitHub App pattern with per-pod installation access tokens (1-hour TTL, scoped to the single task-target repo), rotation and revocation semantics, and the mandatory Mnemosyne sanitization pass that redacts injected credentials and high-entropy values before run records are persisted.

Pods never call the secret provider directly. In Phase 1, Minos resolves credentials and injects them at spawn; in Phase 2, Hecate serves them on authenticated fetch with Minos-set ACLs.

**Phase 1 exception — `claude-code` credential is a shared-blast-radius secret.** The Anthropic API key or OAuth token for `claude-code` is held at deployment scope (one operator subscription) and injected into every Daedalus pod. Per-pod scoping does *not* hold for this credential class in Phase 1 — a compromise of any single pod exposes the operator's full Anthropic subscription. The cost consequences of that compromise are bounded only by the **Anthropic workspace spend cap configured in the Anthropic console** (a Phase 1 deployment prerequisite, not an in-system control; see `security.md §13` and `architecture.md §7 Phase 1 budget posture`). Apollo (Phase 2) closes this gap by moving the credential behind a broker. Accepted Phase 1 risk for the single-operator single-project posture.

**Remaining implementation work:**

- Provider-interface contract: minimum capabilities the secret-provider abstraction must expose (resolve, rotate, revoke, audit-list)
- High-entropy redaction heuristic tuning — how aggressively Mnemosyne scrubs values; false-positive rate against ordinary conversation content
- Per-pod credential tracking in Minos state (so sanitization knows which values to redact at extraction time)
- Phase 2 Hecate broker: ACL schema, JWT-scope shape (`credentials.fetch:<credential_ref>`), replay/reuse policy on the fetch API, and the migration path from Phase 1 push-injection to Phase 2 pull
- In-pod credential refresh via Hecate — required before any pod task is expected to run actively past the GitHub App installation token's 1-hour TTL
- Audit format: what the Ariadne log line looks like for "token minted for pod P with scope S"

---

## 4. Hermes Surface Credentials

**Threat:** Hermes holds the bot/app credentials for every configured surface (Discord bot token, Slack app credentials, Teams bot credentials, Telegram bot token, etc.). If Hermes is compromised, all surface credentials leak simultaneously, granting access to every channel and thread each bot can see.

**Phase: 1 (single credential), 2 (subprocess isolation + rotation), 3 (message signing).** Phase 1 runs one surface plugin in-process with Hermes, holding one credential. No subprocess isolation and no Argus message signing. The task_id→thread_ref cross-thread posting binding **is** in Phase 1 (cheap, worth keeping; Iris benefits from it). Phase 1 accepts the tradeoff that a compromised Hermes can suppress or rewrite messages undetected — single-operator homelab posture.

**Status: Addressed architecturally.** `architecture.md §6 Communication Surfaces` defines:

- **Plugin process isolation** — each surface plugin runs in its own subprocess; only that plugin's credentials live in that subprocess's memory. Compromise of one plugin (e.g., a Slack plugin bug) does not leak credentials held by another (Discord). Plugin restart for rotation or version update is independent.
- **Cross-thread posting enforcement** — pods send messages without thread parameters; Hermes resolves `thread_surface`/`thread_ref` from the JWT `sub` (task_id) via Minos's task registry. A compromised pod cannot supply an alternate thread or redirect to another task's thread.
- **Phase 1 token posture** — one shared bot/app per surface per deployment. Cross-thread protection comes from Hermes's task_id→thread_ref binding, not per-thread credentials. Per-project bots and per-thread webhooks are Phase 2 optional hardening paths.
- **Rotation** — each plugin subprocess re-reads credentials on SIGHUP or admin-triggered rotation, disconnects, and reconnects with fresh credentials. In-flight operations finish on old credentials; new operations use new credentials. No disruption to other plugin subprocesses.
- **Read posture** — each plugin subscribes to its surface's native event stream (Discord bot gateway, Slack events API, Telegram long-poll, etc.) scoped to the bot's membership. Incoming messages are identified by `(surface, surface_id)` and matched against the identity registry; authorized commands forward to Minos.
- **Message integrity** — Phase 1 trusts Hermes. Phase 3 adds Argus message signing for high-privilege events (termination especially), so a compromised Hermes cannot rewrite or suppress escalations without detection.

**Remaining implementation work:**

- Plugin subprocess supervision and crash recovery (Hermes needs to relaunch failed plugins without leaking state)
- Webhook-based event reception for surfaces that require it (intersects with `security.md §2` — many surface plugins can use outbound-only gateways/long-poll, but some features require receiving webhooks)
- Per-project bot-token swap path in Phase 2 — how a project upgrades from shared-bot to per-project-bot without downtime
- Argus message-signing key distribution and UI verification flow (Phase 3)

---

## 5. Git Clone and GitHub Write Credentials

**Threat:** An agent with write access to any branch in any repo is an agent that can push to `main` if branch protection is missing or misconfigured on any single target repo.

**Phase: 1.** GitHub App pattern, per-task installation tokens, branch protection as the structural backstop — all Phase 1. Phase 1 tasks target a single repo; multi-repo tasks are a Phase 2+ schema extension.

**Status: Largely addressed architecturally.** `architecture.md §6 Credential Handling` commits to the GitHub App pattern (not PATs) with per-pod installation access tokens scoped to the single target repository, 1-hour TTL, and the App's private key held via the secret provider. The same token is used for clone and push — no need for separate credentials. Branch protection on target repos remains the structural push prevention.

**Remaining implementation work:**

- **Branch protection policy at project registration** — when a repo is added to the Minos project registry, Minos should verify the target repo has branch protection on `main` (or the configured base branch). Options: refuse to accept the project until branch protection is in place; accept with a warning logged to Discord; auto-configure branch protection via the App's Administration permission. Phase 1 policy needs a pick.
- **Installation token repository scoping** — Phase 1 scopes every mint to the single task-target repo (tighter is better). Multi-repo task support and the associated scope question are Phase 2+.
- **Incident response** — if a token is observed outside its intended use, the flow to invalidate it (App private-key rotation invalidates all outstanding tokens from that key) and respawn all in-flight pods with fresh tokens from the new key.

---

## 6. MCP Broker Authentication

**Threat:** Pods reach privileged capabilities (Proxmox, Terraform state, Athena) via MCP servers. If any MCP endpoint is reachable without caller authentication, any pod with network access inherits every capability that MCP provides.

**Phase: 2.** Phase 1 uses a simpler pattern: pods authenticate to Minos/Hermes with a bearer token minted at spawn, verified by a shared-secret check on the receiving side. Both sides run inside Crete on a trusted Proxmox virtual bridge. High-blast confirmation tokens and the full JWT design below are **Phase 2**.

**Status: Addressed architecturally for Phase 2.** `architecture.md §6 MCP Broker Authentication` defines the pattern:

- Minos signs a JWT per pod at spawn with claims: `sub` (pod id), `iss` (`minos`), `exp` (2hr default), `aud` (broker names), `mcp_scopes` (map from broker name → allowed operations), `jti`
- Brokers validate signature via Minos's public key (distributed via the secret provider), check audience, expiry, and scope
- Denied calls return 403 with structured error and are pushed to Argus as guardrail events; repeated denials from the same pod trigger escalation and termination per §7
- Every call logged to Ariadne `(timestamp, pod, broker, operation, outcome, jti)`
- Task schema carries `mcp_auth_token` alongside `mcp_endpoints` and their `scopes` arrays
- Emergency revocation: rotate the signing key to invalidate every outstanding token at once

The pattern is universal — it applies identically to Athena (§7), Hermes (§4), GitHub, Proxmox, Mnemosyne, research, and any future broker.

**Remaining implementation work:**

- Replay-protection design — `jti` tracking window, storage (Minos or per-broker), trade-off against stateless validation
- Phase 2 per-project scope enforcement (the JWT names the pod's scopes; per-project policy lives in the broker's own config)
- Exact broker-side behavior on signature-validation failures vs scope failures — do we rate-limit? Do we quarantine a pod that fails repeatedly before Argus decides?

---

## 7. Athena Caller Authentication

**Threat:** Athena hosts the inference services, the domain knowledge corpus, and the embedding server. Network-level isolation is the only control described. Any pod that reaches Athena's IP can query any service on it.

**Phase: 1 (network isolation), 2 (JWT scope check at Athena MCP).** Phase 1 relies on Proxmox firewall rules restricting which guests can reach Athena's inference ports — no caller authentication at Athena itself. Phase 1 Iris (via Ollama on Athena) and Daedalus pods (for any inference needs) reach Athena through network-trust only. Phase 2 adds JWT validation at the Athena MCP, depending on §6 (MCP Broker Authentication) landing.

**Status: Addressed architecturally for Phase 2 as an application of §6 (MCP Broker Authentication).** The Athena MCP validates the Minos-minted JWT on every call, checking `aud` includes `athena` and `mcp_scopes.athena` includes the requested operation (`inference.query`, `models.*`, `sandbox.*`, `corpus.refresh`). Low-privilege inference scopes (`inference.query`, `models.list`) are included in Daedalus pods' default capabilities; high-privilege scopes (`models.pull`, `corpus.refresh`, `sandbox.*`) are granted only when operator intent is established.

**Remaining implementation work:**

- Direct Ollama / Qdrant / embedding-server ports on Athena: Athena exposes two caller-authentication surfaces with different trust models. The Ollama port is reachable directly from local-model-backend pods (Iris §10, and the Phase 2 pod fleet — Themis §11, Momus §12, Clio §13, Prometheus §14) gated by network trust (Proxmox firewall + Labyrinth egress policy). The Athena MCP fronts the higher-privilege surfaces (`models.pull`, `models.load`, `sandbox.*`, `corpus.refresh`) and the JWT-scoped inference path (`inference.query`) that Pythia §9 uses for broker-fronted summarization. Two patterns coexist by design. Phase 2 work: confirm no MCP-only operations leak via the direct port path (Ollama's native API should be inference-only at the port Labyrinth can reach) and that Qdrant / embedding-server ports remain MCP-fronted (not in the direct-HTTP set).
- Sandbox-caller scoping — `sandbox.exec` on a specific `sandbox_id` should only be allowed to the pod that created that sandbox; enforcement lives in the Athena MCP, possibly via a per-sandbox scope injected into subsequent JWTs.
- Corpus write operations (beyond `corpus.refresh`) — any direct writes to Qdrant collections from operator surfaces need explicit scope names; currently all writes flow through `corpus.refresh` which is too coarse for fine-grained collection management.

---

## 8. Egress Allowlist Granularity

**Threat:** "GitHub on outbound HTTPS" at wildcard granularity invites exfiltration via `gist.githubusercontent.com`, raw content, or Pages. Agents run LLM-generated code that may make arbitrary outbound calls.

**Phase: 1 (IP-range allowlist), 3 (Charon SNI proxy).** Phase 1 is Proxmox IP-range enforcement with the known limitation that GitHub-hosted surfaces share CIDR with api.github.com. Charon, hostname-layer allowlists, and per-task egress additions land in Phase 3 when Pythia/Talos/Minotaur/Typhon create divergent per-class egress needs.

**Status: Addressed architecturally.** `architecture.md §16 Egress Granularity` defines:

- **Phase 1** — Proxmox firewall enforces an IP-range allowlist; GitHub IP ranges fetched from `api.github.com/meta` refreshed daily, package registry CDN CIDRs refreshed weekly, Anthropic API CDN ranges for `claude-code`'s direct provider call from every Daedalus pod. Known limitation: hostname-level differentiation within a shared CIDR (GitHub API vs raw vs gists) is not enforceable at this layer. Accepted Phase 1 risk given the single-operator deployment running trusted plugins (`claude-code`, Iris backed by Athena-local Ollama). **Phase 2 Apollo collapse:** once Apollo holds the Anthropic credential (Phase 2), the pod-side Anthropic allowlist entry collapses to "Apollo broker only" and external LLM egress stops crossing the Labyrinth vNIC (`architecture.md §16 Egress Granularity`).
- **Phase 3** — **Charon** egress proxy (dedicated Proxmox LXC on Crete) in SNI-passthrough mode. Per-pod-class allowlist by port (Daedalus, Pythia, Talos each listen separately); k3s NetworkPolicy restricts pod→port reachability; every request logged to Ariadne. Proxmox firewall collapses to "Labyrinth → Charon only" for external egress.
- **Per-task egress additions** — Phase 3 task schema grows `capabilities.egress_hosts` for temporary per-task additions.

**Remaining implementation work:**

- Specific Daedalus-class hostname allowlist (the current list in `architecture.md §16` is illustrative; the actual curated list needs Phase 1 review)
- Pythia denylist contents (Phase 3) — known-malicious domains, known-exfil-risk endpoints
- Charon audit-log schema — exact fields that land in Ariadne; whether SNI-only is sufficient or whether Host-header peek (which would require TLS termination) is needed for any specific case
- Fallback when Charon is down — do pods lose external egress entirely, or does Proxmox IP-range fallback take over? Interacts with `security.md §9` on layered enforcement precedence.

---

## 9. Pod-to-Pod Network Isolation

**Threat:** k3s with default CNI (flannel) is flat — pods can reach each other by default. A compromised pod can reach every other active pod's sidecar and workspace.

**Phase: 3.** Phase 1 ships **flannel (flat pod network)** — the explicit tradeoff of MVP scope. With only Daedalus and Iris pods running trusted plugins on a single-operator deployment, flat is acceptable. Calico + default-deny + per-pod-class NetworkPolicies land in **Phase 3** alongside the divergent pod classes (Pythia, Talos, Minotaur, Typhon).

**Status: Addressed architecturally for Phase 3.** `architecture.md §16 Pod-to-Pod Isolation` defines:

- **CNI: Calico** swap lands in Phase 3 (mature NetworkPolicy enforcement, light footprint for single-node k3s). Phase 1–2 ship flannel per the roadmap; Phase 3 may revisit Cilium if eBPF observability or L7 policies become worth the overhead.
- **Default-deny pod-to-pod traffic.** All intra-cluster traffic denied unless explicitly allowed. The architecture's broker-mediated coordination pattern (Daedalus → research broker → Pythia, never direct) makes default-deny natural — no legitimate cross-pod flows exist.
- **Per-pod-class NetworkPolicies** selected via labels (`daedalus.project/pod-class`). Each class's egress allow-list names only its required destinations; no class allows egress to another pod class.
- **Intra-pod sidecar traffic** (thread sidecar ↔ agent, Argus sidecar ↔ agent) uses the pod's shared network namespace (localhost) and is not NetworkPolicy-visible — contained by construction.
- **Layered precedence** — Proxmox firewall + Labyrinth host firewall + NetworkPolicy compose as "AND". Traffic passes all applicable layers; strictest wins. Tightening any one layer cannot be undone by a looser configuration in another.

**Remaining implementation work:**

- Concrete NetworkPolicy manifests per pod class (mostly mechanical YAML once the allow-list destinations are known)
- k3s system exceptions — standard k3s NetworkPolicy needed for CoreDNS, metrics-server, probe ingress to pods (standard recipes available)
- Integration test: deploy a test pod, attempt traffic to every other pod class, confirm all denied
- Calico operator installation and version-upgrade path within k3s upgrades (`architecture.md §16 Upgrades and Maintenance`)

---

## 10. Human Break-Glass and Debug Access

**Threat:** A misbehaving agent cannot be inspected through task-thread scrollback alone. Without a defined break-glass path, operators either have unrestricted cluster access (too loose) or none (too tight to investigate incidents).

**Phase: 2 (session minting), 3 (snapshot access).** Phase 1 operator uses **kubectl directly from the Minos VM** (SSH-to-Minos-VM with OS-level auth) — the single operator already has root on Crete, so the "break-glass" flow is informal. Phase 2 adds Minos-brokered session minting with `break_glass.observe` / `break_glass.shell` capabilities once the identity model and capability system exist. Phase 3 adds post-termination snapshot access (depends on the CSI snapshotter).

**Status: Addressed architecturally for Phase 2+.** `architecture.md §6 Operator Break-Glass Access` defines the full flow:

- **Two capabilities** (extending the Phase 1 capability set): `break_glass.observe` (read-only pod state, logs, workspace files) and `break_glass.shell` (interactive kubectl exec)
- **Minos-brokered session minting** — operator requests via Hermes; Minos validates capability, issues a short-lived k3s ServiceAccount token bound to a ClusterRole matching the requested level; session TTL defaults to 30 minutes with extension requiring fresh approval
- **Scope: pods only** — Minos-VM services are outside break-glass; control-plane administration is SSH-to-Minos-VM with OS-level auth
- **Full audit** — session request, approval, credentials issued, every kubectl API call (via k3s audit log → Ariadne), and session close all logged with operator identity and task context
- **Post-termination snapshot access** (Phase 3) — volume snapshots from Argus-triggered termination mounted read-only via `/minos snapshot-fetch <task_id>`; same `break_glass.observe` capability gates access

**Remaining implementation work:**

- Default session TTL and extension policy (tracked as an open question in `architecture.md §23`)
- ClusterRole templates for observe/shell — the exact RBAC verbs matching each level, tested that `observe` cannot escalate to exec paths
- Emergency revocation path — how an in-flight session is killed mid-command if an incident unfolds faster than TTL
- Phase 2 session recording (capture operator's kubectl stream for replay) for compliance-heavy deployments
- Snapshot retention default beyond the per-project 30-day placeholder

---

## 11. Prompt Injection Posture

**Threat:** Agents read source code, issue text, and PR comments — all attacker-controllable on public repos or repos with outside contributors. A successful injection against an agent with MCP access to GitHub write, Proxmox, or Terraform has a very high blast radius. Pythia's arbitrary-web-content responses multiply the exposure.

**Per-pod-class untrusted-input surfaces.** The Phase 2 pod expansion brings new attacker-reachable input channels that flow through the same §8 trust-boundary contract:

- **Momus (`architecture.md §12`)** — reads PR diffs, commit messages, and review comments. Diffs are attacker-controllable on any repo with outside contributors. Capability gating is the backstop: Momus has `pr.comment` only, no `pr.approve` / `pr.merge` / push.
- **Clio (`architecture.md §13`)** — reads commit history, Momus review output, and Hephaestus topology reports as inputs. Commit messages and merged-PR titles are attacker-reachable on contributor PRs. Clio's GitHub writes are path-scoped to `docs/**` at the `github` MCP broker (not at the installation token).
- **Prometheus (`architecture.md §14`)** — reads release changelogs, version files, and pipeline configuration. CHANGELOG content is derived from merged PRs (attacker-influenceable upstream) and from Clio output (one trust-boundary hop further). Production promotion is a high-blast scope requiring an operator confirmation token, so injection cannot drive autonomous prod deploy.
- **Hephaestus (`architecture.md §15`)** — reads repo structure, Mnemosyne context, and operator-provided ADR topic briefs. Produces draft artifacts only (`docs/adr/proposed/**`, `docs/reports/**`); promotion to `docs/adr/accepted/**` requires a human PR merge on a path-scoped write broker.
- **Themis (`architecture.md §11`)** — receives NL requests from Iris (user-authored) and Argus escalations (broker-sourced, not attacker-authored). User NL is untrusted at the §8 level; Argus events are trusted telemetry.
- **Typhon (`architecture.md §3`, Phase 3)** — destructive-test outputs could contain attacker-influenceable content if the target under test is contributor-reachable; scoped internal-only egress limits exfil blast radius.

All these surfaces route through the §8 untrusted-read contract — tool-output framing, no follow-instructions-in-data, high-blast gated by confirmation token. Capability gating is the backstop when framing fails.

**Phase: 2 (trust boundary contract + high-blast confirmation), 3 (Pythia content filter + Argus drift detection).** Phase 1 ships **without** the formal trust-boundary contract in the plugin interface — the single-operator single-project trusted-plugin (`claude-code`) posture is the accepted MVP tradeoff. Injection defenses land once the layered design below is actually needed (second operator, second project, contributor PRs, Pythia landing in Phase 3).

**Status: Addressed architecturally for Phase 2+.** The design defends in layers:

1. **Explicit trust boundary** (`architecture.md §8 Trust Boundary and Untrusted Content`) — trusted content is limited to the system prompt, task envelope, and Mnemosyne-injected context. Everything read during execution (files, PRs, issues, tool output, research results) is untrusted. The worker interface contract frames untrusted content as data, not instructions, for the agent's LLM. System prompt instructs agents to never follow instructions in read content and to use `request_human_input` for suspicious requests.

2. **Pythia response annotation** (`architecture.md §9 Prompt Injection Surface`) — Pythia responses always carry explicit untrusted-content markers. The Daedalus agent's plugin preserves those markers in tool-output framing.

3. **Mnemosyne untrusted-source tagging** (`architecture.md §19 Secret Sanitization`) — run records tag content by source trust level; Mnemosyne preserves tags across context-injection cycles. Injection planted in run N does not become "trusted context" when surfaced in run N+1. **Phase 1 exception:** untrusted-source tagging is Phase 2. Phase 1 Mnemosyne stores and reinjects run records without tags, so an injection planted in run N *does* surface as part of trusted context in run N+1. This is an explicitly accepted Phase 1 risk given the single-operator single-project trusted-plugin posture; see `architecture.md §19 Phase 1 cross-run injection risk` for the full statement.

4. **Capability gating with human confirmation** (`architecture.md §6 MCP Broker Authentication`) — high-blast scopes (push to protected branch, VM creation, corpus refresh, sandbox creation) require a confirmation token minted by Minos upon explicit human approval through the task thread. An injected agent can *attempt* high-blast calls but cannot complete them without a human saying yes.

5. **Capability containment** — even without confirmation gating, MCP broker JWT validation refuses calls outside the task's composed scope. Egress containment comes from Proxmox firewall (Phase 1) and Charon (Phase 2 with per-task domain allowlists for Pythia).

6. **Anomaly detection** (`architecture.md §21 Phase 2`) — Argus Phase 2 drift detection watches for injection patterns: read-untrusted-then-invoke-high-blast sequences, scope drift from declared task brief, unusual egress patterns. Phase 2 also adds a Pythia content filter that flags injection-shaped patterns before returning.

**Remaining implementation work:**

- Per-backend translation of the trust boundary into backend-specific primitives (Claude Code tool-result tagging, qwen-coder system/user message framing, etc.)
- Specific list of high-blast scopes per broker and default confirmation-required policy
- Argus Phase 2 pattern-detection rules — which sequences warrant warning vs escalation vs termination
- Pythia content filter model choice (small local LLM on Athena?) and false-positive tuning
- Backend-specific jailbreak testing — does the chosen plugin's LLM actually resist injection under adversarial content? Red-team checklist per plugin.

---

## 12. Argus Observation Channel Authenticity

**Threat:** Argus detects silent agents but its telemetry source is agent-produced (Discord posts, self-reported status). An agent that keeps posting innocuous updates can evade Argus while doing arbitrary work.

**Phase: 1 (k3s API + sidecar heartbeat), 2 (MCP broker push events).** Phase 1 Argus logic (bundled into Minos) uses the k3s API for liveness and a dedicated Argus-sidecar container for stall detection. MCP broker push events for guardrail breaches require the JWT MCP broker layer (Phase 2), so they land then.

**Status: Addressed architecturally.** `architecture.md §7` defines authoritative, non-forgeable telemetry sources: the k3s API (liveness), MCP broker push events (guardrail breach and call cadence), and a dedicated Argus-sidecar container that emits heartbeats independent of the worker backend (stall). Task thread posts remain narration, not telemetry — Argus decisions do not depend on them.

**Remaining access-control work:** how MCP brokers and Argus-sidecars authenticate their push events to the Argus ingest endpoint. Phase 1 uses a bearer-token check; Phase 2 moves to the JWT mechanism from §6 (MCP Broker Authentication).

---

## 13. Phase 1 Cost Ceiling

**Threat:** Phase 1 has no non-forgeable in-system token-budget signal (see §5 and `architecture.md §7 Phase 1 budget posture`). Combined with the shared `claude-code` credential (§3 Phase 1 exception) and the `@mention` respawn trigger (`architecture.md §8`), a runaway condition — an injection-driven loop, a misconfigured respawn, or hostile third-party comments on a repo accepting outside contributors — scales Anthropic spend linearly with nothing in Daedalus stopping it.

**Phase: 1 (outer boundary), 2 (in-system cap).** Phase 1 relies on an out-of-system ceiling configured at the provider. Phase 2 brings Apollo online with non-forgeable token counts from provider responses, closing the in-system gap.

**Status: Addressed out-of-system for Phase 1 as a deployment prerequisite.** The Anthropic workspace-level **spend cap** must be configured in the Anthropic console before Phase 1 is considered operational. This is documented in `environment.md §3 (Anthropic)` alongside other homelab-specific deployment settings. Argus's wall-clock cap remains the primary in-system runaway signal in Phase 1; the spend cap is the durable outer bound.

**Remaining implementation work:**

- Spend-cap value selection for the specific homelab deployment (one sensible ceiling per month that still allows expected operator workloads)
- Alerting path when the spend cap is approached — Anthropic webhook or polled console API into Hermes admin channel
- Phase 2 Apollo per-project and per-task caps: how they compose with the provider-side workspace cap (inner caps should fire first)
- Incident response for "spend cap tripped" — the state where the operator's Anthropic workspace is throttled and every pod's next call fails

---

*This document tracks security and access-control design items pending resolution. It will be updated as items are resolved or new gaps are identified.*
