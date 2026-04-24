#!/bin/bash
# Install the Iris conversational pod into the labyrinth k3s cluster.
# Reads deploy/config.json + deploy/secrets.json for the values that
# need to land in the Iris Deployment's Secret.
#
# Prerequisites on the operator side:
#   * deploy/config.json + deploy/secrets.json populated
#   * Operator has run deploy/images-push.sh so daedalus/iris:local
#     is in labyrinth's containerd
#   * ~/.kube/daedalus.yaml — the labyrinth kubeconfig
#
# Env:
#   IMAGE_TAG       default local
#   KUBECONFIG_SRC  default ~/.kube/daedalus.yaml

set -euo pipefail

: "${IMAGE_TAG:=local}"
: "${KUBECONFIG_SRC:=$HOME/.kube/daedalus.yaml}"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

for f in deploy/config.json deploy/secrets.json deploy/templates/iris-deployment.yaml "${KUBECONFIG_SRC}"; do
  if [ ! -f "$f" ]; then
    echo "Missing: $f" >&2
    exit 1
  fi
done

if ! command -v jq >/dev/null; then
  echo "jq required" >&2
  exit 1
fi
if ! command -v kubectl >/dev/null; then
  echo "kubectl required" >&2
  exit 1
fi

# Pull values from the operator's config + secrets. Iris reuses the
# admin token (Phase 1 single-admin posture: Iris commissions on the
# operator's behalf). Anthropic credential falls back to claude-code's
# OAuth token if no separate Anthropic API key is configured.
get_secret() {
  jq -r --arg k "$1" '.credentials[$k].value // empty' deploy/secrets.json
}

IRIS_BEARER=$(get_secret "minos/iris-token")
IRIS_ADMIN_TOKEN=$(get_secret "minos/admin-token")
ANTHROPIC_KEY=$(get_secret "anthropic/api-key")
if [ -z "$ANTHROPIC_KEY" ]; then
  ANTHROPIC_KEY=$(get_secret "claude-code/oauth-token")
fi

if [ -z "$IRIS_BEARER" ]; then
  echo "secrets.json missing minos/iris-token" >&2
  exit 1
fi
if [ -z "$IRIS_ADMIN_TOKEN" ]; then
  echo "secrets.json missing minos/admin-token" >&2
  exit 1
fi
if [ -z "$ANTHROPIC_KEY" ]; then
  echo "secrets.json missing anthropic/api-key (or claude-code/oauth-token fallback)" >&2
  exit 1
fi

DATABASE_URL=$(jq -r '.database_url' deploy/config.json)
MINOS_URL=$(jq -r '.minos_pod_url' deploy/config.json)
PROJECT_ID=$(jq -r '.project.id' deploy/config.json)
DEFAULT_REPO=$(jq -r '.project.default_repo_url // ""' deploy/config.json)

if [ -z "$DATABASE_URL" ] || [ "$DATABASE_URL" = "null" ]; then
  echo "config.json missing database_url" >&2
  exit 1
fi

echo "==> Rendering iris-deployment manifest"
TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT
sed \
  -e "s|REPLACE_IRIS_BEARER|${IRIS_BEARER}|g" \
  -e "s|REPLACE_IRIS_ADMIN_TOKEN|${IRIS_ADMIN_TOKEN}|g" \
  -e "s|REPLACE_ANTHROPIC_KEY|${ANTHROPIC_KEY}|g" \
  -e "s|REPLACE_DATABASE_URL|${DATABASE_URL}|g" \
  -e "s|REPLACE_MINOS_URL|${MINOS_URL}|g" \
  -e "s|REPLACE_PROJECT_ID|${PROJECT_ID}|g" \
  -e "s|REPLACE_DEFAULT_REPO_URL|${DEFAULT_REPO}|g" \
  -e "s|daedalus/iris:local|daedalus/iris:${IMAGE_TAG}|g" \
  deploy/templates/iris-deployment.yaml > "$TMP"

echo "==> Applying to labyrinth"
KUBECONFIG="${KUBECONFIG_SRC}" kubectl apply -f "$TMP"

echo "==> Waiting for Iris pod to become ready (60s)"
KUBECONFIG="${KUBECONFIG_SRC}" kubectl -n daedalus rollout status deploy/iris --timeout=60s || true

echo "==> Tail logs with:"
echo "    KUBECONFIG=${KUBECONFIG_SRC} kubectl -n daedalus logs -f deploy/iris"
