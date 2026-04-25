#!/bin/bash
# Install the Minos daemon on a fresh Minos VM.
# Orchestrated from the operator's workstation — this script does the
# build locally, scps the binary + config + kubeconfig to the VM, then
# SSHes in to lay down systemd + start.
#
# Prerequisites on the operator side:
#   * Go toolchain (for `make build`)
#   * A local deploy/config.json + deploy/secrets.json (copy from
#     templates/ and fill in real values; both are gitignored)
#   * ~/.kube/zakros.yaml — the labyrinth kubeconfig from k3s-install
#
# Env:
#   MINOS_HOST      default: minos guest IP from `terraform output -json guests`
#   SSH_USER        default zakros
#   KUBECONFIG_SRC  default ~/.kube/zakros.yaml

set -euo pipefail

. "$(dirname "$0")/lib.sh"
: "${MINOS_HOST:=$(tf_guest_ip minos 2>/dev/null || true)}"
: "${MINOS_HOST:?run terraform apply so the minos guest is in state, or set MINOS_HOST manually}"
: "${SSH_USER:=zakros}"
: "${KUBECONFIG_SRC:=$HOME/.kube/zakros.yaml}"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

# Sanity checks on operator-side inputs.
for f in deploy/config.json deploy/secrets.json "${KUBECONFIG_SRC}"; do
  if [ ! -f "$f" ]; then
    echo "Missing: $f" >&2
    echo "Create it from deploy/templates/ (or run k3s-install.sh first for the kubeconfig)." >&2
    exit 1
  fi
done

echo "==> Building minos binary for linux/amd64"
# Cross-compile explicitly — operator workstation is often darwin/arm64
# but the minos VM is linux/amd64, and `make build` uses native GOOS/ARCH.
mkdir -p bin
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/minos-linux-amd64 ./cmd/minos

echo "==> Staging files to scp"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

cp bin/minos-linux-amd64           "$STAGE/minos"
cp deploy/config.json              "$STAGE/config.json"
cp deploy/secrets.json             "$STAGE/secrets.json"
cp "${KUBECONFIG_SRC}"             "$STAGE/kubeconfig"
cp deploy/templates/minos.service  "$STAGE/minos.service"

scp "$STAGE"/* "${SSH_USER}@${MINOS_HOST}:/tmp/zakros-deploy/" 2>/dev/null \
  || { ssh "${SSH_USER}@${MINOS_HOST}" 'mkdir -p /tmp/zakros-deploy' && scp "$STAGE"/* "${SSH_USER}@${MINOS_HOST}:/tmp/zakros-deploy/"; }

echo "==> Installing on ${MINOS_HOST}"
ssh "${SSH_USER}@${MINOS_HOST}" 'sudo bash -s' <<'SSH_EOF'
set -euo pipefail
STAGE=/tmp/zakros-deploy

# zakros user already exists (cloud-init); ensure group matches
id zakros >/dev/null

install -o root -g root -m 0755 "$STAGE/minos"       /usr/local/bin/minos

install -d -o root      -g root      -m 0755 /etc/minos
install -o root         -g zakros  -m 0640 "$STAGE/config.json"  /etc/minos/config.json
install -o root         -g zakros  -m 0640 "$STAGE/secrets.json" /etc/minos/secrets.json
install -o root         -g zakros  -m 0640 "$STAGE/kubeconfig"   /etc/minos/kubeconfig

install -d -o zakros  -g zakros  -m 0755 /var/log/minos

install -o root         -g root      -m 0644 "$STAGE/minos.service" /etc/systemd/system/minos.service

systemctl daemon-reload
systemctl enable minos
# restart (not just start) so re-runs pick up config/secret/binary changes
systemctl restart minos

rm -rf "$STAGE"

echo "---"
systemctl --no-pager --full status minos | head -20 || true
SSH_EOF

echo "==> Done. Tail logs with:"
echo "    ssh ${SSH_USER}@${MINOS_HOST} 'sudo journalctl -u minos -f'"
