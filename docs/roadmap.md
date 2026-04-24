# Project Daedalus — Roadmap

*Version 0.1 — Draft*

---

## Purpose

This document is the authoritative phasing for Project Daedalus. `architecture.md`, `security.md`, and `environment.md` derive their "Phase N" annotations from this file. When a component's phase assignment changes, it changes here first, then the other docs are updated to match.

The goal of the roadmap is to ship a working OpenClaw replacement that solves the pod-per-branch isolation and cross-run memory problems OpenClaw cannot, without dragging the full broker fleet and hardening surface in on day one. Controls that exist to defend against threats this deployment does not yet face are deferred explicitly rather than implemented prematurely.

---

## Scope Anchors

- **Phase 1 is MVP.** The system must replace OpenClaw end-to-end when Phase 1 ships. Anything Phase 1 lacks that OpenClaw has is a regression, not a deferral.
- **Single operator, single project, single surface.** Phase 1 does not support multiple admins, multiple projects, or multiple communication surfaces. These are Phase 2/3 expansions.
- **Single private repo with trusted contributors.** Phase 1 targets one private repository whose contributor set is the operator and anyone the operator directly invites. Outside-contributor PRs, public-repo operation, and repos with a broader contributor set are Phase 2+ — the `@mention` respawn trigger, the review-feedback-as-untrusted path, and the Mnemosyne cross-run injection tolerance all rest on this precondition. A repo that accepts PRs from arbitrary GitHub users is not Phase 1 ground.
- **Local trust inside Crete.** Phase 1 relies on network isolation within Crete rather than cryptographic broker auth. Nothing Phase 1 exposes outside Crete's edge beyond Cloudflare Tunnel ingress.
- **Postgres LXC is a single point of failure for the control plane.** Phase 1 is fail-silent on Postgres loss — Argus cannot persist state transitions, queued tasks cannot dispatch, and the operator notices via Proxmox-level VM health (not via Daedalus itself). Asclepius (Phase 3) adds a Daedalus-native alert; until then, Postgres uptime is treated as a homelab-operations concern.
- **Hydra is dropped.** Labyrinth is a single-node k3s cluster for the foreseeable roadmap. If multi-node capacity becomes necessary later, it is a new phase, not a backlog item.

---

## Phase 1 — MVP

Goal: replace OpenClaw with a system that solves branching isolation and persistent memory. Operable by one person, talking to one surface, working on one project.

### Infrastructure

- **Crete** — Minisforum MS-01 Proxmox host, ZFS mirror on 2× 1TB NVMe, firewall rules gating each guest's vNIC
- **Minos VM** — control plane services
- **Postgres LXC** — shared database with pgvector, schemas for Minos, Argus-state, and Mnemosyne
- **Labyrinth k3s VM** — single-node k3s cluster for Daedalus and Iris pods; solves the pod-per-branch isolation OpenClaw struggles with
- **Ariadne VM** — Vector + Loki log archive

### Services on the Minos VM

- **Minos core** — project config, task registry, agent lifecycle, GitHub App token minting, webhook handling
- **Argus logic bundled into Minos** — budget caps, stall watchdog, k3s delete on breach. Not a separate service yet. Co-located because the extraction buys nothing until the broker fleet lands in Phase 2.
- **Hermes** — messaging broker with one surface plugin (Discord — the surface OpenClaw currently uses). In-process; subprocess isolation and additional surfaces (Telegram, Slack, Teams, Matrix) are Phase 2.
- **Cerberus-minimal** — webhook ingress as a library inside Minos, not a standalone broker. One ingress path (Cloudflare Tunnel) and one verifier (GitHub HMAC + delivery-ID replay). Becomes a real pluggable broker in Phase 2 when the second verifier lands.
- **Mnemosyne** — Postgres + pgvector. Full run records, context injection at pod spawn, semantic lookup via MCP, sanitization pass on persistence. Fact-extraction pipeline ships simple; Phase 2 refines.

### Pods in Labyrinth

- **Daedalus agent pods** — worker backend plugin interface; Phase 1 implementation invokes the `claude-code` binary inside the pod. The binary manages its own Anthropic connection and credentials — Minos injects the operator-configured Claude credential (API key or OAuth token) into the pod's environment at spawn; no Anthropic traffic flows through a Daedalus-managed broker. Pod-per-task, one agent per feature branch, isolated workspace per pod.
- **Iris conversational pod** — long-running pod; single entry point for operator interaction. Translates natural-language requests into Minos commissions and state queries under the admin's identity. Backed by an Ollama-hosted model on Athena (not an external LLM); Iris's inference path is Labyrinth → Athena's Ollama port, same network shape agents already use for local inference.
- **Thread sidecar** — posts status/progress directly to the configured surface via Hermes.

### Auth posture

- **Hardcoded admin** — a single `(surface, surface_id)` tuple in Minos config. No pairing flow, no capability model, no identity registry beyond "is this the admin or not."
- **Iris forwards surface-verified identity** — the Hermes plugin delivers the message with the surface's user ID attached; Iris passes that through to Minos; Minos checks against the admin config. Trivial for one user but establishes the pass-through shape so Phase 2's identity model drops in cleanly.
- **Pod → Minos** uses a bearer token on a local HTTP API — no JWT MCP broker layer yet.
- **GitHub App** with per-task installation tokens (1-hour TTL, scoped to the single repo the task targets). This is low-lift and solves real push-scope problems; kept in Phase 1.
- **Claude credential injection** — operator's `claude-code` credential (Anthropic API key or OAuth token) resolved via the configured secret provider and injected into the Daedalus pod's environment at spawn. The credential is held at the deployment scope (one operator's subscription), not per-project or per-task, because Phase 1 is single-project. Because the same credential is fanned out to every pod, Phase 1 explicitly relies on the **Anthropic workspace spend cap** (configured in the Anthropic console, documented in `environment.md §3`) as the outer boundary on runaway cost — Apollo closes the gap in Phase 2.
- **Cross-thread posting enforcement in Hermes** — task_id → thread_ref lookup in Minos's registry, pods don't supply thread parameters. Kept because it's cheap and Iris benefits from it.

### Explicitly deferred from Phase 1

- Apollo external-LLM broker — deferred cleanly because no pod calls an external LLM API through Daedalus-managed plumbing in Phase 1. The `claude-code` binary in agent pods manages its own Anthropic connection; Iris uses Athena local inference. Apollo lands in Phase 2 when a second provider or centralized usage tracking becomes useful.
- Proxmox MCP broker and `infra` task type — Phase 1 ships code and inference-tuning tasks only. Infra tasks (Proxmox/Terraform changes) land in Phase 2 with the Proxmox broker; Phase 2 research will first check whether an existing community Proxmox MCP fits the Daedalus broker contract before writing one.
- Hecate credentials broker — Phase 1 uses Minos-push injection into pods and broker subprocesses instead. Hecate lands in Phase 2 alongside JWT MCP broker auth, enabling in-pod credential refresh and collapsing the Phase 1 "Minos is sole caller" push model into a standard JWT-authenticated pull broker.
- JWT MCP broker authentication — bearer tokens on local HTTP suffice.
- Trust boundary contract formalized in the plugin interface — agents still receive user/file content but no explicit trusted/untrusted framing primitive.
- High-blast capability confirmation tokens.
- Pairing flow, capability-based authz, identity registry, role bundles, revocation semantics.
- Break-glass session minting — operator uses kubectl directly from the Minos VM.
- Trust-boundary Mnemosyne source tagging — sanitization runs; untrusted-source tagging is Phase 2.
- Calico CNI and NetworkPolicy layering — default flannel is accepted for Phase 1 since there is one pod class that matters (Daedalus) and Iris, both running trusted plugins.
- Charon egress proxy — Proxmox firewall IP-allowlist handles egress.
- Asclepius — Proxmox native + systemd watch handles VM/service health.
- Pythia, Talos, Minotaur, Typhon — no research, QA, red-team, or chaos pods.
- Multi-project registry — single-project configuration is hardcoded.
- Mnemosyne pluggable backends — ship pgvector only. SQLite reference stays in the repo for local dev but is not a deployment target.
- Hermes subprocess isolation, multi-plugin, message signing.
- Cerberus as a standalone broker with plugin layers.

### Phase 1 acceptance

- Operator posts a command on the configured surface; Minos commissions a pod; the agent works, opens a PR, signals awaiting-review; Minos hibernates; a review event respawns with Mnemosyne context; the task reaches a terminal state.
- Iris answers "what's running?" and "start a task for X" on the same surface.
- Run records persist across pod teardown; context injection from prior runs demonstrably primes a new run.

---

## Phase 2 — Broker layer, pod-class expansion, and hardening

Goal: extract the broker fleet, add the pod classes that turn Daedalus from "one agent per task" into a coordinated team, and add the security controls Phase 1 deferred. This phase is triggered when any of the following becomes true:
- A second surface (Slack, Teams, Telegram, Matrix) needs to coexist with the Phase 1 surface
- A second operator needs non-admin access (commissioners, observers)
- A second LLM provider (OpenAI, Google) is needed alongside Anthropic
- Prompt injection resistance becomes a concern (e.g., agents start reading outside-contributor PRs)
- Review volume grows past what the operator can keep up with as a human-only first pass (Momus trigger)
- Backlog coordination across multiple pod classes becomes load-bearing (Themis trigger)

### Broker extraction

- **Apollo** — external LLM broker with per-provider plugins, non-forgeable usage tracking, per-project rate limits, audit to Ariadne, subprocess isolation per provider
- **Proxmox MCP broker** — lands alongside the `infra` task type. Design first checks whether an existing community Proxmox MCP meets the Daedalus broker contract (JWT auth, scope enforcement, Ariadne audit); only written in-house if no fit. Enables the `task.commission.infra` capability and Minos's per-project Proxmox token injection (`architecture.md §6` Other credentials).
- **Hecate** — credentials broker, fronts the secret provider, enforces Minos-set per-credential ACLs on JWT-authenticated fetches from pods and Minos-VM broker subprocesses. Required for in-pod credential refresh (tasks that outlive the GitHub App installation token's 1-hour TTL) and for cleanly serving credentials to the newly-extracted plugin subprocesses of Hermes/Cerberus/Apollo. Depends on JWT MCP broker auth (below) — shipping Hecate under Phase 1's bearer-token check would make the system's highest-value target the weakest-authenticated.
- **Hermes** grows subprocess isolation, additional surface plugins, credential rotation via SIGHUP, and inbound-message replay on Minos recovery (timestamped stream delivered to affected pods so agents can decide whether to re-plan or `request_human_input`)
- **Cerberus** becomes a standalone broker with pluggable ingress + verification plugin layers
- **Argus** extracts from Minos into its own service with push-event ingest (signed audit events from every broker), drift detection deferred to Phase 3

### Pod-class expansion

- **Themis** — project management pod. Owns the backlog, decomposes epics into `task_type` envelopes per `architecture.md §8`, commissions work through Minos, and serves as the default routing point for Argus escalations. Does not replace Minos's control-plane role. Long-lived pod, same lifecycle shape as Iris.
- **Momus** — code review pod. Runs on every Daedalus-opened PR as automated triage before human review. Two-stage: local tier (`qwen2.5-coder:32b` on Athena) full sweep on every PR, Apollo escalation for high-confidence findings and architectural drift. Expected 60–70% reduction in Claude calls per PR versus routing every review through Apollo. Comment-only GitHub scope; no approve / request-changes / merge.
- **Clio** — documentation pod. Generates READMEs, API docs, changelogs, and ADR formatting. `docs/**`-scoped GitHub App token; cannot mutate application code. Reactive mode (per merged PR) and scheduled rollup (drift reconciliation).
- **Prometheus** — DevOps / release pod. Owns pipeline config, version bumping, artifact publication, and environment promotion. Production promotion is a high-blast scope requiring an operator confirmation token.
- **Hephaestus** — architectural assistant. Drafts ADRs and surfaces coupling / topology concerns; produces draft artifacts only (`docs/adr/proposed/**`, `docs/reports/**`). Promotion to `docs/adr/accepted/**` is human-only. Claude-tier by default (Sonnet; Opus on operator request for genuinely ambiguous structural decisions).

### Hardening

- **JWT MCP broker authentication** — Minos signs per-pod tokens (Ed25519), brokers verify with public key, scopes enforced per call, audit to Ariadne
- **Trust boundary contract** codified in the worker backend plugin interface — trusted/untrusted framing primitives the plugin surfaces to its LLM
- **High-blast confirmation tokens** — scope-marked operations require human approval via the task thread before execution; confirmation bound to operation content (not just scope name)
- **Pairing flow + identity registry** — `(surface, surface_id)` identities, role bundles (`admin`, `commissioner`, `observer`, `system` — the last for internal pods that commission autonomously; see `architecture.md §11 Authority Model`), `/pair` approval flow (human roles only; `system` bypasses pairing), revocation with last-admin protection (human roles only — `system` has no last-identity protection), bootstrap from config for human admins and from the `system_identities` block in `deploy/config.json` for `system` identities
- **Break-glass session minting** — `break_glass.observe` and `break_glass.shell` capabilities, short-lived k3s ServiceAccount tokens, k3s audit log shipped to Ariadne
- **Mnemosyne untrusted-source tagging** — run records carry trust markers, context assembly preserves them across runs so injected content does not escalate to "trusted context" between runs
- **Mnemosyne fact-extraction maturity** — extraction pipeline refinement, per-project retention tuning, index rebuild tooling

### Phase 2 acceptance

- Paired second identity can commission within its capability set; admin can revoke
- A second LLM provider plugin works alongside Anthropic, with Apollo-reported usage
- A high-blast scope invocation from a pod blocks until the operator approves in-thread
- An injected prompt in a PR comment does not escalate to a high-blast MCP call
- Momus reviews every Daedalus-opened PR before it reaches the human reviewer, with review comments posted to the PR
- Themis decomposes a multi-task operator request from Iris into an ordered plan, commissions each task through Minos, and reports progress back through Iris
- An Argus escalation reaches Themis, is classified (halt / re-plan / escalate-to-human), and the corresponding Minos action fires without operator intervention for the re-plan case
- Clio opens a `docs/**` PR after a feature PR merges; Hephaestus opens a draft ADR in `docs/adr/proposed/**` without ever writing to `docs/adr/accepted/**`; Prometheus cuts a release and is blocked on production promotion pending the operator's confirmation token

---

## Phase 3 — Expansion

Goal: add the surfaces, pod classes, and operational tooling that broaden what Daedalus can safely do.

- **Pythia** research pods with the research MCP broker; prompt-injection surface annotation; per-task domain allowlists
- **Talos** QA/test pods with test-environment provisioning
- **Minotaur** red team pod — adversarial reasoning against Daedalus itself. Finds non-obvious attack paths, chains vulnerabilities, probes prompt injection against internal agents and MCP brokers. Claude-tier (Sonnet default, Opus for adversarial depth) — a local model running patterns is a second security scanner, not red teaming.
- **Typhon** sandboxed destructive test runner — chaos-engineering counterpart to Minotaur. Intentionally breaks infrastructure and code paths inside isolated workspaces to validate recovery automation. Minotaur breaks agents; Typhon breaks infra.
- **Athena Development Sandboxes** — Athena MCP sandbox surface (`sandbox.create`/`destroy`/`exec`), launchd-managed per-sandbox users, allocated port ranges. Lands in Phase 3 rather than Phase 2 because per-pod source scoping for sandbox reachability depends on the Calico/NetworkPolicy layer, which is itself Phase 3.
- **Charon** egress proxy with SNI-passthrough, per-pod-class allowlists, per-task egress additions
- **Asclepius** health monitor in its own LXC; polls every Crete service; alerts via Hermes; MCP surface for operator queries
- **Multi-project registry** — project-id-scoped credentials, egress extensions, per-project identity restrictions, per-project worker-backend overrides
- **Hermes message signing** — Argus-originated escalations signed, trusted UI verification path
- **Calico CNI + NetworkPolicy layering** — per-pod-class ingress/egress rules, default-deny pod-to-pod, layered precedence with Proxmox firewall and Labyrinth host firewall
- **Argus drift detection** — injection-pattern sequences, scope-drift against task brief, unusual egress patterns
- **Post-termination workspace snapshots** — CSI snapshotter, snapshot-fetch break-glass surface, per-project retention
- **Iris admin-surface expansion** — conversational access to every operator action as Minos's admin API grows

No Phase 3 acceptance gate is written yet — items here are additive and can ship independently as each pull lands.

---

## Dropped from earlier drafts

- **Hydra** (multi-node k3s expansion of Labyrinth) — removed from the roadmap. Labyrinth is single-node. If multi-node becomes necessary, it gets its own phase, planned against actual capacity numbers, not pre-designed against speculative ones.
- **Phase 1 deliverables that were overreach**: the JWT broker authentication, trust boundary contract, high-blast confirmation, pairing flow, Iris admin-surface parity, Calico CNI, Argus as a separate service, multi-project registry, pluggable provider stacks for Apollo/Hermes/Cerberus — all slid to Phase 2 or Phase 3 where they belong.

---

## Notes on deferred items

A deferred item is not a backlog item. Deferring means the roadmap has made an explicit choice not to build it now; the security or operational property it provides is not claimed to exist until its phase ships.

When referring back to an architecture or security doc that mentions a deferred feature, assume the feature is absent unless the Phase annotation says otherwise. `security.md` items marked "Addressed architecturally" remain so only in the sense that the design for them exists — not that Phase 1 implements them. The implementation lands in the phase this file assigns.

---

*This document is the authoritative source for phase assignments. Update here first, propagate to `architecture.md §21` and affected section tags, then update `security.md` and `environment.md` cross-references.*
