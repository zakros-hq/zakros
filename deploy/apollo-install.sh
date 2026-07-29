#!/bin/bash
# Install the Apollo LLM broker on the Minos VM (Slice H2a). Co-located
# with Minos / github-broker / argus / hecate: same secrets.json,
# separate config + systemd unit, separate listen port (8085 by default).
#
# Prerequisites:
#   * Go toolchain
#   * deploy/secrets.json populated with minos/signing-key-pub and
#     minos/apollo-token (run `minosctl mint-apollo-token` once after
#     Minos is up, paste under minos/apollo-token.value)
#   * deploy/apollo.json populated (copy from
#     deploy/templates/apollo.json.example)
#   * Hecate up + secret/data/anthropic-api-key seeded (the operator
#     pastes the upstream Anthropic API key into deploy/secrets.json
#     under credentials/anthropic-api-key, then runs openbao-seed.sh
#     with that ref added to the SEEDS array)
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

for f in deploy/secrets.json deploy/apollo.json; do
  if [ ! -f "$f" ]; then
    echo "Missing: $f" >&2
    if [ "$f" = "deploy/apollo.json" ]; then
      echo "Copy from deploy/templates/apollo.json.example." >&2
    fi
    exit 1
  fi
done

echo "==> Building apollo binary for linux/amd64"
mkdir -p bin
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/apollo-linux-amd64 ./cmd/apollo

echo "==> Staging files to scp"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

cp bin/apollo-linux-amd64                "$STAGE/apollo"
cp deploy/apollo.json                    "$STAGE/apollo.json"
cp deploy/templates/apollo.service       "$STAGE/apollo.service"

ssh "${SSH_USER}@${MINOS_HOST}" 'mkdir -p /tmp/zakros-deploy'
scp "$STAGE"/* "${SSH_USER}@${MINOS_HOST}:/tmp/zakros-deploy/"

echo "==> Installing on ${MINOS_HOST}"
ssh "${SSH_USER}@${MINOS_HOST}" 'sudo bash -s' <<'SSH_EOF'
set -euo pipefail
STAGE=/tmp/zakros-deploy

id zakros >/dev/null

install -o root -g root -m 0755 "$STAGE/apollo" /usr/local/bin/apollo

install -d -o root -g root     -m 0755 /etc/zakros
install -o root    -g zakros   -m 0640 "$STAGE/apollo.json" /etc/zakros/apollo.json
[ -e /etc/zakros/secrets.json ] || ln -s /etc/minos/secrets.json /etc/zakros/secrets.json

install -o root -g root -m 0644 "$STAGE/apollo.service" /etc/systemd/system/apollo.service

systemctl daemon-reload
systemctl enable apollo
systemctl restart apollo

rm -rf "$STAGE"

echo "---"
systemctl --no-pager --full status apollo | head -15 || true
SSH_EOF

echo "==> Done. Tail logs with:"
echo "    ssh ${SSH_USER}@${MINOS_HOST} 'sudo journalctl -u apollo -f'"
