#!/bin/bash
# Install Cloudflare Tunnel (cloudflared) on the minos VM as a systemd
# service. Uses a "remotely managed" tunnel — operator configures
# hostnames + ingress rules in the Cloudflare Zero Trust dashboard,
# cloudflared just authenticates with a token and establishes the
# outbound tunnel.
#
# Prerequisites (one-time, in Cloudflare Zero Trust dashboard):
#   1. Zero Trust → Networks → Tunnels → Create a tunnel
#   2. Name it (e.g. "daedalus"), pick Cloudflared, copy the token
#      shown on the "Install and run a connector" screen
#   3. Public Hostname → add subdomain → service: http://localhost:8080
#      (point the /webhooks/github path at minos)
#
# Then from the operator's workstation:
#   CLOUDFLARED_TOKEN='<the long token>' deploy/cloudflared-install.sh
#
# Env:
#   MINOS_HOST           default 172.16.140.101
#   SSH_USER             default daedalus
#   CLOUDFLARED_TOKEN    required — the tunnel token from the dashboard

set -euo pipefail

: "${MINOS_HOST:=172.16.140.101}"
: "${SSH_USER:=daedalus}"
: "${CLOUDFLARED_TOKEN:?must be set}"

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
