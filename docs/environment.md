# Project Daedalus — Environment Catalog

*Version 0.1 — Draft*

---

## Purpose

`architecture.md` describes the system design. `roadmap.md` describes the phasing. This document catalogs the parts of the design that are bound to a specific homelab environment rather than to the architecture itself. If the project were deployed elsewhere, every item on this list would need a replacement — but the architectural components (Minos, Argus, Labyrinth, etc.) would not change.

This file exists so that:
- Environment-specific assumptions are explicit rather than embedded.
- Hardware, network, or external-service changes have a single place to update dependencies.
- Readers can distinguish "the design" from "the deployment."

---

## 1. Physical Hosts

### Crete — Minisforum MS-01

**Referenced in:** `architecture.md §3`, `§4`, `§21`

Specific hardware: Intel i9-13900H, 96GB DDR5, 2× 1TB NVMe (ZFS mirror), third M.2 reserved, multi-NIC (2× 2.5Gb + 2× 10Gb), Proxmox VE 9.x.

What Daedalus depends on (independent of the specific device):
- A hypervisor host that can run the Daedalus VMs and LXC containers listed in `architecture.md §4` with the stated resource allocations.
- Storage with redundancy sufficient to survive a single-drive failure of the VM pool.
- Proxmox snapshots for fast rollback. Off-host backup is not in the Daedalus scope; state is recoverable from external sources (GitHub, Infisical, the configured Hermes surface).

### Athena — Mac Studio M4 Max

**Referenced in:** `architecture.md §3`, `§5`

Specific hardware: Apple Mac Studio (part number Z1CD9LL/A), M4 Max SoC, 40-core GPU, 48GB unified memory, 1TB internal storage. Runs macOS; services as launchd daemons under the admin account.

**Planned expansion:** Athena is expected to grow into a Mac Studio cluster built on M5 Ultra hardware, interconnected over Thunderbolt 5 with RDMA (requires macOS 26.2 or later). The cluster will scale inference capacity and model residency but does not change Athena's architectural contract — it remains a passive oracle reachable on the documented service ports.

What Daedalus depends on:
- An inference node reachable on specific ports (Ollama 11434, embedding 8400, Qdrant 6333, whisper on-demand).
- The node does not host agents, workspaces, case data, or source code.
- The node does not initiate connections to Crete-hosted resources except for one-way log shipping to Ariadne (the documented carveout; outbound to external model registries is also permitted).

macOS / launchd is the current implementation; the architectural contract is port-level service availability, not the OS.

---

## 2. Network

### Homelab Switch and Trunked VLAN Port

**Referenced in:** `architecture.md §4` (Network)

Crete connects to the homelab switch over a single trunked port carrying all required VLANs. Inter-VM traffic stays on Proxmox virtual bridges; VLAN-crossing traffic traverses physical NICs.

Contract: Crete must be able to terminate multiple VLANs internally and expose them to VMs selectively.

### pfSense Firewall (Homelab-Wide, Out of Scope)

**Referenced in:** `architecture.md §4`

pfSense handles broader homelab VLAN policy and ingress routing. Daedalus does not depend on specific pfSense rules for its own isolation — the Proxmox firewall on Crete holds all Daedalus egress allowlist and inbound rules. A deployment without pfSense can rely entirely on the hypervisor firewall for Daedalus isolation.

Contract: none required for Daedalus. pfSense remains present in this homelab for reasons outside the project scope.

### 12-VLAN Homelab Architecture

**Referenced in:** `architecture.md §23` (VLAN assignment open question)

Specific VLAN topology of the homelab. Assignment for Crete is unresolved.

Contract: the architecture assumes VLAN-level network segmentation is available; specific VLAN IDs are deployment detail.

---

## 3. External Service Instances

### Infisical

**Referenced in:** `architecture.md §6`, `§22`; `security.md §3`

Specific Infisical instance serves as the homelab implementation of Minos's secret provider interface (`architecture.md §6`). Holds the secrets Minos injects into pods via machine identity.

**Phase 1 default is the file-backed provider**, not Infisical — this keeps a clean Phase 1 install self-contained on Crete with no dependency on an external secrets service. Both provider implementations ship in Phase 1 (see `architecture.md §22` MVP Blockers — Secret provider); Infisical is selected instead of the default when a homelab deployment already runs it. The durable rotation story (Hecate-fronted credential fetches with JWT ACLs) is Phase 2+; Phase 1 rotation under either provider is restart-driven.

Contract: Minos (Phase 1) or Hecate (Phase 2+) can obtain per-project, per-task credentials from a secret store at spawn time without human interaction, and can rotate them without disrupting running agents. Any provider implementation meeting this contract is a valid substitute. Named alternatives:

- **OpenBao** — Linux-Foundation fork of Vault 1.14 from before HashiCorp's 2023 license change; MPL-2.0, IBM-backed, API-compatible with HashiCorp Vault. Preferred over HashiCorp Vault OSS (BSL 1.1) when taking the Vault-API route, because MPL-2.0 governance avoids the BSL license risk. The Phase 2 Hecate broker's PoC target (`hashicorp/vault-mcp-server`, per `build-vs-adopt.md`) works unchanged against OpenBao.
- **HashiCorp Vault OSS** — BSL 1.1; acceptable for homelab self-host but carries governance risk if HashiCorp's licensing posture changes further.
- **AWS Secrets Manager, GCP Secret Manager, Azure Key Vault** — valid contract-wise but fail the self-containment property this homelab targets; not recommended for this deployment.
- **File-backed** — Phase 1 default, ships in-repo.

### Phase 1 Hermes Surface (Discord)

**Referenced in:** `architecture.md §6`, `§7`, `§8`; `roadmap.md §Phase 1`

Phase 1 configures **one** Hermes surface plugin — whichever the operator uses today (OpenClaw currently runs on Discord). Pods post via the thread sidecar (which proxies to Hermes); the bundled Phase 1 Argus logic escalates through Hermes to the task thread; summaries post through Hermes. Additional surfaces (Telegram, Slack, Teams, Matrix, etc.) are Phase 2 plugin additions.

Contract: a chat platform with bot support, per-thread or per-chat posting, and either webhook-capable inbound routing or an outbound-gateway/long-poll pattern for event delivery. The Hermes plugin contract accommodates both threaded and flat surfaces; Phase 1 ships threaded-only (Discord), and the flat-surface tradeoffs below apply once a Phase 2 flat-surface plugin (Telegram, iMessage, SMS) lands.

**Flat vs. threaded surface — Phase 2+ functionality tradeoff.** Daedalus assumes one task-thread per task. On threaded surfaces (Discord, Slack, Teams, Matrix), each task gets its own thread and cross-task message isolation is preserved by the surface. On **flat surfaces** (Telegram, iMessage, SMS) there are no threads — `thread_ref` collapses to the chat or conversation ID, and every task commissioned from that chat shares it. Consequences the operator must accept when choosing a flat surface:

- A user with access to the chat can read every task's narration, not just the one they commissioned
- Argus escalations, hibernation notices, and task summaries all land in the same chat and can drown routine work under incident noise
- `hermes.post_as_iris` binding degrades to "same chat as the inbound message" — the enforcement still prevents cross-chat leakage, but intra-chat cross-task mixing is unavoidable
- `@mention`-style addressing of specific tasks is surface-dependent and may not work

Phase 2 flat-surface plugins are supported; the project does not attempt to paper over the functionality gap when they land. Deployments that need per-task isolation should choose a threaded surface (Discord is the Phase 1 reference; Slack, Teams, Matrix land in Phase 2).

### GitHub

**Referenced in:** `architecture.md §6`, `§8`, `§22`; `security.md §5`

Daedalus assumes GitHub as the code host — PATs, webhooks, branch protection, PR events drive the agent lifecycle.

Contract: a Git host that supports scoped machine credentials, webhook delivery with HMAC signatures, and branch-level protection rules that Daedalus can treat as the enforcement surface for push restrictions.

### Anthropic Workspace (Claude API)

**Referenced in:** `architecture.md §6 Credential Handling`, `§7 Phase 1 budget posture`, `§16 Egress Granularity`; `security.md §3`, `§13`

Specific Anthropic workspace backs the operator's `claude-code` subscription. Phase 1 Daedalus pods invoke `claude-code` directly against this workspace; the API key or OAuth token is injected into every Daedalus pod at spawn.

**Required deployment settings (Phase 1):**

- **Workspace-level spend cap** configured in the Anthropic console. Phase 1 has no in-system non-forgeable token cap (Apollo is Phase 2); the spend cap is the durable outer boundary on runaway cost from injection, respawn loops, or a compromised pod. A Phase 1 install without a configured spend cap is operating without the bounded-cost property the security model assumes.
- **Alerting threshold** at a fraction of the cap (e.g., 50% and 80%) so the operator sees approach before tripping.

Contract: a Claude-family LLM provider with account-level spend controls and an API the `claude-code` binary can target. Apollo (Phase 2) replaces direct pod access with a broker-held credential, at which point the per-pod injection and its blast radius collapse.

### Cloudflare Tunnel

**Referenced in:** `architecture.md §6`; `security.md §2`

Specific Cloudflare account and tunnel serve as the homelab implementation of Cerberus's ingress-plugin interface. A `cloudflared` agent on the Minos VM establishes an outbound tunnel to Cloudflare's edge; Cloudflare presents a public URL and forwards authenticated requests over the tunnel. No inbound ports exposed at Crete's edge.

Contract: an outbound-tunneling ingress provider that terminates TLS upstream and forwards plaintext to Cerberus. Any equivalent service (Tailscale Funnel, ngrok, a reverse-proxy service, or a manually operated WireGuard + nginx pairing) is a valid substitute — it's the self-contained ingress property that matters, not the specific provider.

---

## 4. Adjacent Systems

### Worklab (Windows Server)

**Referenced in:** `architecture.md §8`

Named as the example workload that justifies VM-per-agent substrate in Phase 3.

Contract: the architecture reserves Hydra VM workspaces for full-OS tasks. The specific workload is environment-specific.

### OpenClaw

**Referenced in:** `architecture.md §21`, `§23`

Pre-existing agent implementation that Daedalus replaces. Phase 1 calls for migrating its behavior into the Minos/Argus separation.

Contract: migration-specific; not relevant to any new deployment.

---

## 5. What Is *Not* Environment-Specific

For clarity, the following are architectural decisions that do not belong in this catalog:

- Component taxonomy (Minos, Argus, Daedalus, Labyrinth, Athena as roles)
- MCP broker pattern and capability composition
- Pod-per-agent substrate in k3s
- Thread sidecar interface (`post_status`, `post_thinking`, etc.) proxied through Hermes
- Worker backend plugin interface
- Argus tiered response (warning / escalation / termination)
- Phased delivery structure

These are design choices that would carry over to any deployment.

---

*This catalog updates whenever a homelab-specific binding is added to, removed from, or changed in `architecture.md`.*
