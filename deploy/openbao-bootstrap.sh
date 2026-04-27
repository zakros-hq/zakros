#!/bin/bash
# Bootstrap an OpenBao instance on the Zakros openbao LXC for Slice H1
# Hecate. Idempotent on the package-install + config-file paths;
# init/unseal is one-shot (re-running after init detects the existing
# state and skips). Outputs the unseal keys + root token + hecate-app
# token to /root/openbao-bootstrap.out so the operator can copy them
# off the LXC and into deploy/secrets.json.
#
# Run from the operator's workstation:
#   ssh root@<crete-ip> "pct exec 214 -- bash" < deploy/openbao-bootstrap.sh
#
# After bootstrap:
#   1. ssh root@<crete> "pct exec 214 -- cat /root/openbao-bootstrap.out"
#   2. Save the 5 unseal keys somewhere safe (e.g. operator workstation
#      secret store). 3-of-5 quorum needed to unseal after restarts.
#   3. Paste the hecate-app token into deploy/secrets.json under
#      "hecate/vault-token". This is what cmd/hecate uses to talk to
#      OpenBao on every credential fetch.
#   4. Seed each project credential into Vault KV:
#        bao kv put secret/claude-code-token value="$(jq -r ... claude-code/oauth-token)"
#        bao kv put secret/github-app-private-key value="$(...)"
#      (See deploy/openbao-seed.sh helper.)

set -euo pipefail

OPENBAO_VERSION="${OPENBAO_VERSION:-2.5.0}"
OPENBAO_DIR=/etc/openbao
OPENBAO_DATA=/var/lib/openbao
OPENBAO_CONFIG="${OPENBAO_DIR}/openbao.hcl"
OUT_FILE=/root/openbao-bootstrap.out

echo "==> apt update + base deps"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq curl ca-certificates gnupg lsb-release jq

if ! command -v bao >/dev/null; then
  echo "==> Installing OpenBao ${OPENBAO_VERSION}"
  ARCH=$(dpkg --print-architecture)
  TMPDEB=/tmp/openbao_${OPENBAO_VERSION}.deb
  curl -fsSL "https://github.com/openbao/openbao/releases/download/v${OPENBAO_VERSION}/bao_${OPENBAO_VERSION}_linux_${ARCH}.deb" -o "${TMPDEB}"
  dpkg -i "${TMPDEB}"
  rm -f "${TMPDEB}"
fi

if ! id openbao >/dev/null 2>&1; then
  useradd --system --home "${OPENBAO_DATA}" --shell /usr/sbin/nologin openbao
fi
install -d -m 0750 -o openbao -g openbao "${OPENBAO_DATA}" "${OPENBAO_DIR}"

if [ ! -f "${OPENBAO_CONFIG}" ]; then
  echo "==> Writing ${OPENBAO_CONFIG}"
  cat > "${OPENBAO_CONFIG}" <<HCL
ui = true
disable_mlock = true

storage "raft" {
  path    = "${OPENBAO_DATA}"
  node_id = "openbao-1"
}

listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = 1
}

api_addr     = "http://0.0.0.0:8200"
cluster_addr = "http://0.0.0.0:8201"
HCL
  chown openbao:openbao "${OPENBAO_CONFIG}"
  chmod 0640 "${OPENBAO_CONFIG}"
fi

if [ ! -f /etc/systemd/system/openbao.service ]; then
  echo "==> Writing openbao.service"
  cat > /etc/systemd/system/openbao.service <<UNIT
[Unit]
Description=OpenBao secret store
Documentation=https://openbao.org/
After=network-online.target
Wants=network-online.target

[Service]
Type=notify
User=openbao
Group=openbao
ExecStart=/usr/bin/bao server -config=${OPENBAO_CONFIG}
Restart=on-failure
RestartSec=5s
LimitNOFILE=65536
LimitMEMLOCK=infinity

[Install]
WantedBy=multi-user.target
UNIT
  systemctl daemon-reload
fi

systemctl enable openbao
systemctl restart openbao

# Wait for the listener to come up before any bao commands.
echo "==> Waiting for OpenBao listener"
for i in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:8200/v1/sys/health 2>&1 | grep -q '"initialized"'; then
    break
  fi
  sleep 1
done

export VAULT_ADDR=http://127.0.0.1:8200
export BAO_ADDR=http://127.0.0.1:8200

INIT_STATUS=$(curl -fsS http://127.0.0.1:8200/v1/sys/init | jq -r .initialized)
if [ "${INIT_STATUS}" = "true" ]; then
  echo "==> OpenBao is already initialized — skipping init step"
  echo "    Existing unseal keys + root token are NOT recoverable from this script."
  echo "    If you need to re-init, manually rm -rf ${OPENBAO_DATA} and re-run."
  exit 0
fi

echo "==> Initializing OpenBao (3-of-5 unseal quorum)"
INIT_OUT=$(bao operator init -key-shares=5 -key-threshold=3 -format=json)
echo "${INIT_OUT}" > "${OUT_FILE}"
chmod 0600 "${OUT_FILE}"

ROOT_TOKEN=$(echo "${INIT_OUT}" | jq -r .root_token)
UNSEAL_KEYS=$(echo "${INIT_OUT}" | jq -r '.unseal_keys_b64[]')

echo "==> Unsealing"
n=0
for key in ${UNSEAL_KEYS}; do
  bao operator unseal "${key}" >/dev/null
  n=$((n+1))
  if [ "${n}" -ge 3 ]; then break; fi
done

export VAULT_TOKEN="${ROOT_TOKEN}"
export BAO_TOKEN="${ROOT_TOKEN}"

# KV v2 mount for project credentials. Path is `secret/`; reads land
# at `secret/data/<key>` per KV-v2 layout.
echo "==> Enabling KV v2 mount at secret/"
bao secrets enable -path=secret -version=2 kv >/dev/null 2>&1 || true

# Hecate's app policy: read-only on every KV path under secret/.
# Slice H1 single-tenant simplification — Phase 2 K hardening replaces
# this with policy-per-scope minting.
echo "==> Writing hecate-app policy"
bao policy write hecate-app - <<'POL' >/dev/null
path "secret/data/*" {
  capabilities = ["read"]
}
path "secret/metadata/*" {
  capabilities = ["read", "list"]
}
POL

echo "==> Issuing hecate-app token"
HECATE_TOKEN_JSON=$(bao token create \
  -policy=hecate-app \
  -ttl=8760h \
  -renewable=true \
  -display-name=hecate-app \
  -format=json)
HECATE_TOKEN=$(echo "${HECATE_TOKEN_JSON}" | jq -r .auth.client_token)

cat >> "${OUT_FILE}" <<APPEND

# --- Slice H1 Hecate broker token (paste under hecate/vault-token in
#     deploy/secrets.json) ---
HECATE_VAULT_TOKEN=${HECATE_TOKEN}
APPEND

echo
echo "==> Done. Read /root/openbao-bootstrap.out for the unseal keys + tokens."
echo "    DO NOT lose the unseal keys. Without 3-of-5 you can't recover after a restart."
