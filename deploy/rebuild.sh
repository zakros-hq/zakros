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
required_secrets=(
  minos/signing-key
  minos/signing-key-pub
  minos/admin-token
  cerberus/github-webhook
  github/app-private-key
  hermes/discord-bot-token
  claude-code/oauth-token
  anthropic/api-key
  cloudflared/tunnel-token
)
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
  echo "==> [0/10] tf-destroy + tf-apply (--from-scratch)"
  make tf-destroy
  make tf-apply
else
  echo "==> [0/10] tf-apply (idempotent)"
  make tf-apply
fi

# -----------------------------------------------------------------------------
# Phase 1: refresh config.json with current guest IPs
# -----------------------------------------------------------------------------

echo "==> [1/10] Rewriting deploy/config.json with fresh guest IPs"

POSTGRES_IP="$(tf_guest_ip postgres)"
MINOS_IP="$(tf_guest_ip minos)"
LABYRINTH_IP="$(tf_guest_ip labyrinth)"
: "${POSTGRES_IP:?terraform output has no postgres ip}"
: "${MINOS_IP:?terraform output has no minos ip}"
: "${LABYRINTH_IP:?terraform output has no labyrinth ip}"

DB_URL_NEW="postgres://zakros:${POSTGRES_PASSWORD}@${POSTGRES_IP}:5432/zakros?sslmode=disable"
MINOS_URL="http://${MINOS_IP}:8080"
BROKER_URL="http://${MINOS_IP}:8082"

tmp="$(mktemp)"
jq \
  --arg db "$DB_URL_NEW" \
  --arg mp "$MINOS_URL" \
  --arg bp "$BROKER_URL" \
  '
    .database_url          = $db
  | .minos_pod_url         = $mp
  | .github_broker_pod_url = $bp
  | .project.capabilities.mcp_endpoints
      = (.project.capabilities.mcp_endpoints
         | map(if .name == "github" then .url = $bp else . end))
  ' deploy/config.json > "$tmp"
mv "$tmp" deploy/config.json

echo "    postgres   $POSTGRES_IP"
echo "    minos      $MINOS_IP"
echo "    labyrinth  $LABYRINTH_IP"

# -----------------------------------------------------------------------------
# Phase 2: postgres bootstrap + migrations
# -----------------------------------------------------------------------------

echo "==> [2/10] Bootstrapping postgres LXC (vmid 211)"

CRETE_HOST="${CRETE_HOST:-172.16.30.103}"
ssh "root@${CRETE_HOST}" \
  "POSTGRES_PASSWORD='${POSTGRES_PASSWORD}' pct exec 211 -- bash" \
  < deploy/postgres-bootstrap.sh

echo "==> [2/10] Waiting for postgres to accept connections"
for _ in $(seq 1 30); do
  if (echo > "/dev/tcp/${POSTGRES_IP}/5432") 2>/dev/null; then
    break
  fi
  sleep 2
done

echo "==> [2/10] Running goose migrations"
"$GOOSE_BIN" -dir minos/storage/pgstore/migrations \
  postgres "$DB_URL_NEW" up

# -----------------------------------------------------------------------------
# Phase 3: k3s on labyrinth + kubeconfig
# -----------------------------------------------------------------------------

echo "==> [3/10] Installing k3s on labyrinth"
ssh "zakros@${LABYRINTH_IP}" 'sudo bash -s' < deploy/k3s-install.sh

echo "==> [3/10] Pulling kubeconfig back to ~/.kube/zakros.yaml"
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

echo "==> [4/10] Building + pushing worker images to labyrinth"
deploy/images-push.sh

# -----------------------------------------------------------------------------
# Phase 5: minos daemon
# -----------------------------------------------------------------------------

echo "==> [5/10] Installing minos on minos VM"
deploy/minos-install.sh

echo "==> [5/10] Waiting for minos /healthz"
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

echo "==> [6/10] Installing github-broker alongside minos"
deploy/github-broker-install.sh

# -----------------------------------------------------------------------------
# Phase 7: cloudflared tunnel
# -----------------------------------------------------------------------------

echo "==> [7/10] Installing cloudflared on minos VM"
deploy/cloudflared-install.sh

# -----------------------------------------------------------------------------
# Phase 8: mint iris token, write into secrets.json
# -----------------------------------------------------------------------------

echo "==> [8/10] Building bin/minosctl"
mkdir -p bin
go build -o bin/minosctl ./cmd/minosctl

echo "==> [8/10] Minting Iris's long-lived JWT"
ADMIN_TOKEN="$(jq -r '.credentials["minos/admin-token"].value' deploy/secrets.json)"
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
# Phase 9: iris pod
# -----------------------------------------------------------------------------

echo "==> [9/10] Installing Iris on labyrinth"
deploy/iris-install.sh

# -----------------------------------------------------------------------------
# Phase 10: smoke check
# -----------------------------------------------------------------------------

echo "==> [10/10] Smoke check"
curl -fsS "${MINOS_URL}/healthz" && echo "    minos /healthz ok"
KUBECONFIG="$HOME/.kube/zakros.yaml" kubectl -n zakros get pods

echo
echo "==> Rebuild complete."
echo "    Next: exercise the end-to-end flow per deploy/README.md §9 (Discord /status, /commission, @iris)."
