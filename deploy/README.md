# Zakros deployment scripts

Bootstrap scripts for the four Zakros guests after Terraform has
provisioned them on Crete. All scripts are idempotent — safe to re-run
after fixing config or adjusting env.

Assumes the flat-VLAN topology (VLAN 140, 172.16.140.0/24, DHCP). Per-guest
IPs are not stable across `tf-destroy/tf-apply` cycles — the install
scripts read them from `terraform output -json guests` (helper in
[`deploy/lib.sh`](lib.sh)). Override with `MINOS_HOST=<ip>` etc. when
needed.

The Postgres LXC IP isn't in TF output (the Proxmox provider doesn't
surface LXC runtime IPs); look it up with
`ssh root@<crete> pct exec 211 -- ip -4 addr show eth0` or from the
homelab router's DHCP leases.

## Teardown and rebuild from scratch

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

**Fresh bring-up** from a clean destroy:

1. `make tf-apply` — provisions guests. Once qemu-guest-agent reports,
   `terraform output -json guests` returns the live IPs and the install
   scripts pick them up automatically.
2. Look up the Postgres LXC IP separately (TF doesn't surface it — see
   above) and update `deploy/config.json` (`database_url`,
   `minos_pod_url`) if either has changed.
3. Run **sections 1–8 below in order** using your existing
   `deploy/secrets.json` + `deploy/config.json`. Each script is
   idempotent, so if any step fails mid-run you re-run it after the fix.

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

Then run migrations from your workstation (substitute the LXC IP you
looked up):

```sh
go install github.com/pressly/goose/v3/cmd/goose@latest
DSN="postgres://zakros:$POSTGRES_PASSWORD@<postgres-lxc-ip>:5432/zakros?sslmode=disable"
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
- `minos/bearer`, `minos/admin-token`, `minos/iris-token` — `openssl rand -base64 32` each
- `cerberus/github-webhook` — any strong random string; configure the same value in the GitHub App webhook secret field
- `hermes/discord-bot-token` — your Discord bot token

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

## 7. Worker pod credentials (Anthropic + GitHub)

The claude-code worker image consumes two credentials at runtime:

- `CLAUDE_CODE_OAUTH_TOKEN` — long-lived Claude Code token so pods bill
  against your Claude.ai subscription (Max / Pro / Teams) instead of
  metered API spend. Generate once on your workstation:

  ```sh
  claude setup-token
  ```

  Paste the emitted token into `deploy/secrets.json` →
  `claude-code/oauth-token.value`. Token is good for ~1 year; re-run
  to rotate.

- `GITHUB_TOKEN` — GitHub PAT for `gh pr create` + git push from the
  worker pod. Phase 1 uses a **fine-grained PAT** scoped to the
  test repo; Phase 2 will mint short-lived installation tokens via
  the GitHub App.

  GitHub → Settings → Developer settings → Personal access tokens →
  Fine-grained tokens → Generate new token. Permissions: Contents
  read/write, Pull requests read/write, Metadata read. Scope to the
  repos you want Zakros to commit to.

  Paste into `deploy/secrets.json` → `github/pat.value`.

Both wire through `project.capabilities.injected_credentials` in
`config.json`; the template already maps them to the expected env vars
that claude-code + gh read automatically. Re-run
`deploy/minos-install.sh` after editing.

## 8. Iris conversational pod

Iris is a long-running pod in labyrinth that long-polls Hermes for
`@iris` / `/iris` messages, asks Claude what to do, and either answers
state queries (`what's running?`) or commissions tasks. Phase 1 / Slice
0 backs it with the Anthropic Messages API directly using the same
OAuth token the worker pod uses; Phase 2 routes through Apollo, Phase 3
swaps backend to Athena Ollama.

Apply the Deployment after the worker images are loaded (step 3) and
Minos is running (step 4):

```sh
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
