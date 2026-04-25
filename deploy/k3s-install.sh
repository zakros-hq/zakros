#!/bin/bash
# Bootstrap k3s on the labyrinth VM as a single-node cluster.
# Idempotent: safe to re-run (the k3s installer no-ops if already running).
#
# Run from the operator's workstation:
#   ssh zakros@<labyrinth-ip> 'sudo bash -s' < deploy/k3s-install.sh

set -euo pipefail

if systemctl is-active --quiet k3s; then
  echo "==> k3s already running; nothing to do"
  exit 0
fi

echo "==> Installing k3s (single-node, no traefik — ingress is FRP/Cloudflare-side)"
# --disable traefik: no public ingress in-cluster; --write-kubeconfig-mode 644
# so the zakros user can read it; --node-name labyrinth for clarity.
curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="server \
  --disable traefik \
  --write-kubeconfig-mode 644 \
  --node-name labyrinth" sh -

echo "==> Waiting for node Ready"
for _ in $(seq 1 30); do
  if k3s kubectl get nodes 2>/dev/null | grep -q ' Ready '; then
    break
  fi
  sleep 2
done

k3s kubectl get nodes

echo "==> Ensuring 'zakros' namespace exists"
k3s kubectl create namespace zakros --dry-run=client -o yaml | k3s kubectl apply -f -

echo
echo "==> kubeconfig is at /etc/rancher/k3s/k3s.yaml"
echo "    Pull it back to your workstation, replace 127.0.0.1 with the VM's IP:"
echo
echo "    scp zakros@<labyrinth-ip>:/etc/rancher/k3s/k3s.yaml ~/.kube/zakros.yaml"
echo "    sed -i '' 's/127.0.0.1/<labyrinth-ip>/' ~/.kube/zakros.yaml   # GNU: drop the ''"
echo "    KUBECONFIG=~/.kube/zakros.yaml kubectl get nodes"
