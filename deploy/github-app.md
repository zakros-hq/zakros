# GitHub App registration for Cerberus

Cerberus authenticates webhook deliveries by HMAC-signing with a shared
webhook secret. (App private-key / installation-token auth for calling
the GitHub API *as* the App is Phase 2 work — Phase 1 webhook-only.)

## Prerequisites

- Public hostname pointing at minos via Cloudflare Tunnel
  (see `deploy/cloudflared-install.sh`). Example: `daedalus.example.com`.
- A strong random webhook secret. Generate once and stash:
  ```sh
  openssl rand -base64 32 | tr -d '=+/' | head -c 40
  ```

## Register the App

GitHub → Settings → Developer settings → **GitHub Apps** → **New GitHub App**.

| Field | Value |
| --- | --- |
| GitHub App name | `daedalus-phase1` (or any unique name) |
| Homepage URL | `https://<your-public-hostname>/` (anything works; not load-bearing) |
| Callback URL | leave blank |
| Setup URL | leave blank |
| **Webhook → Active** | ✅ on |
| **Webhook URL** | `https://<your-public-hostname>/webhooks/github` |
| **Webhook secret** | paste the secret you generated |
| Repository permissions | Contents: read/write, Pull requests: read/write, Metadata: read |
| Subscribe to events | Push, Pull request, Issue comment, Pull request review comment |
| Where can this app be installed | Only on this account |

Click **Create GitHub App**. You'll land on the app settings page.

## Install it on a test repo

Left sidebar → **Install App** → pick your account → select a specific
repository (or all repos). After install, GitHub will immediately start
sending webhooks to the tunnel hostname. `curl` the healthz to confirm
the tunnel is live before installing.

## Wire the webhook secret into Minos

In `deploy/secrets.json`:

```json
"cerberus/github-webhook": {
  "value": "<paste same secret>"
}
```

In `deploy/config.json`, `github_webhook_secret_ref` should already be
`"cerberus/github-webhook"` (default).

Then:

```sh
deploy/minos-install.sh
```

## Verify

Push a test commit to a watched repo, or click **Redeliver** on any
past delivery in the GitHub App → Advanced → Recent Deliveries page.

```sh
ssh daedalus@172.16.140.101 'sudo journalctl -u minos --since "1 minute ago" --no-pager | grep -iE "webhook|cerberus"'
```

Should see a cerberus-category audit line confirming the delivery was
verified and handled.

## What's not done yet (Phase 2)

- **App JWT signing + installation tokens** — needed to let Daedalus
  *call* GitHub (e.g. comment on PRs as the App rather than as a
  personal token). Phase 1 workers use operator-supplied `gh` CLI auth
  via the injected credentials mechanism instead.
- **Check runs** — webhook-to-check flow that lets Daedalus mark PRs
  with pass/fail status. Out of Phase 1 scope per roadmap.md.
