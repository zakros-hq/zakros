#!/bin/bash
# Seed project credentials into OpenBao KV from deploy/secrets.json.
# One-shot helper; run after deploy/openbao-bootstrap.sh to copy each
# pod-side credential into Vault. After this, the operator can clear
# the corresponding entries from deploy/secrets.json (their values
# only existed there for bootstrap, not runtime).
#
# Slice H1 default mapping:
#   claude-code/oauth-token  →  secret/data/claude-code-token
#
# Run from the operator's workstation. Requires OPENBAO_LXC env var
# pointing at the LXC's vmid (default 214). The script copies values
# *into the LXC* via stdin and runs `bao kv put` there with the root
# token from /root/openbao-bootstrap.out.

set -euo pipefail

: "${OPENBAO_LXC:=214}"
: "${CRETE_HOST:=$(terraform -chdir=terraform output -raw proxmox_host 2>/dev/null || echo)}"
: "${CRETE_HOST:?set CRETE_HOST to the Proxmox node IP}"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

if [ ! -f deploy/secrets.json ]; then
  echo "deploy/secrets.json missing" >&2
  exit 1
fi

get() {
  jq -r --arg k "$1" '.credentials[$k].value // empty' deploy/secrets.json
}

# Pull each (file-ref, vault-ref) pair from secrets.json.
declare -a SEEDS=(
  "claude-code/oauth-token:claude-code-token"
)

# Extract the root token from the LXC's bootstrap output. Operator
# uses this to write to Vault one-shot — it's not stored anywhere.
ROOT_TOKEN=$(ssh root@${CRETE_HOST} "pct exec ${OPENBAO_LXC} -- jq -r .root_token /root/openbao-bootstrap.out")
if [ -z "$ROOT_TOKEN" ] || [ "$ROOT_TOKEN" = "null" ]; then
  echo "could not read root token from /root/openbao-bootstrap.out on LXC ${OPENBAO_LXC}" >&2
  exit 1
fi

for pair in "${SEEDS[@]}"; do
  fileref="${pair%%:*}"
  vaultref="${pair##*:}"
  val=$(get "$fileref")
  if [ -z "$val" ] || [[ "$val" == REPLACE_* ]]; then
    echo "skip ${fileref} → ${vaultref} (placeholder or empty in secrets.json)"
    continue
  fi
  echo "==> seeding secret/${vaultref} from ${fileref}"
  ssh root@${CRETE_HOST} "pct exec ${OPENBAO_LXC} -- env BAO_TOKEN='${ROOT_TOKEN}' BAO_ADDR=http://127.0.0.1:8200 bao kv put secret/${vaultref} value='${val}'" >/dev/null
done

echo
echo "==> Done. Verify with:"
echo "    ssh root@${CRETE_HOST} \"pct exec ${OPENBAO_LXC} -- env BAO_TOKEN='\$(jq -r .root_token /root/openbao-bootstrap.out)' BAO_ADDR=http://127.0.0.1:8200 bao kv list secret/\""
