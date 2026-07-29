# Zakros — Goals, design, and architecture review (2026-07-28)

Step-back review requested by the operator after the Centaur stress test. Inputs: `architecture.md` (v0.2), `security.md`, `roadmap.md`, `phase-2-plan.md`, both July reviews + field-delta addendum, and today's verified tree state (`132c49e`: build/vet/test green, H2a landed, gitleaks clean). This is an assessment, not a plan — nothing here changes scope until the operator says so.

---

## 0. The question under the question

Everything below sorts differently depending on one unstated decision: **is Zakros a product or personal infrastructure?**

- As a **product**, the market moved (Centaur ships chat-dispatch + k8s isolation + credential proxy + a real SDLC loop, permissively licensed, corporate-backed). Competing means narrative, license, CI, docs-as-marketing, and pace.
- As **personal SDLC infrastructure**, Centaur is a design reference, not a threat. What matters is: does it do the operator's work reliably, safely, at $0 marginal inference cost.

Every prior review implicitly assumed "product-shaped infra." The honest current posture — single operator, homelab, subscription economics — says **infrastructure first**. The recommendations below assume that; where a product ambition would change the call, it's flagged.

---

## 1. Goals — still valid?

| Goal (from roadmap/architecture) | Verdict |
|---|---|
| Replace OpenClaw: pod-per-branch isolation + cross-run memory | **Achieved and verified** (Phase 1 end-to-end on real Proxmox) |
| "Agents build. Infrastructure isolates. Humans approve." | **Valid, and now the differentiator** — Centaur auto-merges by default; Zakros's human gate is a position, not a gap |
| $0-marginal subscription economics | **Valid, unmatched** in the surveyed market; still the piece with ToS ambiguity |
| Evolve to a multi-agent, multi-project platform (23 named components) | **The over-scoped part** — see §3. The taxonomy is a target architecture, but the roadmap treats too much of it as committed work |

The roadmap's own Phase 2 trigger list is instructive: second surface needed, second operator, second provider, injection resistance needed, review volume too high, backlog coordination load-bearing. **Strictly, at most one of those has fired** (injection resistance — the repo is now public). The slices built so far (F, G, J, H1, H2a) were the *hardening spine*, which was the right choice anyway — but the remaining Phase 2 slices should be re-tested against their triggers instead of inherited as a queue.

## 2. What works (verified, keep building on it)

1. **The Phase 1 loop** — commission → pod → PR → hibernate → webhook respawn with Mnemosyne context → terminal state. This is the hard part of the whole system and it runs on real hardware. Everything else is accretion around this loop.
2. **The auth/credential spine** (F, J, H1, H2a) — Ed25519 per-pod JWTs with per-broker scopes, OpenBao-backed Hecate with per-credential policies, Apollo removing the Anthropic credential from every pod. Per-task *scoping* is ahead of everything surveyed, including Centaur (deployment-scoped vault by its own admission).
3. **Hypervisor blast radius** — control plane and workers on separate VMs under an operator-controlled firewall. Confirmed unique in the market; cheap to keep; structural rather than policy-based.
4. **Mnemosyne substrate** — pgvector run records + spawn-time injection. The episodic-memory bet is validated (no competitor has it) even though the curation loop is still missing.
5. **Design honesty in the docs** — security.md states every accepted Phase 1 risk explicitly ("no one reads 'Addressed architecturally' as 'shipped'"); the roadmap distinguishes deferred from backlog. This discipline is rarer than the features and is worth protecting — it is also why doc drift (§3.2) hurts more here than in most repos.
6. **Repo hygiene** — 18.7k LOC, 9 direct deps, zero TODO markers, gitleaks clean over all history, build/vet/test green.

## 3. What needs improvement (ordered by damage)

1. **The verification gap is the #1 systemic risk — and it's ironic.** A system whose purpose is automated SDLC has: no CI (never existed), 21 packages with zero tests **including two deployed brokers** (`cmd/argus`, `cmd/github-broker`), acceptance checkpoints that run only as manual smokes on Crete, and a slice that sat uncommitted and uncompiled for three months. H2a itself is committed but not yet Crete-smoked, so by the plan's own rule it isn't done. Fix order: CI (30 min, Makefile already encodes it) → Crete smoke → tests for the two deployed brokers. Zakros should be its own first customer: Momus reviewing Zakros PRs and CI gating them is the dogfood loop the whole design promises.
2. **Docs describe a system that doesn't exist.** ~25 live "Athena Ollama" references for a Swift/MLX daemon with no Ollama; README constraints list two slices behind the code; `architecture.md §7`'s budget-forgeability premise invalidated by Athena's server-side budgets. In a repo whose method is docs-as-authority, drift is corrosive: every future session (human or agent) reads these as ground truth. One correction pass, then the existing "update the doc when implementation diverges" rule actually enforced — ideally by Calliope-shaped automation eventually, by CI lint on banned strings now.
3. **Velocity vs. committed scope.** Observed Phase 2 throughput ≈ 1–1.5 slices/month. Remaining committed: H2b, I, K, L1, L2–L5, M ≈ 9 slices ≈ 6–9 months, before any Phase 3 item. That is fine for infrastructure-at-leisure and fatal for a product race. Either answer to §0 is acceptable; pretending both are simultaneously true is not.
4. **Structural debt, small but compounding**: the ~30-line broker bootstrap copy-pasted 4× (5th copy looms with forge-broker); the 365-day Apollo service JWT (self-documented exception, should be an ADR with an H2b/K revisit); decisions living inline in planning docs (~16 of them) instead of `docs/decisions/`.
5. **Public-repo obligations**: no LICENSE (default copyright — blocks the audience a public repo implies), stale README for anyone who finds it. Either license it properly or make it private until the product question is answered.

## 4. What can't be dropped (the spine)

These are load-bearing; removing any one changes what Zakros *is*:

1. **The human-approval invariant** — PR as the gate, confirmation tokens for high-blast ops (Slice K). Post-Centaur this is the positioning differentiator *and* the safety property. Auto-merge is now a competitor's default; resisting it is a feature.
2. **Typed task envelopes + capability composition** — "an agent's reach is defined by its task type, not by what it can run." The central design idea; everything (scopes, egress, budgets, pod classes) hangs off it.
3. **Per-task scoped short-TTL credentials** (the F/H1/H2a spine) — the market validates the direction; Centaur's wire-substitution is a *delivery* upgrade to steal, not a replacement for scoping.
4. **The hypervisor split** — cheap, structural, unique.
5. **Mnemosyne run records + spawn injection** — the original reason Zakros exists (OpenClaw couldn't remember).
6. **Argus non-forgeable telemetry** (k3s API + sidecar heartbeats, never agent self-report) — the supervision story; Centaur has nothing comparable.
7. **Slice K (trust boundary + untrusted tagging + confirmation tokens)** — the injection posture that §11 of security.md builds everything on, and the one Phase 2 trigger that has arguably fired (public repo). If Phase 2 got cut to three slices, K is one of them.

## 5. What can be dropped, deferred, or replaced (the ponytail list)

None of this is deleted today; it is the explicit not-committed list:

1. **Custom pod-lifecycle code → agent-sandbox CRDs** — the spike is already planned alongside H2b; adoption deletes code and buys the gVisor/Kata path for free. The lazy path and the strategic path agree.
2. **L3–L5 (Calliope, Prometheus, Hephaestus)** — speculative until their triggers fire. Centaur demonstrates a replaceable-prompt methodology gets surprisingly far; each of these is a pod image + task type + scopes on the existing substrate, so *deferring them costs nothing structurally*. Momus (L2) stays — its trigger (PR review as table stakes) has fired market-wide, and it's the dogfood reviewer for Zakros itself.
3. **Slice M's admin UI** — a single operator with `minosctl` + Discord + (eventually) Iris doesn't need a web UI; the identity registry it would manage has one row. Break-glass session minting is similarly informal-is-fine until a second operator exists.
4. **Slice I (Slack)** — its trigger ("a second surface *needs* to coexist") has not fired. The Hermes extraction half of I has independent value (subprocess isolation for surface credentials, per security.md §4); the Slack plugin half can wait for an actual second surface need.
5. **The Phase 3 zoo** (Pythia, Talos, Minotaur, Typhon, Asclepius) — correctly parked; keep parked. **Charon specifically should not be built as designed** — the H2b Centaur/iron-proxy ADR will likely reshape it toward credential-substituting proxy rather than SNI allowlist, and building the old design first would be waste.
6. **Second Hermes surfaces beyond Slack, multi-project registry, admin UI expansion** — all correctly phase-gated; no change.

## 6. Recommended posture (three moves)

1. **Answer §0 explicitly and record it as an ADR.** Everything else — LICENSE choice, README ambition, pace expectations, whether Centaur matters — derives from it. Default recommendation: *personal infrastructure, built product-shaped* (clean enough to open, no product obligations).
2. **Close the verification gap before adding surface** (CI → H2a Crete smoke → broker tests). It converts the repo's biggest systemic weakness into the dogfood loop the design promises, and it is all P0/P1 work already on `priorities-2026-07-28.md`.
3. **Re-derive the Phase 2 tail from triggers, not inheritance**: committed core = H2b → K → L1 → L2 (+ the Hermes-extraction half of I when subprocess credential isolation matters). Everything else moves to a triggers-attached options list. This roughly halves committed Phase 2 scope without dropping anything the spine needs.

---

*Assessment only. Scope changes, the §0 ADR, and any slice re-sequencing are operator decisions; `phase-2-plan.md` remains authoritative until amended.*
