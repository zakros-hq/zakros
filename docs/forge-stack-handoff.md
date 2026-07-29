# Handoff: self-hosted forge stack (Forgejo) — Zakros owns this

**From:** Athena session, 2026-07-04. **Decision already made:** Forgejo (not Bugzilla/GitLab/OneDev) — full evaluation with sources in `~/Source/forge-stack-evaluation-2026-07.md`; read it before re-litigating anything. **Why Zakros owns it:** the forge becomes Zakros's work-queue substrate (issue webhooks → commissioning) and the credential story is a sibling of this repo's `github-broker`.

## The goal

One self-hosted system (git server + issue tracker + kanban) that AI agents drive via API/CLI/MCP, deployed in the homelab, eventually wired into Zakros so a labeled issue commissions an agent run. Fixes the cross-project problem of findings tracked in session docs ("fixes audit item A") — the binding convention is `~/okf/playbooks/track-findings-as-issues.md` (PROPOSED; adopt/amend as part of this work).

## Facts you'd otherwise have to rediscover

- Forgejo v15.0 (2026-04): single Go binary, ~40–80 MB idle; issues + kanban project boards + GH-syntax-compatible Actions + webhooks + full REST API + `tea` CLI. ≥3 maintained MCP servers (SquareCows/forgejo-mcp = 103 tools; raohwork; Kunde21) — pick and pin one, the REST API is the stable floor.
- **Mirror, don't move:** Forgejo pull-mirrors GitHub repos, so it can be tracker-of-record per repo while GitHub stays canonical; push-mirror back to GitHub as offsite copy. No big-bang migration; case-project stays on GitHub indefinitely.
- **Known weak spot:** project-*board* API coverage lags issues/labels in the Gitea lineage. Design rule: agent-facing work state lives in labels/milestones (fully API-covered); boards are the human view. The spike must verify card-moves via API before promising them.
- Homelab pattern to match: Proxmox VM via Terraform + a new Ansible role, Infisical per-VM machine identity for secrets, PBS backups, AdGuard/BIND internal DNS (`git.goodolclint.internal`). SQLite is fine at this scale; Postgres only if PBS-friendly dumps are wanted. IaC lives in `~/Source/homelab`.

## Existing seams in THIS repo

- `github-broker` (`cmd/github-broker/server.go`): JWT-gated (`pkg/brokerauth`), mints scoped GitHub App installation tokens (`POST /github/installation-token`, scope=clone) via `pkg/githubapp`. **The new work is a `forge-broker` sibling, not a replacement** — same brokerauth pattern, mints scoped Forgejo bot-account tokens (or per-repo deploy keys). Extract/define the common interface as "give me a clone/push credential for repo X" so agents don't care which forge backs a repo.
- `hecate` (chat surface / webhook receiver): target for Forgejo issue webhooks — `label added: agent:go` → commission. Note Forgejo has no GitHub-App installation semantics; it's bot users + scoped tokens (simpler — write audit expectations against that model).
- `agents/claude-code/entrypoint.sh` sets git author "Daedalus Agent" — agent commits against Forgejo repos should keep a distinguishable author identity.

## Phases (each independently shippable)

1. **Spike (~half day):** Forgejo container on the homelab docker VM. Mirror two repos (Athena + one ansible collection). Point one MCP server + `tea` at it. Run the full agent loop: file issue → branch → fix → push with `Fixes #N` → verify auto-close → attempt a board card-move via API (the coverage check). Kill criteria: auto-close broken on mirrored repos, or token scoping too coarse for the broker model.
2. **Productionize:** dedicated Proxmox VM, Terraform + Ansible role, Infisical secrets, PBS backup, internal DNS/TLS, one `forgejo-runner`. Land in `~/Source/homelab`.
3. **`forge-broker`:** the sibling broker + common credential interface + Forgejo webhooks → hecate commissioning path. This is where Zakros-native design decisions live (per-agent bot accounts vs one bot, audit rows, token TTLs).
4. **Convention adoption:** amend `track-findings-as-issues.md` with a forge-of-record rule per repo (never two trackers for one repo); migrate the first repo's tracker-of-record to Forgejo.

## Open decisions (operator's, surface before building)

- Which repos move tracker-of-record first (suggest: the MLX workspace tracker — it was about to become a private GitHub repo `mlx-tracker`; a Forgejo org does the same job self-hosted).
- Per-agent bot accounts vs one shared bot (audit granularity vs account sprawl).
- Whether Forgejo Actions replaces any existing CI, or stays dormant initially (suggest: dormant).
