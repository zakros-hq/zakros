# LESSONS

Operational lessons, one entry each, newest first. A lesson records what happened, what it cost, and the rule that prevents a repeat.

## 2026-07-29 — Overcorrecting a missed competitor is as wrong as missing it

The July 4 survey missed Centaur (public six weeks earlier); the July 28 delta then overcorrected, crediting Centaur's credential model as "stronger" while missing that its vault is deployment-scoped and that a real SDLC loop (githubbot) had landed two weeks prior. Both errors came from reading summaries instead of the repo. **Rule:** competitor claims get verified against the actual tree/docs before entering a review, and both directions of error (missed capability, overcredited capability) get checked.

## 2026-07-28 — Docs described a system that never existed

~25 references across live docs, source comments, and deploy files described Iris/pod inference against "Athena Ollama" — a stack that was never built (Athena shipped as a Swift/MLX daemon speaking the Anthropic dialect). Every one of those references was a false premise handed to future sessions. **Rule:** when implementation (or an external system) diverges from a doc, the doc is corrected in the same change window; CI's docs-lint bans the known fiction phrases.

## 2026-07-28 — Unbuilt code is indistinguishable from broken code

Slice H2a (~1,600 lines) sat uncommitted and never compiler-checked for roughly three months because the workstation lost its Go toolchain and nothing forced a build. It turned out to be entirely green — but nothing distinguished it from broken until `go build` ran. Cost: three months of "biggest live risk in the project" status, two reviews carrying it as P0. **Rule:** land slices when green; a slice that can't be built today is a blocker today, not later; CI (added 2026-07-29) makes the check involuntary.

## 2026-07 — OpenBao bootstrap needed two follow-up fixes

The H1 OpenBao bootstrap shipped with a wrong .deb URL and `cluster_addr` (`661c65a`), then needed its output split and the seed root token moved to an env var (`ec3f4c9`). Both were found only by running the deploy end-to-end on real hardware. **Rule:** deploy scripts count as code — they get the same rebuild-from-scratch verification as slices (`deploy/rebuild.sh` exists for exactly this), and acceptance isn't claimed until the script has run clean on the target.
