# Zakros deployment scripts

Bootstrap scripts for the four Zakros guests after Terraform has
provisioned them on Crete. All scripts are idempotent — safe to re-run
after fixing config or adjusting env.

Assumes the flat-VLAN topology (VLAN 140, 172.16.140.0/24, DHCP). Per-guest
IPs are not stable across `tf-destroy/tf-apply` cycles — the install
scripts read them from `terraform output -json guests` (helper in
[`deploy/lib.sh`](lib.sh)). Override with `MINOS_HOST=<ip>` etc. when
needed. Both VM and LXC IPs are populated by the time `tf-apply`
returns (LXC via the bpg/proxmox provider's `wait_for_ip` block).

## Teardown and rebuild from scratch

After each phase slice, validate the latest changes by rebuilding the
whole stack. Two flavors:

```sh
make rebuild                 # tf-apply + bootstrap chain (over existing infra)
make rebuild-from-scratch    # tf-destroy + tf-apply + bootstrap chain (cold)
```

Both run [`deploy/rebuild.sh`](rebuild.sh), which:

1. Runs `make tf-apply` (or `tf-destroy && tf-apply` for `--from-scratch`)
2. Rewrites the IP-bearing fields in `deploy/config.json` from
   `terraform output -json guests` — preserves the postgres password
   you put in `database_url` and every other field
3. Runs sections 1–8 below in order (postgres bootstrap, migrations,
   k3s, image push, minos, github-broker, cloudflared, iris)
4. Mints Iris's JWT with `minosctl mint-iris-token` and writes it back
   into `deploy/secrets.json` automatically

The script reads persistent credentials out of `deploy/secrets.json`
(gitignored) — operator must seed those once. The postgres password
lives in `database_url` in `deploy/config.json` (also gitignored); the
script extracts it and re-applies it to the freshly-bootstrapped LXC.
First-time setup follows the manual sections below.

**What persists** across a teardown/rebuild — do not delete:

- Cloudflare Tunnel registration + hostname route (reuse the same token)
- GitHub App registration + webhook secret + installed repos
- Discord App + bot token
- Claude Code OAuth token from `claude setup-token`
- Proxmox host config: bridges, storage pools, the `terraform@crete`
  token, libguestfs-tools, `snippets`/`vztmpl`/`iso` content-type flags
  on local datastore, Debian LXC template
- `deploy/config.json` and `deploy/secrets.json` (gitignored; values
  still valid after rebuild)

**What gets destroyed**:

```sh
make tf-destroy
```

Tears down the 4 guests + Proxmox files (cloud-init snippets, per-VM
downloads). Proxmox state snapshots cleanly — a fresh `tf-apply` will
re-download the Ubuntu cloud image and recreate everything.

Optional Crete cleanup if you want a truly cold start (otherwise these
cache across rebuilds and speed up the next apply):

```sh
ssh root@172.16.30.103 "rm -f /var/lib/vz/template/iso/noble-server-cloudimg-amd64.img"
```

**Fresh bring-up** from a clean destroy: just `make rebuild-from-scratch`.
Sections 1–8 below are still the per-step source of truth (and what to
re-run by hand if a single phase fails mid-rebuild) — each script is
idempotent.

Expected total time for a cold rebuild: ~20 minutes (mostly Postgres +
apt-upgrade waits; nothing interactive).

## 1. Postgres LXC (vmid 211)

```sh
export POSTGRES_PASSWORD="$(openssl rand -base64 32 | tr -d '=+/' | head -c 32)"
echo "$POSTGRES_PASSWORD"   # stash — needed for migrations + minos config

ssh root@172.16.30.103 \
  "POSTGRES_PASSWORD='$POSTGRES_PASSWORD' pct exec 211 -- bash" \
  < deploy/postgres-bootstrap.sh
```

Then run migrations from your workstation:

```sh
go install github.com/pressly/goose/v3/cmd/goose@latest
PG=$(terraform -chdir=terraform output -json guests | jq -r '.postgres.ip')
DSN="postgres://zakros:$POSTGRES_PASSWORD@$PG:5432/zakros?sslmode=disable"
~/go/bin/goose -dir minos/storage/pgstore/migrations postgres "$DSN" up
```

## 2. k3s on labyrinth (vmid 212)

```sh
LABYRINTH=$(terraform -chdir=terraform output -json guests | jq -r '.labyrinth.ip')

ssh zakros@$LABYRINTH 'sudo bash -s' < deploy/k3s-install.sh

# pull kubeconfig back
scp zakros@$LABYRINTH:/etc/rancher/k3s/k3s.yaml ~/.kube/zakros.yaml
sed -i '' "s/127.0.0.1/$LABYRINTH/" ~/.kube/zakros.yaml  # drop '' on Linux
KUBECONFIG=~/.kube/zakros.yaml kubectl get nodes
```

## 3. Worker images → labyrinth's containerd

```sh
deploy/images-push.sh
```

Builds `zakros/claude-code:local` + `zakros/argus-sidecar:local` locally,
scps tars, imports into k3s's containerd. No remote registry needed.
The labyrinth host IP is read from `terraform output -json guests`;
override with `LABYRINTH_HOST=<ip> deploy/images-push.sh` if needed.

## 4. Minos on minos VM (vmid 210)

First, copy the config + secrets templates and fill in real values:

```sh
cp deploy/templates/config.json.example  deploy/config.json
cp deploy/templates/secrets.json.example deploy/secrets.json
# edit both — both are gitignored
```

Things to replace in `config.json`:
- `REPLACE_POSTGRES_PASSWORD` → the password you generated in step 1
- `REPLACE_YOUR_DISCORD_USER_ID` → your Discord user ID (enable Developer Mode, right-click yourself, Copy User ID)
- `REPLACE_DISCORD_CHANNEL_ID` → the Discord channel where Minos creates task threads
- `REPLACE_DEFAULT_REPO_URL` → the project's primary GitHub repo URL (used by Iris when commissioning without an explicit repo)

Things to replace in `secrets.json`:
- `minos/admin-token` — `openssl rand -base64 32`
- `cerberus/github-webhook` — any strong random string; configure the same value in the GitHub App webhook secret field
- `hermes/discord-bot-token` — your Discord bot token
- `minos/signing-key` and `minos/signing-key-pub` — generate with `make build && bin/minosctl gen-signing-key`, paste the two PEM blocks into the matching entries
- `minos/iris-token` — minted in step 8 below (leave the placeholder for now)
- `github/app-private-key` — the PEM from your GitHub App; generated/registered in step 6

Then:

```sh
deploy/minos-install.sh

# tail logs
MINOS=$(terraform -chdir=terraform output -json guests | jq -r '.minos.ip')
ssh zakros@$MINOS 'sudo journalctl -u minos -f'
```

The script builds `bin/minos`, scps it + config + secrets + kubeconfig,
writes the systemd unit, starts the service. Idempotent — re-run to push
config changes.

## 5. Public ingress via Cloudflare Tunnel

Makes `POST https://<your-hostname>/webhooks/github` reach the minos
daemon without port-forwarding or public IPs.

One-time in the Cloudflare Zero Trust dashboard:
1. Networks → Tunnels → **Create a tunnel** (Cloudflared flavor), name
   it `zakros`, copy the token on the "Install and run a connector"
   screen.
2. **Public Hostname** tab → Add public hostname → pick a subdomain on
   a domain you control, service type `HTTP`, URL `localhost:8080`.

Paste the token into `deploy/secrets.json` under
`cloudflared/tunnel-token`, then:

```sh
deploy/cloudflared-install.sh

# verify
curl -v https://<your-hostname>/healthz
```

(`CLOUDFLARED_TOKEN=<...>` in env also works and overrides secrets.json.)

## 6. GitHub App (Cerberus webhooks)

See [github-app.md](./github-app.md) for the full registration walkthrough.
TL;DR:

1. Generate webhook secret: `openssl rand -base64 32 | tr -d '=+/' | head -c 40`
2. Register GitHub App pointing at `https://<your-hostname>/webhooks/github`
3. Install on a test repo
4. Paste the webhook secret into `deploy/secrets.json` under
   `cerberus/github-webhook`, re-run `deploy/minos-install.sh`

## 7. Worker pod credentials + github-broker

The claude-code worker pod's GitHub access changed in Slice F: instead
of a long-lived PAT, the pod calls the **github-broker** at startup
to mint a per-task GitHub App installation token. The PAT is gone
from the deploy templates entirely.

### 7a. `CLAUDE_CODE_OAUTH_TOKEN`

Long-lived Claude Code token so pods bill against your Claude.ai
subscription (Max / Pro / Teams) instead of metered API spend.
Generate once on your workstation:

```sh
claude setup-token
```

Paste the emitted token into `deploy/secrets.json` →
`claude-code/oauth-token.value`. Token is good for ~1 year.

### 7b. github-broker daemon

Runs on the Minos VM alongside Minos. Reads the App's private key from
the secret provider, validates pod JWTs, mints installation tokens
per call.

Copy the broker config template:

```sh
cp deploy/templates/github-broker.json.example deploy/github-broker.json
# edit deploy/github-broker.json:
#   github_app_id            — from your App's settings page
#   github_installation_id   — from .../installations/<id> URL after install
```

Make sure `deploy/secrets.json` has `github/app-private-key` (the PEM
you downloaded when registering the App in step 6). Then:

```sh
deploy/github-broker-install.sh

# tail logs
ssh zakros@$MINOS 'sudo journalctl -u github-broker -f'
```

The broker listens on `:8082` and the worker pod hits it via
`ZAKROS_GITHUB_BROKER_URL` (configured in `config.json` →
`github_broker_pod_url`).

## 8. Iris conversational pod

Iris is a long-running pod in labyrinth that long-polls Hermes for
`@iris` / `/iris` messages, asks Claude what to do, and either answers
state queries (`what's running?`) or commissions tasks. Phase 1 / Slice
0 backs it with the Anthropic Messages API directly using the same
OAuth token the worker pod uses; Phase 2 routes through Apollo, Phase 3
swaps backend to Athena Ollama.

Apply the Deployment after the worker images are loaded (step 3) and
Minos is running (step 4). Iris's bearer is now a Minos-minted JWT
(Slice F) — mint it once, paste into secrets.json, then install:

```sh
# Mint Iris's long-lived JWT (calls Minos /admin/iris/mint-token)
MINOS_URL=http://$MINOS:8080 \
MINOS_ADMIN_TOKEN="$(jq -r '.credentials["minos/admin-token"].value' deploy/secrets.json)" \
  bin/minosctl mint-iris-token

# Paste the printed JWT into deploy/secrets.json under
#   "minos/iris-token": { "value": "<the JWT>" }

deploy/iris-install.sh

# tail logs
KUBECONFIG=~/.kube/zakros.yaml kubectl -n zakros logs -f deploy/iris
```

The script reads `deploy/config.json` + `deploy/secrets.json`, renders
`deploy/templates/iris-deployment.yaml`, applies it. Iris uses:

- `minos/iris-token` for `/state/*`, `/hermes/events.next`,
  `/hermes/post_as_iris`, `/memory/lookup`
- `minos/admin-token` for `POST /tasks` (Iris commissions on the
  operator's behalf — Phase 2 Slice G replaces this with proper
  user-on-behalf-of identity forwarding)
- `anthropic/api-key` for Claude calls — **must be a real Anthropic
  API key from https://console.anthropic.com**, NOT the Claude Code
  OAuth token. The bare Messages API rejects OAuth tokens with
  "OAuth authentication is currently not supported"; the OAuth flow
  is specific to the `claude` CLI binary used by the worker pod.

Add the key to `deploy/secrets.json` under `anthropic/api-key` before
running `iris-install.sh`. This is a separate billing path from the
Claude Pro/Max subscription that backs the worker pod's OAuth token —
Iris's API calls draw from your Anthropic API credit balance. Phase 2
H2 (Apollo) centralizes this and adds per-project rate limits.

## 9. End-to-end smoke test

1. `/status` in Discord → minos should respond with operational summary
2. `/commission repo=… branch=… "echo hello"` → pod spawns on labyrinth,
   runs entrypoint, opens PR on the test repo, audit row lands in postgres
3. `@iris what's running` in Discord → Iris replies with the active
   task list pulled from `/state/tasks`
4. `@iris commission a task to add a TODO to README.md` → Iris confirms
   and commissions; the worker pod runs the same Slice A–D pipeline as
   the manual `/commission` path
