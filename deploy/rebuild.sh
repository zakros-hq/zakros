#!/bin/bash
# End-to-end Zakros rebuild. Runs the full chain documented in
# deploy/README.md sections 1-8 as a single command, and (optionally)
# tears down + recreates the Crete guests first.
#
# Designed to be re-run after each phase slice: validates that the
# current code/config combo still deploys cleanly from a known state.
#
# Usage:
#   deploy/rebuild.sh                  # bootstrap over existing infra (idempotent)
#   deploy/rebuild.sh --from-scratch   # also runs `make tf-destroy && make tf-apply`
#
# Operator-side prerequisites (one-time, persist across rebuilds):
#   * Go toolchain, terraform, jq, docker, kubectl, ssh, scp, openssl
#   * deploy/secrets.json populated — every credential except
#     minos/iris-token (this script mints it after minos comes up)
#   * deploy/config.json populated — database_url *must* already contain
#     the postgres password the operator wants to keep using; the IP
#     portion gets rewritten from terraform output on each rebuild
#   * deploy/github-broker.json populated with App ID + installation ID
#   * GitHub App registered + installed (registration persists across
#     rebuilds — see deploy/github-app.md)
#   * Cloudflare Tunnel registered in the dashboard, token in secrets.json
#   * goose installed: `go install github.com/pressly/goose/v3/cmd/goose@latest`
#
# What this does NOT regenerate (operator must seed once):
#   * Postgres password — read out of deploy/config.json's database_url.
#     If you want to rotate, hand-edit config.json before running this.
#   * Minos signing keypair — `minosctl gen-signing-key` once, paste
#     into secrets.json. Re-running would invalidate any persisted JWTs.
#   * GitHub App PEM, Discord/Anthropic/Cloudflared tokens.

set -euo pipefail

FROM_SCRATCH=0
for arg in "$@"; do
  case "$arg" in
    --from-scratch) FROM_SCRATCH=1 ;;
    -h|--help)
      sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'
      exit 0 ;;
    *)
      echo "unknown arg: $arg" >&2
      exit 2 ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

. deploy/lib.sh

# -----------------------------------------------------------------------------
# Operator-side preflight
# -----------------------------------------------------------------------------

need() {
  command -v "$1" >/dev/null 2>&1 || { echo "missing dep: $1" >&2; exit 1; }
}
need terraform
need jq
need ssh
need scp
need docker
need kubectl
need go
need openssl

if ! command -v goose >/dev/null 2>&1 && [ ! -x "$HOME/go/bin/goose" ]; then
  echo "goose not found; install with: go install github.com/pressly/goose/v3/cmd/goose@latest" >&2
  exit 1
fi
GOOSE_BIN="$(command -v goose 2>/dev/null || echo "$HOME/go/bin/goose")"

for f in deploy/config.json deploy/secrets.json deploy/github-broker.json; do
  if [ ! -f "$f" ]; then
    echo "missing: $f — copy from deploy/templates/ and fill in real values" >&2
    exit 1
  fi
done

# Required secret entries that this script does NOT mint itself.
# Slice H2a: Apollo replaces direct Anthropic credential in pods —
# `anthropic-api-key` (flat) is what Apollo fetches from Vault at
# startup and is what openbao-seed.sh seeds into Vault.
#
# `hecate/vault-token` is captured automatically when --from-scratch
# runs deploy/openbao-bootstrap.sh; without --from-scratch the operator
# is expected to have it in secrets.json from a prior bootstrap.
required_secrets=(
  minos/signing-key
  minos/signing-key-pub
  minos/admin-token
  cerberus/github-webhook
  github/app-private-key
  hermes/discord-bot-token
  anthropic-api-key
  cloudflared/tunnel-token
)
if [ "$FROM_SCRATCH" -eq 0 ]; then
  required_secrets+=(hecate/vault-token)
fi
for k in "${required_secrets[@]}"; do
  v="$(jq -r --arg k "$k" '.credentials[$k].value // empty' deploy/secrets.json)"
  if [ -z "$v" ] || [[ "$v" == REPLACE_* ]]; then
    echo "deploy/secrets.json missing or placeholder for: $k" >&2
    exit 1
  fi
done

# Pull persistent postgres password out of the existing database_url so
# we can re-apply it to the freshly-bootstrapped LXC. If it still has
# the placeholder, the operator hasn't completed first-time setup.
DB_URL_OLD="$(jq -r '.database_url' deploy/config.json)"
POSTGRES_PASSWORD="$(printf '%s' "$DB_URL_OLD" \
  | sed -nE 's|^postgres://[^:]+:([^@]+)@.*$|\1|p')"
if [ -z "$POSTGRES_PASSWORD" ] || [ "$POSTGRES_PASSWORD" = "REPLACE_POSTGRES_PASSWORD" ]; then
  echo "deploy/config.json database_url has no postgres password (or still placeholder)." >&2
  echo "Generate one and put it in database_url before running this script:" >&2
  echo "  openssl rand -base64 32 | tr -d '=+/' | head -c 32" >&2
  exit 1
fi

# -----------------------------------------------------------------------------
# Phase 0: terraform
# -----------------------------------------------------------------------------

if [ "$FROM_SCRATCH" -eq 1 ]; then
  echo "==> [0/14] tf-destroy + tf-apply (--from-scratch)"
  make tf-destroy
  make tf-apply
else
  echo "==> [0/14] tf-apply (idempotent)"
  make tf-apply
fi

# -----------------------------------------------------------------------------
# Phase 1: refresh config.json with current guest IPs
# -----------------------------------------------------------------------------

echo "==> [1/14] Rewriting deploy/config.json with fresh guest IPs"

POSTGRES_IP="$(tf_guest_ip postgres)"
MINOS_IP="$(tf_guest_ip minos)"
LABYRINTH_IP="$(tf_guest_ip labyrinth)"
: "${POSTGRES_IP:?terraform output has no postgres ip}"
: "${MINOS_IP:?terraform output has no minos ip}"
: "${LABYRINTH_IP:?terraform output has no labyrinth ip}"

DB_URL_NEW="postgres://zakros:${POSTGRES_PASSWORD}@${POSTGRES_IP}:5432/zakros?sslmode=disable"
MINOS_URL="http://${MINOS_IP}:8080"
BROKER_URL="http://${MINOS_IP}:8082"
HECATE_URL="http://${MINOS_IP}:8084"
APOLLO_URL="http://${MINOS_IP}:8085"

tmp="$(mktemp)"
jq \
  --arg db "$DB_URL_NEW" \
  --arg mp "$MINOS_URL" \
  --arg bp "$BROKER_URL" \
  --arg hp "$HECATE_URL" \
  --arg ap "$APOLLO_URL" \
  '
    .database_url          = $db
  | .minos_pod_url         = $mp
  | .github_broker_pod_url = $bp
  | .hecate_pod_url        = $hp
  | .apollo_pod_url        = $ap
  | .project.capabilities.mcp_endpoints
      = (.project.capabilities.mcp_endpoints
         | map(
             if .name == "github" then .url = $bp
             elif .name == "apollo" then .url = $ap
             else . end))
  ' deploy/config.json > "$tmp"
mv "$tmp" deploy/config.json

echo "    postgres   $POSTGRES_IP"
echo "    minos      $MINOS_IP"
echo "    labyrinth  $LABYRINTH_IP"

# -----------------------------------------------------------------------------
# Phase 1.5: openbao bootstrap (only on --from-scratch)
#
# tf-destroy nukes the openbao LXC's raft data, so a fresh apply means
# we have to re-init, re-issue the hecate-app token, capture the root
# token for the seed step, and write the new hecate/vault-token into
# secrets.json. The bootstrap script is idempotent — on a non-fresh
# LXC it short-circuits and exits 0 — so this branch is safe to run
# unconditionally if the operator passed --from-scratch.
# -----------------------------------------------------------------------------

CRETE_HOST="${CRETE_HOST:-172.16.30.103}"
OPENBAO_ROOT_TOKEN="${OPENBAO_ROOT_TOKEN:-}"

if [ "$FROM_SCRATCH" -eq 1 ]; then
  echo "==> [1.5] Bootstrapping openbao (vmid 214) — fresh init expected"
  ssh "root@${CRETE_HOST}" "pct exec 214 -- bash" < deploy/openbao-bootstrap.sh

  echo "==> [1.5] Capturing fresh root token + hecate-app token from LXC"
  bootstrap_out="$(ssh "root@${CRETE_HOST}" "pct exec 214 -- cat /root/openbao-bootstrap.out 2>/dev/null" || true)"
  hecate_out="$(ssh "root@${CRETE_HOST}" "pct exec 214 -- cat /root/openbao-hecate.out 2>/dev/null" || true)"

  if [ -z "$bootstrap_out" ] || [ -z "$hecate_out" ]; then
    cat >&2 <<MSG
==> [1.5] openbao bootstrap output missing on the LXC.

The LXC was already initialized in a prior run and the bootstrap
script short-circuited. The original /root/openbao-bootstrap.out and
/root/openbao-hecate.out are no longer accessible from here, so we
can't recover the hecate-app token automatically.

Either:
  * Manually paste the original hecate-app token (from your
    workstation secret store) into deploy/secrets.json under
    hecate/vault-token, then re-run without --from-scratch
  * Or destroy the openbao LXC's raft state and re-init:
      ssh root@${CRETE_HOST} 'pct exec 214 -- rm -rf /var/lib/openbao'
      ssh root@${CRETE_HOST} 'pct exec 214 -- systemctl restart openbao'
    then re-run with --from-scratch.
MSG
    exit 1
  fi

  OPENBAO_ROOT_TOKEN="$(printf '%s' "$bootstrap_out" | jq -r .root_token)"
  HECATE_VAULT_TOKEN="$(printf '%s' "$hecate_out" | sed -nE 's|^HECATE_VAULT_TOKEN=(.*)$|\1|p')"
  if [ -z "$OPENBAO_ROOT_TOKEN" ] || [ -z "$HECATE_VAULT_TOKEN" ]; then
    echo "[1.5] failed to parse openbao bootstrap output" >&2
    exit 1
  fi

  echo "==> [1.5] Writing hecate/vault-token into deploy/secrets.json"
  tmp="$(mktemp)"
  jq --arg tok "$HECATE_VAULT_TOKEN" \
    '.credentials["hecate/vault-token"].value = $tok' \
    deploy/secrets.json > "$tmp"
  mv "$tmp" deploy/secrets.json

  cat <<MSG
==> [1.5] OpenBao bootstrap captured.
    SAVE THE 5 UNSEAL KEYS NOW (the script will not print them again):
       ssh root@${CRETE_HOST} 'pct exec 214 -- cat /root/openbao-bootstrap.out'
    Stash them in your workstation password manager. The next tf-destroy
    will erase them and you will need at least 3-of-5 to unseal after
    any LXC restart.
MSG
fi

export OPENBAO_ROOT_TOKEN

# -----------------------------------------------------------------------------
# Phase 2: postgres bootstrap + migrations
# -----------------------------------------------------------------------------

echo "==> [2/14] Bootstrapping postgres LXC (vmid 211)"

ssh "root@${CRETE_HOST}" \
  "POSTGRES_PASSWORD='${POSTGRES_PASSWORD}' pct exec 211 -- bash" \
  < deploy/postgres-bootstrap.sh

echo "==> [2/14] Waiting for postgres to accept connections"
for _ in $(seq 1 30); do
  if (echo > "/dev/tcp/${POSTGRES_IP}/5432") 2>/dev/null; then
    break
  fi
  sleep 2
done

echo "==> [2/14] Running goose migrations"
"$GOOSE_BIN" -dir minos/storage/pgstore/migrations \
  postgres "$DB_URL_NEW" up

# -----------------------------------------------------------------------------
# Phase 3: k3s on labyrinth + kubeconfig
# -----------------------------------------------------------------------------

echo "==> [3/14] Installing k3s on labyrinth"
ssh "zakros@${LABYRINTH_IP}" 'sudo bash -s' < deploy/k3s-install.sh

echo "==> [3/14] Pulling kubeconfig back to ~/.kube/zakros.yaml"
mkdir -p "$HOME/.kube"
scp "zakros@${LABYRINTH_IP}:/etc/rancher/k3s/k3s.yaml" "$HOME/.kube/zakros.yaml"
# BSD vs GNU sed compat
if sed --version >/dev/null 2>&1; then
  sed -i "s/127.0.0.1/${LABYRINTH_IP}/" "$HOME/.kube/zakros.yaml"
else
  sed -i '' "s/127.0.0.1/${LABYRINTH_IP}/" "$HOME/.kube/zakros.yaml"
fi
KUBECONFIG="$HOME/.kube/zakros.yaml" kubectl get nodes

# -----------------------------------------------------------------------------
# Phase 4: build + push container images to labyrinth's containerd
# -----------------------------------------------------------------------------

echo "==> [4/14] Building + pushing worker images to labyrinth"
deploy/images-push.sh

# -----------------------------------------------------------------------------
# Phase 5: minos daemon
# -----------------------------------------------------------------------------

echo "==> [5/14] Installing minos on minos VM"
deploy/minos-install.sh

echo "==> [5/14] Waiting for minos /healthz"
for i in $(seq 1 60); do
  if curl -fsS --max-time 2 "${MINOS_URL}/healthz" >/dev/null 2>&1; then
    echo "    minos is up"
    break
  fi
  if [ "$i" -eq 60 ]; then
    echo "minos did not become healthy after 60s — check 'journalctl -u minos -f' on the minos VM" >&2
    exit 1
  fi
  sleep 1
done

# -----------------------------------------------------------------------------
# Phase 6: github-broker (depends on minos's secrets.json being on disk)
# -----------------------------------------------------------------------------

echo "==> [6/14] Installing github-broker alongside minos"
deploy/github-broker-install.sh

# -----------------------------------------------------------------------------
# Phase 7: hecate (Slice H1) — assumes openbao LXC is bootstrapped + sealed
#                              with the hecate-app token already in secrets.json
# -----------------------------------------------------------------------------

echo "==> [7/14] Installing hecate alongside minos"
echo "    (assumes deploy/openbao-bootstrap.sh has run on the openbao LXC and"
echo "     hecate/vault-token is in deploy/secrets.json)"
deploy/hecate-install.sh

# -----------------------------------------------------------------------------
# Phase 8: build minosctl + mint Apollo's service JWT
# -----------------------------------------------------------------------------

echo "==> [8/14] Building bin/minosctl"
mkdir -p bin
go build -o bin/minosctl ./cmd/minosctl

echo "==> [8/14] Minting Apollo's long-lived service JWT"
ADMIN_TOKEN="$(jq -r '.credentials["minos/admin-token"].value' deploy/secrets.json)"
APOLLO_TOKEN="$(MINOS_URL="$MINOS_URL" MINOS_ADMIN_TOKEN="$ADMIN_TOKEN" \
  bin/minosctl mint-apollo-token | tail -1)"
if [ -z "$APOLLO_TOKEN" ]; then
  echo "minosctl mint-apollo-token returned empty" >&2
  exit 1
fi
tmp="$(mktemp)"
jq --arg tok "$APOLLO_TOKEN" \
  '.credentials["minos/apollo-token"].value = $tok' \
  deploy/secrets.json > "$tmp"
mv "$tmp" deploy/secrets.json

# -----------------------------------------------------------------------------
# Phase 9: seed Vault with the upstream Anthropic key, then install Apollo
# -----------------------------------------------------------------------------

echo "==> [9/14] Seeding anthropic-api-key into OpenBao"
echo "    (deploy/openbao-seed.sh requires OPENBAO_ROOT_TOKEN env var)"
if [ -n "${OPENBAO_ROOT_TOKEN:-}" ]; then
  CRETE_HOST="${CRETE_HOST}" deploy/openbao-seed.sh
else
  echo "    SKIPPING — set OPENBAO_ROOT_TOKEN to auto-seed; otherwise seed manually before apollo-install"
fi

echo "==> [9/14] Installing apollo alongside minos"
deploy/apollo-install.sh

# -----------------------------------------------------------------------------
# Phase 10: cloudflared tunnel
# -----------------------------------------------------------------------------

echo "==> [10/14] Installing cloudflared on minos VM"
deploy/cloudflared-install.sh

# -----------------------------------------------------------------------------
# Phase 11: mint iris token, write into secrets.json
# -----------------------------------------------------------------------------

echo "==> [11/14] Minting Iris's long-lived JWT"
IRIS_TOKEN="$(MINOS_URL="$MINOS_URL" MINOS_ADMIN_TOKEN="$ADMIN_TOKEN" \
  bin/minosctl mint-iris-token | tail -1)"
if [ -z "$IRIS_TOKEN" ]; then
  echo "minosctl mint-iris-token returned empty" >&2
  exit 1
fi

tmp="$(mktemp)"
jq --arg tok "$IRIS_TOKEN" \
  '.credentials["minos/iris-token"].value = $tok' \
  deploy/secrets.json > "$tmp"
mv "$tmp" deploy/secrets.json

# -----------------------------------------------------------------------------
# Phase 12-13: iris pod + apollo health
# -----------------------------------------------------------------------------

echo "==> [12/14] Installing Iris on labyrinth"
deploy/iris-install.sh

echo "==> [13/14] Smoke check — broker fleet health"
curl -fsS "${MINOS_URL}/healthz" && echo "    minos /healthz ok"
curl -fsS "${HECATE_URL}/healthz" && echo "    hecate /healthz ok"
curl -fsS "${APOLLO_URL}/healthz" && echo "    apollo /healthz ok"

# -----------------------------------------------------------------------------
# Phase 14: cluster smoke check
# -----------------------------------------------------------------------------

echo "==> [14/14] Cluster pod state"
KUBECONFIG="$HOME/.kube/zakros.yaml" kubectl -n zakros get pods

echo
echo "==> Rebuild complete."
echo "    Next: exercise the end-to-end flow per deploy/README.md §9 (Discord /status, /commission, @iris)."
