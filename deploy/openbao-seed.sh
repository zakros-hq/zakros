#!/bin/bash
# Seed project credentials into OpenBao KV from deploy/secrets.json.
# One-shot helper; run after deploy/openbao-bootstrap.sh to copy each
# pod-side credential into Vault. After this, the operator can clear
# the corresponding entries from deploy/secrets.json (their values
# only existed there for bootstrap, not runtime).
#
# Slice H2a default mapping:
#   anthropic-api-key  →  secret/data/anthropic-api-key
#
# (H1 used claude-code/oauth-token → secret/data/claude-code-token; H2a
# replaces it because the worker pod no longer holds an Anthropic
# credential — Apollo fronts every Anthropic call.)
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
  "anthropic-api-key:anthropic-api-key"
)

# Operator passes the root token explicitly. Pulling it programmatically
# via jq broke against the corrupt-from-prior-bootstrap file shape and
# pretty-printed JSON output; explicit env var sidesteps that whole
# parsing class. The token is one-shot — Hecate uses its own long-lived
# hecate-app token at runtime, this is just for the kv-put pass.
: "${OPENBAO_ROOT_TOKEN:?run: OPENBAO_ROOT_TOKEN=\"\$(ssh root@${CRETE_HOST} pct exec ${OPENBAO_LXC} -- cat /root/openbao-bootstrap.out)\" then extract \"root_token\" value, or copy from openbao-bootstrap.out and pass it on the command line}"
ROOT_TOKEN="${OPENBAO_ROOT_TOKEN}"

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
echo "    ssh root@${CRETE_HOST} \"pct exec ${OPENBAO_LXC} -- env BAO_TOKEN='${ROOT_TOKEN}' BAO_ADDR=http://127.0.0.1:8200 bao kv list secret/\""
