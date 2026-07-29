# Field delta — 2026-07-28

Refresh of `project-review-2026-07.md` (survey date 2026-07-04), 24 days on. Not a re-survey: this only records what **changed**, what the prior survey **got wrong**, and what Athena being live changes about the design. Read the July 4 report first; it still stands except where contradicted below.

---

## TL;DR

1. **The headline claim from July 4 is no longer true.** "Self-hosted control plane + k8s pod-per-task + chat dispatch + scoped-credential broker exists nowhere else as one product" — **Centaur** (Paradigm + Tempo, open-sourced 2026-05-21, Apache-2.0 OR MIT) is exactly that combination, and it was already public six weeks before the survey ran. It was missed.
2. **Centaur's credential model is stronger than Hecate's**, not weaker. Its sandboxes hold *placeholder names only*; a per-sandbox proxy substitutes real secrets on the wire. Zakros's broker still hands the pod a real (scoped, short-TTL) token.
3. **kubernetes-sigs/agent-sandbox went from "watch list" to "adoptable" in three weeks** — v1beta1 API graduation (v0.5.0, 2026-06-24), v0.5.3 on 2026-07-23, a Red Hat downstream product build announced 2026-07-15, and gVisor + Kata examples. The July 4 "re-evaluate at Phase 3" deferral is now too conservative.
4. **Athena is live and is not what the Zakros docs say it is.** Every Zakros doc and three source comments describe Iris's future backend as "Athena Ollama." Athena is a native Swift/MLX daemon on `:7447` with no Ollama anywhere, and it serves `/v1/messages` — the Anthropic dialect Iris already speaks. The flip is a base-URL and bearer swap, not a port.
5. **Athena now enforces per-principal token budgets server-side** (its ADR 041), which invalidates `architecture.md:711`'s premise that local-Athena usage is only plugin-self-reported and therefore forgeable.

---

## 1. Correction: Zakros's combination is no longer unique

`project-review-2026-07.md §2` claims, across a ~30-product survey, that no product ships the full combination. **Centaur does.**

| Dimension | Zakros | Centaur |
|---|---|---|
| Control plane | Minos (Go), self-hosted | `api-rs` (Rust) + FastAPI control plane, self-hosted |
| Isolation | k3s pod-per-task, **plus hypervisor VM split** | k8s pod-per-session; Helm chart, warm pool, NetworkPolicies assume k8s |
| Chat dispatch | Discord via Hermes | Slack-native, thread-per-session |
| Credential model | Hecate/OpenBao mints scoped short-TTL tokens **into the pod** | per-sandbox `iron-proxy`; sandbox holds **only placeholder names**, proxy substitutes on the wire, host- and location-bound |
| State | Postgres + pgvector (Mnemosyne) | Postgres; step-level checkpoints, replayable event streams |
| Agent backends | `claude-code` (pluggable interface) | Amp, Claude Code, Codex/pi-mono |
| Egress | Proxmox firewall allowlist (Charon in Ph2) | all sandbox egress through the proxy pod |
| Durable workflows | none | sleep/resume/wait-for-event/child-agents, survives restarts |

Verified facts (GitHub API, 2026-07-28): 948 stars, created 2026-05-18, **commits landing today**, license **Apache-2.0 OR MIT**. It is alive, actively developed, corporate-backed, and permissively licensed.

Two consequences:

- **The moat argument weakens.** July 4 positioned OpenHands's Polyform-licensed enterprise plane as the gap Zakros exploits. Centaur has no such gap — it is fully permissive and ships the isolation + credential story in the open. "Zakros with a company behind it" already exists; it is Centaur, not OpenHands.
- **Centaur is the reference implementation to read before hardening H2b/Charon.** Its credential-substitution proxy is the design Zakros's Hecate should be measured against.

Also confirmed this pass: in the ecosystem's own orchestrator index, Centaur is the *only* other entry meeting self-hosted + k8s-isolation + chat-dispatch simultaneously. The July 4 finding was right about the shape of the gap and wrong about it being empty.

### What Zakros still has that Centaur does not

Worth stating plainly so the response isn't an overcorrection:

- **Hypervisor-level blast radius.** Control plane and workers on separate VMs under an operator-controlled firewall. Centaur is entirely in-cluster; a cluster-level escape reaches its control plane.
- **Subscription-OAuth economics.** Centaur's proxy model presumes BYO metered API keys. Zakros fanning one Claude subscription into pods at $0 marginal cost remains unmatched — and remains the piece with ToS ambiguity.
- **Cross-run episodic memory.** Mnemosyne's pgvector run records vs Centaur's transcripts + checkpoints (durable, but not episodic recall).
- **Guardrail supervision** (Argus heartbeats, stall/runaway detection) and a **conversational operator agent** (Iris) — Centaur documents neither.
- **Local inference appliance** (Athena) under the same trust boundary.

### What to steal

- **Credential substitution on the wire** — strictly better than minting a token the pod can read. Fold into the H2b/Charon design rather than a later slice.
- **Durable step checkpoints** — Zakros task state is coarser; a mid-task crash restarts work Centaur would resume.
- **Warm pool** — pod cold-start is a real UX tax on chat-dispatched work.

---

## 2. agent-sandbox: deferral is now too conservative

July 4 said "re-evaluate at Phase 3." Since then:

| Date | Event |
|---|---|
| 2026-06-24 | **v0.5.0 — API graduates `v1alpha1` → `v1beta1`**, with conversion webhooks for migration |
| 2026-07-09 | v0.5.1 |
| 2026-07-15 | **Red Hat announces a downstream product build**, positioned in its agentic AI stack, supporting the Kata `runtimeClass` via OpenShift sandboxed containers |
| 2026-07-17 | v0.5.2 — **gVisor-isolated deployment example**, Windows example, all-in-one manifest |
| 2026-07-23 | v0.5.3 — OperatorHub publication readiness (OLM bundle, CSV), faster leader-election handover, concurrent workers default 1 → 100, Go + Python SDK docs |

Repo: 3,323 stars, pushed today. CRDs are `Sandbox`, `SandboxTemplate`, `SandboxClaim`, `SandboxWarmPool`.

**Advisory:** v0.5.0 and v0.5.1 carry a status-wiping race on warm-started claims — go straight to ≥v0.5.2.

Assessment: a `v1beta1` API with a vendor product build behind it, an OLM bundle, and a documented warm-pool primitive is no longer "young OSS to watch." Zakros's dispatcher builds raw pods today; `SandboxClaim` + `SandboxWarmPool` covers pod lifecycle *and* the warm-start gap identified above, and the `runtimeClass` hook is how gVisor/Kata isolation arrives without Zakros writing any of it.

Recommendation: **pull the evaluation forward from Phase 3 to a spike alongside H2b** — a half-day, same shape as the Forgejo spike, with kill criteria (does `SandboxClaim` express Zakros's per-task envelope? does the warm pool survive the egress-allowlist requirement?). Adopting it deletes custom pod-lifecycle code rather than adding any, so the lazy path and the strategic path agree here. Note the Red Hat caveat: without Kata/gVisor the sandbox still shares the worker-node kernel — the CRD is a lifecycle win, not automatically an isolation win.

---

## 3. LiteLLM Agent Platform: verified real, momentum questionable

July 4 flagged this single-source and unverified, with a "verify before H2b design hardens" action. Verified now:

- Real. `BerriAI/litellm-agent-platform`, created 2026-05-07, **MIT** (not the restrictive license feared), 1,170 stars.
- Kubernetes sandboxes + Postgres session store + vault proxy, consuming a running LiteLLM gateway; model routing/cost/rate-limiting stay in the gateway layer.
- Still labelled **alpha public preview**, and **last pushed 2026-06-20 — five weeks stale** as of today.

Assessment: the overlap with Apollo + Labyrinth is real but the project is drifting. It does not threaten the H2b design and should not be adopted as load-bearing (per the July 4 consolidation-graveyard rule). Downgrade from "verify before H2b" to "watch." The interesting part is the layering idea — routing/cost/rate-limits in a gateway, isolation above it — which is exactly Apollo's split and is mild independent validation of it.

---

## 4. Athena is live, and the docs describe a system that does not exist

The user's note that "Athena exists now and is fairly complete" is the largest local delta, and it invalidates standing text in five Zakros docs plus three source comments.

**What the Zakros tree currently says** — every one of these is now wrong:

| Location | Claim |
|---|---|
| `architecture.md:1081`, `:1124` | Iris Phase 1 backend is "an **Ollama-hosted model on Athena**" |
| `architecture.md:1359` | "Athena **Ollama** inference (direct HTTP, not MCP-scoped)" |
| `roadmap.md:63` | "Iris uses Athena local inference" (Ollama framing) |
| `deploy/README.md:420` | "Phase 3 swaps backend to **Athena Ollama**" |
| `deploy/templates/iris-deployment.yaml:5` | "…to Athena Ollama" |
| `agents/iris/Dockerfile:4` | "Claude-backed inference (no **Athena Ollama** yet)" |
| `agents/iris/cmd/iris/main.go:8` | "Iris flips to local…" |
| `agents/iris/internal/iris/conversation.go:4` | "Athena/Ollama lands **when Athena is stood up**" |

**What Athena actually is** (verified against the live daemon's `/openapi.json` and the repo at `9c9b5264`, 2026-07-28):

- Native Swift/MLX daemon on `127.0.0.1:7447`. **No Ollama, anywhere.** 164 Swift sources, 42 ADRs.
- Two dialects: `/v1/*` (inference + data) and `/api/*` (control plane). Live routes include `/v1/chat/completions`, `/v1/embeddings`, `/v1/audio/transcriptions`, `/v1/models`, and — decisively — **`/v1/messages`** plus `/v1/messages/count_tokens` (ADR 042, Anthropic dialect parity).
- Bearer-token RBAC with roles, users, per-token surfaces (`/api/tokens`, `/api/roles`, `/api/users`).
- **Per-principal token budgets enforced server-side** (ADR 041): rolling-period accounting, 429 + advisory headers on breach, budgets surfaced on `/api/usage`.
- Self-describing: `GET /openapi.json`, no auth, always reachable.

### Consequence 1 — the Iris backend flip is nearly free

Iris already has `agents/iris/internal/iris/anthropic.go` and speaks the Anthropic Messages dialect. Athena serves `/v1/messages` in that same dialect. **Pointing Iris at Athena is a base-URL + bearer-token change, not a client rewrite** — no Ollama client, no second code path, and the "Phase 2/Phase 3 backend swap" framing across the docs is obsolete. This is now a small, well-scoped change rather than a phase-gated one.

### Consequence 2 — the budget-forgeability premise is stale

`architecture.md:711` records that budget enforcement uses "plugin runtime reports for local Athena calls," with Phase 1 token counts "forgeable by a compromised pod," and positions Apollo (H2b) as what makes usage non-forgeable. Athena's ADR 041 already enforces budgets **server-side, per principal**, and refuses over-budget requests with a 429 — a compromised pod cannot forge its way past a limit it does not control. For the local-inference path the guarantee Apollo was going to provide **already exists in the appliance**.

This does not delete H2b — Apollo still covers *external* providers, where Zakros holds the credential and the provider is the accounting authority. It does mean H2b's scope should be restated: local-Athena budget enforcement is a solved problem to *integrate with* (read `/api/usage`, honour the 429), not to rebuild.

### Consequence 3 — Athena is a standards exemplar in-house

Athena has 42 ADRs, a `CLAUDE.md` with binding canonical-pipeline declarations, and a machine-readable public surface. Zakros has **none of the three house artifacts** (July 4 §3, still true today). The gap is not a knowledge gap — the pattern is already implemented, reviewed, and working one repo over. Copy the shape.

---

## 5. Unchanged since July 4

Re-checked, no material movement, no action:

- **OpenHands** — 82.4k stars (was 79.4k), pushed today, MIT core with the Polyform enterprise plane intact. Still the loudest project in the space; still not shipping Zakros's credential-broker story. Demoted from "the strategic competitor" — that is Centaur now.
- **mem0** — build-vs-adopt "pass" holds; nothing changed the run-record-primary reasoning.
- **Claude Code Channels** — still the right thing to read before building more Hermes surface plugins. Unread.
- **Consolidation-graveyard dynamics** — the July 4 read (own the substrate, don't take young SaaS-adjacent OSS as load-bearing) survives contact with everything above, and LiteLLM Agent Platform's five-week stall is a fresh data point for it.

---

## 6. Action list (delta only — July 4's list otherwise stands)

### P0 — unchanged and still the biggest risk

The July 4 P0s have not moved: no Go toolchain on this workstation, **Slice H2a still uncommitted after ~3 months**, gitignore gaps open. Nothing below matters more than landing H2a.

### P1 — new, cheap, unblocked by Athena being live

1. **Purge the Ollama fiction** — 8 locations listed in §4. One pass, no code change, removes a false premise from every doc a future session reads.
2. **Flip Iris to Athena** — base URL + bearer against `/v1/messages`. Verify with `/openapi.json` first. Small enough to land with the doc pass.
3. **Restate H2b scope** in `phase-2-plan.md` — Apollo owns external-provider accounting; local-Athena budgets are enforced by Athena (ADR 041) and integrated with, not rebuilt.
4. **Copy Athena's standards shape** into Zakros — `CLAUDE.md`, `docs/decisions/`, `LESSONS.md`. July 4 already ordered this; the new argument is that the template is in-house and proven.

### P2 — new strategic work

5. **Read Centaur before hardening H2b/Charon.** Specifically the `iron-proxy` credential-substitution design. Record the keep-or-adopt call as an ADR either way — this is the first time the market has been *ahead* of Zakros on its strongest differentiator.
6. **Pull the agent-sandbox spike forward** from Phase 3 to alongside H2b. Half-day, kill criteria on envelope expressiveness and egress-allowlist compatibility. Pin ≥v0.5.2 (v0.5.0/v0.5.1 warm-claim race).
7. **Reposition the project narrative.** "Nothing else does this" is no longer defensible and should not appear in the README. What survives scrutiny: hypervisor-level blast radius, subscription economics, episodic cross-run memory, and an inference appliance inside the same trust boundary. That is still a real position — it is just narrower and more honest than the July 4 framing.

### Demoted

8. LiteLLM Agent Platform: "verify before H2b hardens" → **watch only** (verified real, MIT, alpha, stalling).

---

*Verification posture: repo facts (stars, dates, licenses, release timeline) come from the GitHub API on 2026-07-28, not from search snippets — one fetched page reported 2024 release dates for a 2026 project and was discarded. Athena claims are from the live daemon's `/openapi.json` and its committed ADRs. Product-behaviour claims about Centaur come from its own architecture documentation and have not been run.*
