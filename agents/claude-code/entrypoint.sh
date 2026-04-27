#!/usr/bin/env bash
# Daedalus Claude Code worker entrypoint.
#
# Contract (per agents/plugin/plugin.go and architecture.md §8):
#   - Envelope JSON mounted at $ZAKROS_ENVELOPE (default /var/run/zakros/envelope.json)
#   - GITHUB_TOKEN, ANTHROPIC_API_KEY set via injected_credentials
#   - Workspace: /workspace (EmptyDir, cloned into)
#   - Memory extraction: /var/run/zakros/memory/run-record.json on SIGTERM or exit
#   - Exit 0 on success (PR opened), non-zero on failure
#
# Phase 1 Slice A scope: clone, checkout, run claude-code, commit, push,
# open PR. No thread sidecar calls yet (Slice B). No memory extraction
# integration yet (Slice C reads /var/run/zakros/memory/).

set -euo pipefail

: "${ZAKROS_ENVELOPE:=/var/run/zakros/envelope.json}"
: "${ZAKROS_MEMORY_DIR:=/var/run/zakros/memory}"
: "${WORKSPACE:=/workspace}"
: "${ZAKROS_MINOS_URL:=}"
: "${ZAKROS_GITHUB_BROKER_URL:=}"

log()  { printf '[%s] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
die()  { log "ERROR: $*"; post_status "status" "error: $*" "" || true; flush_memory "failed" "$*"; exit 1; }

# post_status narrates mid-run progress to Minos, which forwards to the
# task thread via Hermes. Best-effort: failures log but never abort the
# run. Usage: post_status <kind> <content> [language]
#   kind: status | thinking | code | request_human_input
post_status() {
  local kind="${1:-status}"
  local content="$2"
  local language="${3:-}"
  if [[ -z "$ZAKROS_MINOS_URL" || -z "${MCP_AUTH_TOKEN:-}" || -z "$content" ]]; then
    return 0
  fi
  local payload
  payload=$(jq -n \
    --arg kind "$kind" \
    --arg content "$content" \
    --arg language "$language" \
    '{kind: $kind, content: $content, language: $language}')
  local status
  status=$(curl -sS -o /tmp/post-response -w '%{http_code}' \
    -X POST \
    -H "Authorization: Bearer $MCP_AUTH_TOKEN" \
    -H "Content-Type: application/json" \
    --data "$payload" \
    "$ZAKROS_MINOS_URL/tasks/$ZAKROS_TASK_ID/post" \
    || echo "000")
  if [[ "$status" != "200" ]]; then
    log "WARN: narration post returned $status"
  fi
}

# flush_memory writes a minimal run record for Mnemosyne (Slice C consumes).
flush_memory() {
  local outcome="${1:-unknown}"
  local message="${2:-}"
  mkdir -p "$ZAKROS_MEMORY_DIR"
  jq -n \
    --arg task_id    "${ZAKROS_TASK_ID:-}" \
    --arg run_id     "${ZAKROS_RUN_ID:-}" \
    --arg project_id "${ZAKROS_PROJECT_ID:-}" \
    --arg outcome    "$outcome" \
    --arg message    "$message" \
    --arg summary    "${RUN_SUMMARY:-}" \
    '{task_id: $task_id, run_id: $run_id, project_id: $project_id, outcome: $outcome, message: $message, summary: $summary}' \
    > "$ZAKROS_MEMORY_DIR/run-record.json"
  log "memory blob written: $ZAKROS_MEMORY_DIR/run-record.json ($outcome)"
}

on_sigterm() {
  log "received SIGTERM; flushing memory"
  flush_memory "${LAST_OUTCOME:-terminated}" "SIGTERM received"
  exit 143
}
trap on_sigterm TERM INT

# Preconditions
[[ -f "$ZAKROS_ENVELOPE" ]] || die "envelope not found at $ZAKROS_ENVELOPE"
command -v jq  >/dev/null || die "jq not installed in image"
command -v git >/dev/null || die "git not installed in image"
command -v gh  >/dev/null || die "gh CLI not installed in image"
command -v claude >/dev/null || die "claude (Claude Code CLI) not installed in image"
# GITHUB_TOKEN is no longer injected directly; we mint a per-task App
# installation token from the github broker just before clone.
[[ -n "$ZAKROS_GITHUB_BROKER_URL" ]] || die "ZAKROS_GITHUB_BROKER_URL not set (Slice F: pod must mint installation tokens via the broker)"
[[ -n "${MCP_AUTH_TOKEN:-}" ]] || die "MCP_AUTH_TOKEN not injected (required to authenticate to the github broker)"

# Slice H1: fetch any HecateCredentials before parsing the envelope.
# The pod JWT carries credentials.fetch:<ref> scopes; Hecate verifies
# scope match and returns the value. We export each as the named env
# var so downstream invocations (claude, gh, custom hooks) see it
# the same way they saw a Minos-pushed env var pre-Slice-H1.
if [[ -n "${ZAKROS_HECATE_FETCHES:-}" ]]; then
  [[ -n "${ZAKROS_HECATE_URL:-}" ]] || die "ZAKROS_HECATE_FETCHES set but ZAKROS_HECATE_URL is empty"
  log "fetching Hecate-resolved credentials"
  # ZAKROS_HECATE_FETCHES is a JSON array of {env_var, ref} objects.
  # Iterate via jq -c so each line is one credential spec.
  while IFS= read -r spec; do
    env_var=$(printf '%s' "$spec" | jq -r '.env_var')
    ref=$(printf '%s' "$spec" | jq -r '.ref')
    [[ -n "$env_var" && -n "$ref" ]] || die "hecate credential spec missing env_var/ref: $spec"
    response=$(curl -sS -o /tmp/hecate-fetch -w '%{http_code}' \
      -H "Authorization: Bearer $MCP_AUTH_TOKEN" \
      "$ZAKROS_HECATE_URL/credentials/fetch/$ref" || echo "000")
    [[ "$response" == "200" ]] || die "hecate fetch $ref returned $response: $(cat /tmp/hecate-fetch 2>/dev/null)"
    val=$(jq -r '.value' /tmp/hecate-fetch)
    [[ -n "$val" && "$val" != "null" ]] || die "hecate fetch $ref returned empty value"
    export "$env_var=$val"
    log "fetched $ref → $env_var"
  done < <(printf '%s' "$ZAKROS_HECATE_FETCHES" | jq -c '.[]')
fi

# Parse envelope
REPO_URL=$(jq -r '.execution.repo_url' "$ZAKROS_ENVELOPE")
BRANCH=$(jq -r '.execution.branch' "$ZAKROS_ENVELOPE")
BASE_BRANCH=$(jq -r '.execution.base_branch' "$ZAKROS_ENVELOPE")
BRIEF_SUMMARY=$(jq -r '.brief.summary' "$ZAKROS_ENVELOPE")
BRIEF_DETAIL=$(jq -r '.brief.detail // ""' "$ZAKROS_ENVELOPE")
PROJECT_ID=$(jq -r '.project_id' "$ZAKROS_ENVELOPE")

# Mint a GitHub App installation access token from the broker. The pod
# JWT (MCP_AUTH_TOKEN) carries audience=github + scope=clone; the
# broker mints a token scoped to the requested repo and returns it.
# Replaces Phase 1's long-lived PAT injection.
REPO_FULL_NAME=$(printf '%s' "$REPO_URL" | sed -E 's#^https?://[^/]+/##' | sed -E 's#\.git$##')
[[ -n "$REPO_FULL_NAME" ]] || die "could not derive owner/name from repo_url=$REPO_URL"
log "minting github installation token for $REPO_FULL_NAME"
token_payload=$(jq -n --arg repo "$REPO_FULL_NAME" '{repo: $repo}')
token_response=$(curl -sS -o /tmp/github-token-response -w '%{http_code}' \
  -X POST \
  -H "Authorization: Bearer $MCP_AUTH_TOKEN" \
  -H "Content-Type: application/json" \
  --data "$token_payload" \
  "$ZAKROS_GITHUB_BROKER_URL/github/installation-token" \
  || echo "000")
[[ "$token_response" == "200" ]] || die "github broker mint returned $token_response: $(cat /tmp/github-token-response 2>/dev/null)"
GITHUB_TOKEN=$(jq -r '.token' /tmp/github-token-response)
[[ -n "$GITHUB_TOKEN" && "$GITHUB_TOKEN" != "null" ]] || die "github broker returned empty token"
export GITHUB_TOKEN
log "github installation token minted (expires $(jq -r '.expires_at' /tmp/github-token-response))"

log "task $ZAKROS_TASK_ID run $ZAKROS_RUN_ID project=$PROJECT_ID"
log "repo=$REPO_URL branch=$BRANCH base=$BASE_BRANCH"
post_status "status" "Starting task: $BRIEF_SUMMARY"

# Configure git
git config --global user.email "daedalus-agent@$PROJECT_ID.zakros.local"
git config --global user.name  "Daedalus Agent"
# Embed the token in the URL for the clone/push. GH deprecates username/password
# for clone, so use x-access-token:TOKEN (documented GitHub pattern).
AUTHED_URL=$(printf '%s' "$REPO_URL" | sed -E "s#^https://#https://x-access-token:${GITHUB_TOKEN}@#")

# Clone and checkout
mkdir -p "$WORKSPACE"
cd "$WORKSPACE"
log "cloning repo"
post_status "status" "Cloning $REPO_URL ($BASE_BRANCH)"
git clone --branch "$BASE_BRANCH" --depth 50 "$AUTHED_URL" repo
cd repo
git checkout -B "$BRANCH"

# Build claude-code prompt from brief
PROMPT_FILE=$(mktemp)
{
  printf '%s\n\n' "$BRIEF_SUMMARY"
  if [[ -n "$BRIEF_DETAIL" ]]; then
    printf '%s\n' "$BRIEF_DETAIL"
  fi
} > "$PROMPT_FILE"

log "invoking claude"
post_status "status" "Running claude"
# Non-interactive: --print emits the final response; --permission-mode acceptEdits
# lets claude apply filesystem edits without a human in the loop.
set +e
claude --print --permission-mode acceptEdits < "$PROMPT_FILE"
CC_STATUS=$?
set -e
LAST_OUTCOME="claude-exit-$CC_STATUS"
[[ $CC_STATUS -eq 0 ]] || die "claude exited $CC_STATUS"

# Commit any changes
if ! git diff --quiet || ! git diff --cached --quiet; then
  log "committing changes"
  post_status "status" "Committing changes"
  git add -A
  git commit -m "daedalus: $BRIEF_SUMMARY"
else
  log "claude-code produced no changes"
  post_status "status" "claude-code produced no changes"
fi

# Push the feature branch
log "pushing branch $BRANCH"
post_status "status" "Pushing branch $BRANCH"
git push -u origin "$BRANCH"

# Open PR
log "opening pull request"
post_status "status" "Opening pull request"
PR_URL=$(gh pr create \
  --base "$BASE_BRANCH" \
  --head "$BRANCH" \
  --title "$BRIEF_SUMMARY" \
  --body "Opened by Daedalus task $ZAKROS_TASK_ID (run $ZAKROS_RUN_ID)." \
  2>&1 | tail -1)
log "PR: $PR_URL"
RUN_SUMMARY="Opened $PR_URL for branch $BRANCH"

# Report PR to Minos so the webhook handler can bind it back to this task.
# Requires ZAKROS_MINOS_URL and MCP_AUTH_TOKEN to be in the pod env.
if [[ -n "$ZAKROS_MINOS_URL" && -n "${MCP_AUTH_TOKEN:-}" ]]; then
  log "reporting PR url to Minos"
  report_body=$(jq -n --arg pr "$PR_URL" '{pr_url: $pr}')
  report_status=$(curl -sS -o /tmp/pr-report-response -w '%{http_code}' \
    -X POST \
    -H "Authorization: Bearer $MCP_AUTH_TOKEN" \
    -H "Content-Type: application/json" \
    --data "$report_body" \
    "$ZAKROS_MINOS_URL/tasks/$ZAKROS_TASK_ID/pr" \
    || echo "000")
  if [[ "$report_status" != "200" ]]; then
    log "WARN: PR report to Minos returned $report_status: $(cat /tmp/pr-report-response 2>/dev/null)"
  fi
else
  log "skipping PR report (ZAKROS_MINOS_URL or MCP_AUTH_TOKEN unset)"
fi

flush_memory "completed" "$PR_URL"

# Ship the sanitized run record to Mnemosyne via Minos. Minos sanitizes
# again server-side against the pod's injected credentials; this client
# side just packages what the entrypoint knows.
if [[ -n "$ZAKROS_MINOS_URL" && -n "${MCP_AUTH_TOKEN:-}" && -f "$ZAKROS_MEMORY_DIR/run-record.json" ]]; then
  log "posting memory blob to Mnemosyne"
  memory_payload=$(jq -n \
    --arg outcome "completed" \
    --arg summary "${RUN_SUMMARY:-}" \
    --argjson body "$(cat "$ZAKROS_MEMORY_DIR/run-record.json")" \
    '{outcome: $outcome, summary: $summary, body: $body}')
  memory_status=$(curl -sS -o /tmp/memory-response -w '%{http_code}' \
    -X POST \
    -H "Authorization: Bearer $MCP_AUTH_TOKEN" \
    -H "Content-Type: application/json" \
    --data "$memory_payload" \
    "$ZAKROS_MINOS_URL/tasks/$ZAKROS_TASK_ID/memory" \
    || echo "000")
  if [[ "$memory_status" != "200" ]]; then
    log "WARN: memory POST returned $memory_status: $(cat /tmp/memory-response 2>/dev/null)"
  fi
fi

log "done"
