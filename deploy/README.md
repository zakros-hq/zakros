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

## 1.5. OpenBao LXC (vmid 214) — Slice H1 Hecate backend

```sh
ssh root@<crete-ip> "pct exec 214 -- bash" < deploy/openbao-bootstrap.sh
```

This installs OpenBao 2.5.x in the LXC, initializes with a 3-of-5
unseal quorum, sets up a KV-v2 mount at `secret/`, writes a
`hecate-app` policy with read access to that mount, and issues a
1-year token for the Hecate broker.

The init keys + tokens land in `/root/openbao-bootstrap.out` on the
LXC. **Pull them off and store them somewhere safe — the unseal
keys are not recoverable.** Then paste the broker token into
`deploy/secrets.json` under `hecate/vault-token`.

```sh
ssh root@<crete-ip> "pct exec 214 -- cat /root/openbao-bootstrap.out"
# → Copy unseal_keys_b64 into your operator-side secret store.
# → Copy HECATE_VAULT_TOKEN line value into deploy/secrets.json
#   under "hecate/vault-token".
```

After every Proxmox restart, OpenBao starts sealed. Manual unseal
needed (3 of 5 keys):

```sh
ssh root@<crete-ip> "pct exec 214 -- bao operator unseal <key1>"
# Repeat with keys 2 and 3.
```

Auto-unseal via Transit seal is a follow-up; for Slice H1 the
operator-in-loop unseal is the accepted operational toil.

After bootstrap, seed the existing Claude OAuth token into Vault:

```sh
CRETE_HOST=<crete-ip> deploy/openbao-seed.sh
```

This copies `claude-code/oauth-token` from `deploy/secrets.json` into
`secret/claude-code-token` in OpenBao. After this, the worker pod
fetches the token from Hecate at startup; the value in
`deploy/secrets.json` is bootstrap-only and can be cleared.

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

### 7a. `anthropic-api-key` (consumed by Apollo, Slice H2a)

Slice H2a moved every Anthropic call (worker pods + Iris) behind the
Apollo broker, so neither the worker pod nor Iris carries an Anthropic
credential anymore. Apollo holds the upstream key, fetched from
OpenBao at startup. Get a real Anthropic API key from
https://console.anthropic.com (the Claude Code OAuth token does NOT
work for the bare Messages API).

Paste the API key into `deploy/secrets.json` →
`anthropic-api-key.value`. The seed script (§7e) writes it into Vault
under `secret/anthropic-api-key` so Apollo can fetch it at boot.

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

### 7c. hecate broker (Slice H1)

Runs on the Minos VM alongside Minos / github-broker. Fronts OpenBao:
worker pods (and future brokers) authenticate to Hecate with their
Minos-minted JWT and fetch credentials by reference; Hecate verifies
the JWT carries `credentials.fetch:<ref>` scope and reads from Vault
KV.

```sh
cp deploy/templates/hecate.json.example deploy/hecate.json
# Edit deploy/hecate.json:
#   vault_addr  →  http://<openbao-lxc-ip>:8200
#   (read the IP from `terraform output -json guests` — currently
#    no entry for openbao there since LXC IPs aren't always surfaced;
#    fallback: `ssh root@<crete> "pct exec 214 -- ip -4 addr show eth0"`)
```

Make sure `deploy/secrets.json` has both:
- `minos/signing-key-pub` — same key the github-broker uses
- `hecate/vault-token` — pasted from `/root/openbao-bootstrap.out`

Then:

```sh
deploy/hecate-install.sh

# tail logs
ssh zakros@$MINOS 'sudo journalctl -u hecate -f'
```

Hecate listens on `:8084`. Worker pods inject the URL via
`ZAKROS_HECATE_URL` (configured in `config.json` → `hecate_pod_url`).

Slice H2a removed the worker pod's `ZAKROS_HECATE_FETCHES` for
`claude-code-token` — Apollo now fronts every Anthropic call, so the
pod no longer pulls an Anthropic credential of its own. Vault now
holds `anthropic-api-key` (consumed by Apollo at startup); see §7e.

### 7d. apollo broker (Slice H2a)

Runs on the Minos VM alongside Minos / github-broker / hecate. Fronts
every upstream LLM provider:

- Worker pods + Iris call `/v1/messages` on Apollo with their own
  Minos-minted JWT (`Authorization: Bearer <pod-jwt>`).
- Apollo verifies the JWT (`audience=apollo`, scope
  `apollo.<provider>.<model>`), strips the bearer, and forwards
  upstream with its own credential (fetched from Hecate at startup).

The H2a deviation from §2 D4 (subprocess-per-provider) is documented
in `docs/phase-2-plan.md §9`: in-process Provider interface, single
binary, one provider compiled in. Subprocess split lands when a
second provider is integrated.

```sh
# Mint Apollo's long-lived service JWT (calls Minos /admin/apollo/mint-token)
MINOS_URL=http://$MINOS:8080 \
MINOS_ADMIN_TOKEN="$(jq -r '.credentials["minos/admin-token"].value' deploy/secrets.json)" \
  go run ./cmd/minosctl mint-apollo-token

# Paste the output into deploy/secrets.json under
# minos/apollo-token.value, then:

cp deploy/templates/apollo.json.example deploy/apollo.json
# Defaults are correct for the standard layout (Apollo + Hecate
# co-located on Minos VM, Anthropic upstream). Edit allowed_anthropic_models
# to extend the model whitelist.

deploy/apollo-install.sh
ssh zakros@$MINOS 'sudo journalctl -u apollo -f'
```

Apollo listens on `:8085`. Worker pods inject the URL via
`ZAKROS_APOLLO_URL` (config.json → `apollo_pod_url`); Iris uses the
same env. Pod JWTs gain `audience=apollo` + per-model scopes
automatically when the project's `mcp_endpoints` includes the apollo
entry (see `deploy/templates/config.json.example`).

### 7e. seed Apollo's upstream credential into Vault

After Hecate is up and Apollo's service JWT is in `secrets.json`,
seed the Anthropic API key into Vault. `deploy/openbao-seed.sh`
reads `anthropic-api-key` from `deploy/secrets.json` and writes it
to `secret/anthropic-api-key` (the path Apollo's
`anthropic_credential_ref` resolves):

```sh
OPENBAO_ROOT_TOKEN="<root_token from /root/openbao-bootstrap.out>" \
  CRETE_HOST=$CRETE \
  deploy/openbao-seed.sh
```

After seeding, restart Apollo so it picks up the new credential
(`apollo-install.sh` does this automatically; if Apollo was already
running before the seed, `ssh zakros@$MINOS sudo systemctl restart apollo`).

### 7f. argus daemon (Slice J extraction)

Runs on the Minos VM alongside Minos and the github-broker. Owns
the rules engine + heartbeat ingest + push-event ingest as its own
systemd unit. Minos no longer bundles the watcher; pods POST
heartbeats to Argus's `/argus/heartbeat` directly.

Copy the broker config template:

```sh
cp deploy/templates/argus.json.example deploy/argus.json
# edit deploy/argus.json:
#   database_url — copy from deploy/config.json (same shared LXC)
```

Then:

```sh
deploy/argus-install.sh

# tail logs
ssh zakros@$MINOS 'sudo journalctl -u argus -f'
```

Argus listens on `:8083`. Worker pods + the Argus-sidecar inject
the URL via `ZAKROS_ARGUS_INGEST_URL` (configured in `config.json` →
`project.communication.argus_ingest_url`). Mutual `/healthz` between
Minos and Argus surfaces transitions in both audit streams.

## 8. Iris conversational pod

Iris is a long-running pod in labyrinth that long-polls Hermes for
`@iris` / `/iris` messages, asks Claude what to do, and either answers
state queries (`what's running?`) or commissions tasks. Slice H2a
routes Anthropic calls through Apollo (Iris no longer holds an
Anthropic credential); Phase 3 swaps backend to Athena Ollama.

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
- `ZAKROS_APOLLO_URL` (rendered from `config.json` →
  `apollo_pod_url`) for Anthropic calls — Iris speaks the Messages
  API directly, but Apollo is now the host. Iris uses its own pod JWT
  as the bearer to Apollo; Apollo strips it, validates the
  `apollo.anthropic.<model>` scope, and forwards upstream with its
  own credential.

Slice H2a removed the per-pod Anthropic key; the upstream credential
lives only in Vault and only Apollo fetches it.

## 9. End-to-end smoke test

1. `/status` in Discord → minos should respond with operational summary
2. `/commission repo=… branch=… "echo hello"` → pod spawns on labyrinth,
   runs entrypoint, opens PR on the test repo, audit row lands in postgres
3. `@iris what's running` in Discord → Iris replies with the active
   task list pulled from `/state/tasks`
4. `@iris commission a task to add a TODO to README.md` → Iris confirms
   and commissions; the worker pod runs the same Slice A–D pipeline as
   the manual `/commission` path
