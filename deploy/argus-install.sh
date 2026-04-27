#!/bin/bash
# Install the extracted Argus binary on the Minos VM (Slice J).
# Co-located with Minos: same secrets.json, same kubeconfig, separate
# config + systemd unit, separate listen port (8083 by default).
#
# Prerequisites:
#   * Go toolchain
#   * deploy/secrets.json populated (Argus reads minos/signing-key-pub)
#   * deploy/argus.json populated (database_url at minimum; usually
#     copy database_url verbatim from deploy/config.json)
#
# Env:
#   MINOS_HOST   default: terraform output -> guests.minos.ip
#   SSH_USER     default zakros

set -euo pipefail

. "$(dirname "$0")/lib.sh"
: "${MINOS_HOST:=$(tf_guest_ip minos 2>/dev/null || true)}"
: "${MINOS_HOST:?run terraform apply so the minos guest is in state, or set MINOS_HOST manually}"
: "${SSH_USER:=zakros}"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

for f in deploy/secrets.json deploy/argus.json; do
  if [ ! -f "$f" ]; then
    echo "Missing: $f" >&2
    if [ "$f" = "deploy/argus.json" ]; then
      echo "Copy from deploy/templates/argus.json.example and fill in database_url." >&2
    fi
    exit 1
  fi
done

echo "==> Building argus binary for linux/amd64"
mkdir -p bin
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/argus-linux-amd64 ./cmd/argus

echo "==> Staging files to scp"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

cp bin/argus-linux-amd64                "$STAGE/argus"
cp deploy/argus.json                    "$STAGE/argus.json"
cp deploy/templates/argus.service       "$STAGE/argus.service"

ssh "${SSH_USER}@${MINOS_HOST}" 'mkdir -p /tmp/zakros-deploy'
scp "$STAGE"/* "${SSH_USER}@${MINOS_HOST}:/tmp/zakros-deploy/"

echo "==> Installing on ${MINOS_HOST}"
ssh "${SSH_USER}@${MINOS_HOST}" 'sudo bash -s' <<'SSH_EOF'
set -euo pipefail
STAGE=/tmp/zakros-deploy

id zakros >/dev/null

install -o root -g root -m 0755 "$STAGE/argus" /usr/local/bin/argus

install -d -o root -g root     -m 0755 /etc/zakros
install -o root    -g zakros   -m 0640 "$STAGE/argus.json" /etc/zakros/argus.json
# Symlink the secrets + kubeconfig that Minos already manages.
[ -e /etc/zakros/secrets.json ] || ln -s /etc/minos/secrets.json /etc/zakros/secrets.json

install -o root -g root -m 0644 "$STAGE/argus.service" /etc/systemd/system/argus.service

systemctl daemon-reload
systemctl enable argus
systemctl restart argus

rm -rf "$STAGE"

echo "---"
systemctl --no-pager --full status argus | head -15 || true
SSH_EOF

echo "==> Done. Tail logs with:"
echo "    ssh ${SSH_USER}@${MINOS_HOST} 'sudo journalctl -u argus -f'"
