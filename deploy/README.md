# Daedalus deployment scripts

Bootstrap scripts for the four Daedalus guests after Terraform has
provisioned them on Crete. All scripts are idempotent — safe to re-run
after fixing config or adjusting env.

Assumes the flat-VLAN topology (VLAN 140, 172.16.140.0/24, DHCP). Current
IPs: postgres .100, minos .101, labyrinth .102, ariadne .103.

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
DSN="postgres://daedalus:$POSTGRES_PASSWORD@172.16.140.100:5432/daedalus?sslmode=disable"
~/go/bin/goose -dir minos/storage/pgstore/migrations postgres "$DSN" up
```

## 2. k3s on labyrinth (vmid 212)

```sh
ssh daedalus@172.16.140.102 'sudo bash -s' < deploy/k3s-install.sh

# pull kubeconfig back
scp daedalus@172.16.140.102:/etc/rancher/k3s/k3s.yaml ~/.kube/daedalus.yaml
sed -i '' 's/127.0.0.1/172.16.140.102/' ~/.kube/daedalus.yaml  # drop '' on Linux
KUBECONFIG=~/.kube/daedalus.yaml kubectl get nodes
```

## 3. Worker images → labyrinth's containerd

```sh
LABYRINTH_HOST=172.16.140.102 deploy/images-push.sh
```

Builds `daedalus/claude-code:local` + `daedalus/argus-sidecar:local` locally,
scps tars, imports into k3s's containerd. No remote registry needed.

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

Things to replace in `secrets.json`:
- `minos/bearer` and `minos/admin-token` — `openssl rand -base64 32` each
- `cerberus/github-webhook` — any strong random string; configure the same value in the GitHub App webhook secret field
- `hermes/discord-bot-token` — your Discord bot token

Then:

```sh
deploy/minos-install.sh

# tail logs
ssh daedalus@172.16.140.101 'sudo journalctl -u minos -f'
```

The script builds `bin/minos`, scps it + config + secrets + kubeconfig,
writes the systemd unit, starts the service. Idempotent — re-run to push
config changes.

## 5. Public ingress via Cloudflare Tunnel

Makes `POST https://<your-hostname>/webhooks/github` reach the minos
daemon without port-forwarding or public IPs.

One-time in the Cloudflare Zero Trust dashboard:
1. Networks → Tunnels → **Create a tunnel** (Cloudflared flavor), name
   it `daedalus`, copy the token on the "Install and run a connector"
   screen.
2. **Public Hostname** tab → Add public hostname → pick a subdomain on
   a domain you control, service type `HTTP`, URL `localhost:8080`.

Then from the operator workstation:

```sh
CLOUDFLARED_TOKEN='<the long token>' deploy/cloudflared-install.sh

# verify
curl -v https://<your-hostname>/healthz
```

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
  repos you want Daedalus to commit to.

  Paste into `deploy/secrets.json` → `github/pat.value`.

Both wire through `project.capabilities.injected_credentials` in
`config.json`; the template already maps them to the expected env vars
that claude-code + gh read automatically. Re-run
`deploy/minos-install.sh` after editing.

## 8. End-to-end smoke test

1. `/status` in Discord → minos should respond with operational summary
2. `/commission "echo hello"` → pod spawns on labyrinth, runs entrypoint,
   opens PR on the test repo, audit row lands in postgres
3. `minosctl replay <run-id>` from operator workstation → prints the full
   task trace
