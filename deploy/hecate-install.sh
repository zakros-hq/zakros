#!/bin/bash
# Install the Hecate credentials broker on the Minos VM (Slice H1).
# Co-located with Minos / github-broker / argus: same secrets.json,
# separate config + systemd unit, separate listen port (8084 by default).
#
# Prerequisites:
#   * Go toolchain
#   * deploy/secrets.json populated (Hecate reads minos/signing-key-pub
#     and hecate/vault-token)
#   * deploy/hecate.json populated with vault_addr (the OpenBao LXC URL)
#   * OpenBao up + initialized (see deploy/openbao-bootstrap.sh) and
#     hecate/vault-token pasted into deploy/secrets.json
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

for f in deploy/secrets.json deploy/hecate.json; do
  if [ ! -f "$f" ]; then
    echo "Missing: $f" >&2
    if [ "$f" = "deploy/hecate.json" ]; then
      echo "Copy from deploy/templates/hecate.json.example and fill in vault_addr." >&2
    fi
    exit 1
  fi
done

echo "==> Building hecate binary for linux/amd64"
mkdir -p bin
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/hecate-linux-amd64 ./cmd/hecate

echo "==> Staging files to scp"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

cp bin/hecate-linux-amd64                "$STAGE/hecate"
cp deploy/hecate.json                    "$STAGE/hecate.json"
cp deploy/templates/hecate.service       "$STAGE/hecate.service"

ssh "${SSH_USER}@${MINOS_HOST}" 'mkdir -p /tmp/zakros-deploy'
scp "$STAGE"/* "${SSH_USER}@${MINOS_HOST}:/tmp/zakros-deploy/"

echo "==> Installing on ${MINOS_HOST}"
ssh "${SSH_USER}@${MINOS_HOST}" 'sudo bash -s' <<'SSH_EOF'
set -euo pipefail
STAGE=/tmp/zakros-deploy

id zakros >/dev/null

install -o root -g root -m 0755 "$STAGE/hecate" /usr/local/bin/hecate

install -d -o root -g root     -m 0755 /etc/zakros
install -o root    -g zakros   -m 0640 "$STAGE/hecate.json" /etc/zakros/hecate.json
[ -e /etc/zakros/secrets.json ] || ln -s /etc/minos/secrets.json /etc/zakros/secrets.json

install -o root -g root -m 0644 "$STAGE/hecate.service" /etc/systemd/system/hecate.service

systemctl daemon-reload
systemctl enable hecate
systemctl restart hecate

rm -rf "$STAGE"

echo "---"
systemctl --no-pager --full status hecate | head -15 || true
SSH_EOF

echo "==> Done. Tail logs with:"
echo "    ssh ${SSH_USER}@${MINOS_HOST} 'sudo journalctl -u hecate -f'"
