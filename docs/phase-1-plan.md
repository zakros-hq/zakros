# Project Daedalus — Phase 1 Plan

*Version 0.1 — Draft*

---

## Purpose

This document decomposes `roadmap.md §Phase 1` into an ordered, slice-based build plan. It is the authoritative sequencing document for Phase 1 implementation. When `roadmap.md` changes Phase 1 scope, this document is updated to match; when implementation diverges from this plan, update the plan rather than letting it rot.

The planning method is slice-by-acceptance: work backward from the Phase 1 acceptance gate to find the minimum path, then build forward in increments that each prove one gate bullet.

---

## 1. Phase 1 Acceptance Gate

From `roadmap.md §Phase 1 acceptance`:

1. Operator posts a command on the configured surface; Minos commissions a pod; the agent works, opens a PR, signals awaiting-review; Minos hibernates; a review event respawns with Mnemosyne context; the task reaches a terminal state.
2. Iris answers "what's running?" and "start a task for X" on the same surface.
3. Run records persist across pod teardown; context injection from prior runs demonstrably primes a new run.

Every slice below closes one or more of these bullets.

---

## 2. Structural Decisions

### Language: Go

Daedalus is a systems/orchestration codebase — the architectural neighbors are Kubernetes, Vault, Consul, Nomad, Traefik, containerd. Go is the default language for Phase 1 for these maintainability reasons:

- **Static single-binary deploy.** One file per service, `systemctl restart` is the release; no per-service venv management, lockfile-per-service, or Python-version drift.
- **Compile-time safety over mutating shape.** Task envelope schema, scope tables, JWT claim shapes, and broker registry will all mutate across phases. Go's structural typing catches renames and shape changes at compile time; the single-operator homelab cannot rely on Python's runtime-caught shape errors as a safety net.
- **Concurrency maps to the actual problem.** k3s pod-phase watching, GitHub webhook dispatch, broker-subprocess supervision, Argus heartbeat ingest, Postgres connection pooling — goroutines + channels are the idiom.
- **Smaller operational surface.** Labeled binaries with legible RSS vs. Gunicorn/uvicorn/worker process trees.
- **Python-favorable carve-outs are thin in Phase 1.** Mnemosyne is Postgres writes, pgvector ORDER BY, and regex redaction. Embedding calls are HTTP to Athena. No in-process ML.
- **Polyglot doubles maintenance cost.** Two CI pipelines, two test frameworks, two release processes. One language, even if locally suboptimal for a narrow component, is cheaper to maintain at one-operator scale.

**Exceptions permitted:**
- Pod-image worker backends are language-agnostic (the plugin contract is HTTP/MCP + subprocess exit code). Claude Code is Node; any future plugin's host language is its author's choice.
- Scripts and one-off tooling in `scripts/` may be shell or Python.

**Revisit triggers for the Go default:**
- Phase 2 Apollo landing — reassess if the provider plugin ecosystem is Python-concentrated enough to warrant polyglot.
- Any component that requires in-process ML inference or embedding (none in Phase 1).

### Repository: monorepo

Single repository, structure per `architecture.md §20`. Maintainability reasons:

- **Cross-cutting code is load-bearing.** Task envelope schemas (`schemas/`), provider interface, JWT signing library, plugin interface contract, broker auth middleware — every service depends on these. Monorepo makes cross-cutting changes atomic and reviewable in one diff.
- **One operator, one deployment.** There is no independent release cadence or independent team to justify multi-repo coordination cost.
- **Go workspaces handle the "multi-module in one repo" shape** without splitting history.
- **CI blast-radius control** via per-module test isolation and `go build ./...` with build caches; no need for multi-repo to keep CI tractable.

**Revisit trigger:** if a broker (Hecate or Apollo most likely) acquires third-party consumers in Phase 2+, extract that broker to its own repo *then*, not preemptively.

### Build order and dependencies

```
       A → (B ∥ D) → C → E         (code slices)
Prereqs ·········································→ (parallel track; must complete before any slice's acceptance checkpoint)
```

- **Prerequisites** (§3) runs in parallel with code work, not as a gate on it. Code is developed on the operator workstation against local substrates (Postgres in Docker, `k3d` or `kind` for Kubernetes, file-backed secret provider). VMs are only required for a slice's acceptance checkpoint — the end-to-end smoke test on the real Crete deployment. Prereqs must be complete by then, but not before coding starts.
- **A** is the critical-path substrate for the code graph; nothing else builds without it.
- **B** and **D** are parallel-safe once A is done — they touch different subsystems.
- **C** depends on B because the hibernate/respawn round-trip needs a review-event webhook to fire, which requires Cerberus.
- **E** depends on C because Iris's `memory.lookup` requires Mnemosyne.

---

## 3. Prerequisites — Crete host and VMs

Prerequisites is a parallel track, not a gate on code work. Slice A code develops on the operator workstation against local substrates; Prerequisites must be complete by Slice A's acceptance checkpoint (when the first real deploy to Crete happens), but coding can start immediately. This section is environment work, not Daedalus code: a one-time manual host install followed by automated Terraform provisioning modeled on the sibling `homelab/` repo.

### 3.1 Manual Proxmox host install (Crete)

Hypervisor setup is one-time and intentionally out of scope for automation — the `homelab/` repo assumes Proxmox already exists, and Daedalus assumes the same. These steps land on Crete before any Terraform runs.

**Hardware prep:**
- Minisforum MS-01 racked per `environment.md §1`
- Two 1TB NVMe drives in the first two M.2 slots (third M.2 reserved)
- Network uplinked to the homelab switch on the trunked VLAN port per `environment.md §2`

**Proxmox VE install:**
- Proxmox VE 9.x installed from USB; ZFS RAID-1 mirror across the two NVMe drives as the root and VM-storage pool; hostname `crete`
- Management IP on the homelab management VLAN; confirm headless reachability after reboot
- `apt update && apt dist-upgrade`; switch to the no-subscription repo; disable the subscription-nag dialog if desired
- ZFS scrub schedule reviewed before production data lands on the pool

**Networking:**
- Proxmox bridge `vmbr0` bound to the uplink NIC with VLAN-aware mode enabled
- All VLAN tags used by Daedalus guests (management, services) configured on `vmbr0` — the guests attach via tagged subinterfaces in Terraform
- Specific VLAN IDs are a Phase 1 open question per `architecture.md §23`; confirm them before Terraform runs
- Edge firewall (pfSense or equivalent) has a rule allowing admin access to Crete from the operator workstation VLAN

**Terraform access:**
- Dedicated Proxmox API user (`terraform@pve`) with role permissions for VM/LXC lifecycle, SDN, and storage operations per `homelab/terraform/` conventions
- API token generated for that user and stored in the operator's workstation secret store (never committed)
- Operator workstation SSH public key added to root's `~/.ssh/authorized_keys` on Crete (the `bpg/proxmox` provider drives some actions over SSH)
- Test: `pveum user list` returns the Terraform user; `ssh root@crete pveversion` works from the workstation

**Baseline storage:**
- ZFS dataset for VM disks confirmed (`rpool/data` is fine as default)
- ISO/cloud-image storage pool confirmed (`local` is fine as default)
- Third M.2 slot remains unallocated per `environment.md §1`

**Explicitly out of scope for Daedalus:**
- Off-host backup (`environment.md §1`: Proxmox snapshots are the recovery floor; external backup is not in Daedalus scope)
- VLAN/SDN creation on Crete when it does not already exist — either provisioned by the `homelab/` repo's `modules/network/` or created manually; Daedalus Terraform references VLANs rather than declaring them

### 3.2 Terraform VM provisioning

Daedalus ships a `terraform/` directory at the repo root patterned on `homelab/terraform/`, scoped to the four Daedalus guests and nothing else. The full homelab stack (UniFi provider, Vultr, Cloudflare, SDN management, Ansible inventory seeding for the whole lab) is explicitly not copied in — Daedalus provisions what Daedalus owns.

**Repo layout:**
```
terraform/
  main.tf                     — module instantiations
  provider.tf                 — bpg/proxmox provider config
  variables.tf                — inputs (Proxmox endpoint, credentials, VLAN refs)
  outputs.tf                  — VM IPs, SSH user, inventory yaml
  vm-configs.tf               — Daedalus guest definitions
  vars.auto.tfvars.example    — sanitized template; real tfvars gitignored
  modules/
    proxmox-vm/               — VM module, adapted from homelab
    proxmox-lxc/              — LXC module (homelab has VMs only)
```

**Provider and credentials:**
- `bpg/proxmox` ~> 0.78 to match homelab
- Endpoint points at Crete's management IP; `ssh.agent = true` for actions requiring SSH
- Credentials loaded from `TF_VAR_*` environment variables at apply time — never in the repo. Wrapper script in `scripts/` is acceptable for ergonomics as long as it sources from the workstation secret store, not checked-in files
- Pre-commit hook blocking secrets in `vars.auto.tfvars`, same as homelab

**Guest definitions (from `architecture.md §4 VM Inventory`):**

| Guest | Type | vCPU | RAM | Disk | VLANs | Base image |
|---|---|---|---|---|---|---|
| `minos` | VM | 2 | 8GB | 50GB | mgmt, services | Ubuntu 24.04 cloud image |
| `postgres` | LXC | 2 | 4GB | 50GB | mgmt, services | Debian 12 template |
| `labyrinth` | VM | 4 | 16GB | 200GB | mgmt, services | Ubuntu 24.04 cloud image; nested virt enabled |
| `ariadne` | VM | 2 | 4GB | 100GB | mgmt, services | Ubuntu 24.04 cloud image |

VLAN references live as variables so specific VLAN IDs can land without editing `vm-configs.tf`.

**LXC handling:**
- Homelab's `modules/proxmox-vm/` is VM-only; Daedalus adds a sibling `modules/proxmox-lxc/` using `proxmox_virtual_environment_container` for the Postgres guest
- LXC cloud-init equivalent (hook scripts or `user_data`) provisions initial user, SSH key, baseline packages — same downstream contract as VM cloud-init

**Cloud-init baseline (all guests):**
- Non-root admin user with operator's SSH public key injected
- Timezone, NTP, locale set from Terraform variables
- `apt update && apt upgrade` on first boot; unattended-upgrades enabled
- UFW or nftables default-deny inbound except SSH (plus the guest's Daedalus-specific ports once Slice A lands the services)

**Inventory output:**
- `terraform output -raw ansible_inventory_yaml > inventory.yaml` produces an Ansible-shaped inventory mirroring `homelab/` even if Phase 1 does not adopt Ansible for post-provision (4-VM scale is within shell-script reach)

**Apply flow:**
- `terraform init` — first run on operator workstation
- `terraform plan` — review
- `terraform apply` — provision all four guests
- `terraform output` — confirm reachability and note IPs for Slice A's software-install tasks

### 3.3 Exit criteria for Prerequisites

- Four guests reachable over SSH as the operator admin user (`ssh minos hostname`, etc., return expected names)
- Proxmox UI shows all four with correct VLAN attachments and resource sizing per the table above
- `terraform plan` on a second run shows no drift
- Operator workstation has the Proxmox API token, SSH keys, and `TF_VAR_*` wrapper ready to re-run Terraform on demand

Slice A starts from this point.

---

## 4. Slice A — "a pod can do a task"

**Proves:** commission → pod work → PR. (Acceptance gate bullet 1, first half.)

**Scope:** land the minimum code path that lets Minos commission a pod via CLI or HTTP and produce a pull request. No Discord, no hibernation, no Mnemosyne, no Argus enforcement.

**Local dev substrates.** Code tasks 4–9 below have no VM dependency and develop entirely on the operator workstation. Infrastructure tasks 1–3 (Postgres, Vector+Loki, k3s) develop against local equivalents — Postgres in Docker, a local Vector+Loki stack in Docker, `k3d` or `kind` for Kubernetes. The real Postgres LXC / Ariadne VM / Labyrinth VM installs happen when Prerequisites (§3) is complete; the acceptance checkpoint runs against those. Until then, everything is a `make dev` away.

### Tasks

1. **Postgres install + schemas** (Postgres LXC)
   - Install Postgres on the LXC provisioned in §3.2
   - Create schemas `minos`, `argus`, `mnemosyne`, `iris` per `architecture.md §6 Recovery and Reconciliation`
   - Install the pgvector extension (used in Slice C; install now so migrations are ordered correctly)
   - Migration tooling chosen (golang-migrate, goose, or atlas — decide at implementation); first migration lands the Slice A `tasks` table in the `minos` schema

2. **Ariadne log stack install** (Ariadne VM)
   - Install Vector as the ingest shipper and Loki as the log store per `architecture.md §17`
   - Configure Vector on each Daedalus guest to forward stdout/journald to Ariadne's Loki
   - Query-side work (Grafana, ariadne MCP) is deferred; Phase 1 debugging uses direct LogQL

3. **k3s install** (Labyrinth VM)
   - Single-node k3s with default flannel CNI
   - Host nftables rules per `architecture.md §16 Network Isolation`
   - Proxmox-vNIC firewall allowlist per `architecture.md §16 Egress Granularity`
   - `kubectl` access configured on Minos VM (the only expected caller in Phase 1)

4. **Shared Go modules in monorepo**
   - `pkg/envelope` — task envelope schema types + JSON Schema validation
   - `pkg/jwt` — Ed25519 signing/verification (Phase 2 consumer; Phase 1 uses HMAC bearer, but design the package so Phase 2 substitution is drop-in)
   - `pkg/provider` — secret-provider interface (`Resolve`, `Rotate`, `Revoke`, `AuditList`)
   - `pkg/audit` — structured audit emitter; writes to stdout in JSON for Vector to pick up

5. **Secret provider: file-backed reference**
   - Implements the `pkg/provider` interface reading from a YAML/JSON file under the Minos config directory
   - Phase 1 shipping default per `architecture.md §22` MVP Blockers — Secret provider
   - Infisical provider is a Slice A stretch goal; not a blocker for the acceptance checkpoint

6. **Minos core — minimum viable**
   - Service binary under `minos/core/`, deployed as a systemd unit on the Minos VM
   - Single hardcoded project config loader
   - Single hardcoded admin identity loader
   - Task registry: CRUD on `tasks` table
   - Dispatch: accept an HTTP `POST /tasks` from CLI, compose task envelope, spawn a k3s pod, insert task row in `running` state
   - State machine: `queued → running → completed | failed` (no `awaiting-review` yet — that lands in Slice C)
   - Startup reconciliation per `architecture.md §6 Recovery and Reconciliation` (minimum: DB integrity + adopt/orphan pods)

7. **GitHub App deployment**
   - GitHub App created in the operator's GitHub account with permissions: repo contents rw, PRs rw, issues rw, metadata r
   - Private key stored via the secret provider
   - Installation-token minter in Minos per `architecture.md §6 Credential Handling`: 1-hour TTL, repo-scoped to single `repo_url` in task envelope
   - Branch protection verification at project-registration time per `security.md §5`

8. **Worker plugin interface + Claude Code plugin**
   - Plugin contract defined in `agents/plugin/` — entry-point binary receives task envelope on stdin or file mount, exits 0 on success
   - Claude Code plugin under `agents/claude-code/`: pod image with the `claude-code` binary, envelope-parsing entry, git clone, `claude-code` invocation with the brief, PR open via `gh` CLI or direct GitHub API
   - Memory-extraction hook at SIGTERM (Phase 1: stdout dump + workspace file list; Mnemosyne consumes this in Slice C)

9. **CLI dispatcher for bootstrapping**
   - `minosctl commission --project X --brief Y --repo Z --branch feature/...`
   - Short-circuits the Hermes/Discord path for Slice A testing

### Acceptance checkpoint for Slice A

- From Minos VM, run `minosctl commission ...` against a test repo
- Pod spawns in Labyrinth, clones repo, opens a PR, exits cleanly
- Task row transitions `queued → running → completed`
- Logs visible in Ariadne (Loki query)

---

## 5. Slice B — "operator loop closes"

**Proves:** operator commissions from Discord; PR-merge webhook drives task to terminal; summary posts to thread. (Acceptance gate bullet 1, hibernation deferred to Slice C.)

**Scope:** Hermes + Discord plugin + thread sidecar + Cerberus-in-Minos with Cloudflare Tunnel. Still no hibernation, no Mnemosyne, no Iris.

### Tasks

1. **Cerberus as a library inside Minos**
   - `cerberus/core/` — route table (Postgres-backed) and delivery-ID replay store
   - `cerberus/ingress/cloudflare/` — `cloudflared` configuration reference + plaintext forward to the in-Minos Cerberus handler
   - `cerberus/verification/github/` — HMAC verification with `X-GitHub-Delivery` replay protection

2. **Cloudflare Tunnel setup**
   - `cloudflared` service on Minos VM
   - Public hostname → `localhost:<cerberus port>` forward
   - GitHub App webhook URL pointed at the public hostname

3. **Hermes core (in-process)**
   - `hermes/core/` — minimal broker: maintains surface-plugin registry, `thread_surface` → plugin routing, cross-thread posting enforcement (task_id → thread_ref lookup) per `architecture.md §6 Communication Surfaces`
   - Runs in the Minos process in Phase 1 per `roadmap.md §Phase 1` services list

4. **Discord plugin**
   - `hermes/plugins/discord/` — bot connection via `bwmarrin/discordgo`, inbound message stream, outbound thread posts
   - Bot token resolved from secret provider
   - Outbound-only gateway (no webhook from Discord side needed in Phase 1; uses Discord's bot gateway)

5. **Thread sidecar**
   - `agents/sidecar/thread/` — MCP server running inside each pod as a sidecar container
   - Exposes `post_status`, `post_thinking`, `post_code_block`, `request_human_input`
   - Proxies to Hermes on Minos VM over bearer-token HTTP

6. **Minos — command intake**
   - Parse Discord messages matching the single-admin identity
   - `/commission` or natural-language intake (simple regex sufficient for Phase 1; Iris NL parsing is Slice E)
   - Dispatch path reuses Slice A's `POST /tasks` handler

7. **Minos — webhook handler**
   - `POST /webhooks/github` in Minos (verified by Cerberus library)
   - Handle `pull_request.closed` with `merged: true` → task finalization
   - Handle `pull_request.closed` with `merged: false` → task closed
   - Handle `pull_request_review` and `issue_comment` events → no-op in Slice B (respawn logic lands in Slice C)

8. **Summary posting**
   - On terminal transition, Minos composes summary and posts to the task thread via Hermes

### Acceptance checkpoint for Slice B

- Operator posts `/commission fix bug 123` in Discord
- Pod spawns, opens PR, thread sidecar posts progress to the same Discord thread
- Operator merges the PR on GitHub
- Minos receives the webhook, transitions task to `completed`, posts summary to thread

---

## 6. Slice D — "guardrails on" (parallel with B)

**Proves:** Argus wall-clock cap and stall detection terminate misbehaving pods; task threads get warning/escalation/termination posts.

**Scope:** Argus logic bundled into Minos per `roadmap.md §Phase 1 Services on the Minos VM`; Argus-sidecar container in each pod; no push-event ingest yet (Phase 2).

Landing D in parallel with B ensures every later slice runs under real guardrails. Both slices touch `Minos` but in non-overlapping packages (`minos/argus/` vs. `cerberus/` + `hermes/`); they can land in either order.

### Tasks

1. **Argus-sidecar container**
   - `agents/sidecar/argus/` — minimal Go binary that emits heartbeat POST to Minos's Argus ingest endpoint on a configurable interval (default: 30s)
   - Runs in every Daedalus pod alongside the worker backend and thread sidecar

2. **Argus logic in Minos**
   - `minos/argus/` package — per-agent state table in Postgres (`started_at`, `last_heartbeat_at`, `token_count_self_reported`, `mcp_call_count`, `phase`)
   - Rules engine: wall-clock cap, heartbeat-silence threshold, warning/escalation/termination tiers per `architecture.md §7 Guardrails`
   - k3s watcher: poll pod phase on short cadence

3. **Heartbeat ingest endpoint**
   - `POST /argus/heartbeat` on Minos
   - Bearer-token check (Phase 1 posture; JWT is Phase 2)

4. **Tiered response**
   - Warning → post to task thread
   - Escalation → ping admin on configured surface (Hermes), pause agent (Phase 1: best-effort — no pod-pause primitive; escalation defaults to termination if not human-acknowledged within timeout)
   - Termination → `kubectl delete pod` with 30s grace, post incident to thread

5. **Startup reconciliation and grace period**
   - Per `architecture.md §7 State Persistence and Recovery` — recovery grace period on Minos restart to suppress false-positive stall alerts

### Acceptance checkpoint for Slice D

- A test pod that sleeps past its wall-clock cap is terminated with a thread post
- A test pod whose Argus sidecar is killed is detected as stalled and terminated
- Termination event visible in Ariadne

---

## 7. Slice C — "memory persists across runs"

**Proves:** run records persist; `awaiting-review` hibernation + respawn with Mnemosyne context drives the task to terminal. (Acceptance gate bullets 1 full, 3.)

**Scope:** Mnemosyne core + `awaiting-review` state + respawn logic. Untrusted-source tagging is Phase 2 per `architecture.md §19 Secret Sanitization` — not in scope here.

### Tasks

1. **Mnemosyne — pgvector backend**
   - `mnemosyne/postgres/` — schema with `run_records`, `learned_facts`, `project_contexts` tables; vector columns for embeddings
   - Migration landed in Slice A's pgvector setup; schema DDL lands here
   - SQLite reference implementation under `mnemosyne/sqlite/` for local-dev and plugin-interface testing (not a deployment target per roadmap)

2. **Mnemosyne service**
   - `mnemosyne/core/` — service with two surfaces:
     - Internal API for Minos: `memory.store_run(run_record)`, `memory.get_context(project_id, task_type)`
     - MCP broker for pods: `memory.lookup(query, scope)`
   - Sanitization pass mandatory before persistence per `architecture.md §19 Secret Sanitization`
   - Fact-extraction pipeline: simple for Phase 1, refine in Phase 2

3. **Worker plugin — memory extraction at SIGTERM**
   - Claude Code plugin writes run record (conversation log, scratchpad summary, artifact list) to a shared volume on SIGTERM
   - 30s grace window before SIGKILL
   - Minos picks up the blob and forwards to `memory.store_run`

4. **Minos — hibernation on `awaiting-review`**
   - New state `awaiting-review` in the task state machine
   - Trigger: agent signals PR opened via worker interface
   - Action: call `memory.store_run`, `kubectl delete pod`, record `context_ref` for respawn

5. **Minos — respawn on qualifying review events**
   - Webhook handler (from Slice B) extends to handle `pull_request_review` with `state: changes_requested` and `issue_comment` with `@mention` of the agent
   - Respawn flow: new `run_id`, same `task_id`, resolve `context_ref` via `memory.get_context`, spawn fresh pod with injected context

6. **Hibernation TTLs**
   - Reminder threshold, abandonment threshold — defaults tracked in `architecture.md §23 Open Questions`; pick concrete values during Slice C and document in config

7. **Context injection verification**
   - Integration test: two-run task where run 2's log demonstrably references a decision from run 1

### Acceptance checkpoint for Slice C

- Operator commissions a task from Discord
- Agent opens PR, task hibernates (pod deleted)
- Operator requests changes on the PR
- Minos respawns a fresh pod; new pod's run log shows prior-run context primed its work
- PR eventually merges, task finalizes; both run records visible in Postgres

---

## 8. Slice E — "Iris talks"

**Proves:** Iris answers "what's running?" and "start a task for X" on the same surface. (Acceptance gate bullet 2.)

**Scope:** Iris long-running pod on Athena-hosted Ollama backend.

### Tasks

1. **Athena Ollama reachability**
   - Iris pod's network-policy allowlist (Phase 1: firewall rules) includes Athena's Ollama port (11434)
   - Specific model chosen, pulled to Athena in advance; model name configured for Iris

2. **Iris pod image**
   - Long-running pod spec with `daedalus.project/pod-class: iris` label
   - Backend: Ollama HTTP client, conversation state persisted to Postgres `iris.conversations` schema (keyed by surface + thread + user identity per `architecture.md §10 Conversation State`)

3. **Minos state API**
   - `GET /state/tasks`, `GET /state/queue`, `GET /state/recent` — read-only endpoints Iris consumes
   - Bearer-token auth; Iris holds a dedicated token

4. **Iris capabilities wired up**
   - `mnemosyne.memory.lookup` client — semantic search over project memory
   - `hermes.events.next` — long-poll inbound message delivery
   - `hermes.post_as_iris` — scoped reply posting

5. **Command intake**
   - `@iris` mention, DM, or `/iris` slash command trigger
   - Two primary intents for Phase 1: state query (answer from Minos state API) and commission (translate to structured commission + confirm + forward with admin identity)
   - Iris never manufactures identity — passes through the Hermes-delivered `(surface, surface_id)`

6. **Safeguards**
   - Admin-only in Phase 1 (hardcoded admin config check)
   - Argus sidecar deferred to Phase 2 per `architecture.md §10 Pod Configuration` — document the stall-detection gap
   - Trust-boundary framing best-effort in Phase 1 per `architecture.md §10 Pod Configuration`

### Acceptance checkpoint for Slice E

- Operator asks Iris "what's running?" in Discord → Iris replies with current task list
- Operator asks Iris "start a task to fix bug 456" → Iris confirms, commissions, and the rest of the Slice A-D path executes
- Run record for the commissioned task exists; Iris's own conversation state persists across Iris pod replacement

---

## 9. Cross-Cutting Concerns

### Testing strategy per slice

- **Unit tests** on every Go package touched.
- **Integration tests** per slice: a scripted end-to-end run that exercises the slice's acceptance checkpoint against a dev Postgres and a kind cluster (or dev k3s).
- **Manual smoke test** on the real Crete deployment at each slice's acceptance checkpoint before declaring the slice done.

### Observability baseline

- All services emit structured JSON logs to stdout (`pkg/audit`) picked up by Vector on each VM.
- Vector ships to Loki on Ariadne; manual LogQL queries suffice for Phase 1 debugging. An Ariadne MCP query surface is a Phase 1+ stretch goal (candidate: `grafana/mcp-grafana` per `build-vs-adopt.md`).

### CI

- One GitHub Actions workflow for the monorepo with per-module test invocation.
- Required checks: `go vet`, `go test ./...`, `golangci-lint`, `go build ./...`.
- Per-module Dockerfile builds for pod-image components (Claude Code plugin, Iris pod, sidecars).

### Config and secrets

- Minos config: YAML file under `/etc/minos/config.yaml`, pointed to by a systemd `Environment=` directive.
- Secrets never in config; config holds `credentials_ref` names that resolve through the configured provider.
- Project config (single project in Phase 1) lives alongside Minos config.

### Documentation updates during build

- Whenever an implementation decision clarifies or contradicts `architecture.md`, update that doc rather than letting implementation drift.
- Open Questions in `architecture.md §23` get resolved or re-scoped during the slice that forces the decision; track resolutions in commit messages and the affected doc.

---

## 10. Risks and Open Questions

### Risks

- **Go MCP SDK maturity.** The community `mark3labs/mcp-go` and `modelcontextprotocol/go-sdk` are production-used (github/github-mcp-server, grafana/mcp-grafana, hashicorp/vault-mcp-server are all vendor-official Go MCP servers). Low-probability risk; evaluate during Slice A's plugin-interface design.
- **Postgres LXC single-point-of-failure.** Accepted per `roadmap.md §Scope Anchors`; fail-silent posture documented. Operational risk sits with Proxmox-level monitoring until Asclepius lands in Phase 3.
- **Cloudflare Tunnel as trusted intermediary.** Phase 1 accepts TLS termination at Cloudflare's edge per `security.md §2`. Deployment prerequisite: the operator is willing to accept this exposure.
- **Shared `claude-code` credential.** Blast radius bounded by Anthropic workspace spend cap per `security.md §3 Phase 1 exception`. Spend cap configuration is a Phase 1 deployment prerequisite.

### Open questions (to resolve during the slice that forces them)

- **Slice A:** migration tooling choice (golang-migrate / goose / atlas)
- **Slice B:** specific Discord slash-command shape vs. plain-text intake
- **Slice C:** hibernation reminder/abandonment TTL defaults per `architecture.md §23`
- **Slice C:** `context_ref` payload shape — inline in envelope vs. shared-volume reference, threshold for switching
- **Slice D:** concrete budget defaults (token cap, wall-clock cap) per `architecture.md §23`
- **Slice E:** specific Ollama model chosen for Iris; footprint constraints on Athena

---

*This plan is authoritative for Phase 1 sequencing. Update it when scope changes in `roadmap.md §Phase 1`, when a slice completes, or when an implementation decision clarifies an open question.*
