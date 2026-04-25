#!/bin/bash
# Install Cloudflare Tunnel (cloudflared) on the minos VM as a systemd
# service. Uses a "remotely managed" tunnel — operator configures
# hostnames + ingress rules in the Cloudflare Zero Trust dashboard,
# cloudflared just authenticates with a token and establishes the
# outbound tunnel.
#
# Prerequisites (one-time, in Cloudflare Zero Trust dashboard):
#   1. Zero Trust → Networks → Tunnels → Create a tunnel
#   2. Name it (e.g. "zakros"), pick Cloudflared, copy the token
#      shown on the "Install and run a connector" screen
#   3. Public Hostname → add subdomain → service: http://localhost:8080
#      (point the /webhooks/github path at minos)
#
# Then from the operator's workstation:
#   CLOUDFLARED_TOKEN='<the long token>' deploy/cloudflared-install.sh
#
# Env:
#   MINOS_HOST           default: minos guest IP from `terraform output -json guests`
#   SSH_USER             default zakros
#   CLOUDFLARED_TOKEN    optional — tunnel token. Defaults to the
#                        `cloudflared/tunnel-token` entry in
#                        deploy/secrets.json so the token lives alongside
#                        the rest of the Zakros credentials.

set -euo pipefail

. "$(dirname "$0")/lib.sh"
: "${MINOS_HOST:=$(tf_guest_ip minos 2>/dev/null || true)}"
: "${MINOS_HOST:?run terraform apply so the minos guest is in state, or set MINOS_HOST manually}"
: "${SSH_USER:=zakros}"

# Fall back to secrets.json if env var isn't set.
if [ -z "${CLOUDFLARED_TOKEN:-}" ]; then
  REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
  SECRETS_FILE="${REPO_ROOT}/deploy/secrets.json"
  if [ -f "${SECRETS_FILE}" ]; then
    CLOUDFLARED_TOKEN="$(jq -r '.credentials["cloudflared/tunnel-token"].value // empty' "${SECRETS_FILE}")"
  fi
fi
: "${CLOUDFLARED_TOKEN:?set CLOUDFLARED_TOKEN env var or add cloudflared/tunnel-token to deploy/secrets.json}"

echo "==> Installing cloudflared on ${MINOS_HOST}"
ssh "${SSH_USER}@${MINOS_HOST}" "CLOUDFLARED_TOKEN='${CLOUDFLARED_TOKEN}' sudo -E bash -s" <<'SSH_EOF'
set -euo pipefail

if ! command -v cloudflared >/dev/null 2>&1; then
  echo "==> Adding Cloudflare apt repo"
  mkdir -p --mode=0755 /usr/share/keyrings
  curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg \
    | tee /usr/share/keyrings/cloudflare-main.gpg >/dev/null
  echo "deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared $(lsb_release -cs) main" \
    > /etc/apt/sources.list.d/cloudflared.list
  apt-get update -qq
  apt-get install -y cloudflared
fi

# `cloudflared service install <token>` writes /etc/systemd/system/cloudflared.service
# and starts it. If already installed, uninstall+reinstall so the token updates cleanly.
if systemctl is-active --quiet cloudflared 2>/dev/null; then
  echo "==> cloudflared already installed; reinstalling to refresh token"
  cloudflared service uninstall || true
fi

cloudflared service install "${CLOUDFLARED_TOKEN}"

sleep 2
systemctl --no-pager --full status cloudflared | head -15 || true
SSH_EOF

echo "==> Done. Tail logs with:"
echo "    ssh ${SSH_USER}@${MINOS_HOST} 'sudo journalctl -u cloudflared -f'"
echo
echo "==> Verify the tunnel is reachable (replace <hostname>):"
echo "    curl -v https://<hostname>/healthz"
