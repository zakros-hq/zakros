#!/usr/bin/env bash
# Daedalus Claude Code worker entrypoint.
#
# Contract (per agents/plugin/plugin.go and architecture.md §8):
#   - Envelope JSON mounted at $DAEDALUS_ENVELOPE (default /var/run/daedalus/envelope.json)
#   - GITHUB_TOKEN, ANTHROPIC_API_KEY set via injected_credentials
#   - Workspace: /workspace (EmptyDir, cloned into)
#   - Memory extraction: /var/run/daedalus/memory/run-record.json on SIGTERM or exit
#   - Exit 0 on success (PR opened), non-zero on failure
#
# Phase 1 Slice A scope: clone, checkout, run claude-code, commit, push,
# open PR. No thread sidecar calls yet (Slice B). No memory extraction
# integration yet (Slice C reads /var/run/daedalus/memory/).

set -euo pipefail

: "${DAEDALUS_ENVELOPE:=/var/run/daedalus/envelope.json}"
: "${DAEDALUS_MEMORY_DIR:=/var/run/daedalus/memory}"
: "${WORKSPACE:=/workspace}"

log()  { printf '[%s] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
die()  { log "ERROR: $*"; flush_memory "failed" "$*"; exit 1; }

# flush_memory writes a minimal run record for Mnemosyne (Slice C consumes).
flush_memory() {
  local outcome="${1:-unknown}"
  local message="${2:-}"
  mkdir -p "$DAEDALUS_MEMORY_DIR"
  jq -n \
    --arg task_id    "${DAEDALUS_TASK_ID:-}" \
    --arg run_id     "${DAEDALUS_RUN_ID:-}" \
    --arg project_id "${DAEDALUS_PROJECT_ID:-}" \
    --arg outcome    "$outcome" \
    --arg message    "$message" \
    --arg summary    "${RUN_SUMMARY:-}" \
    '{task_id: $task_id, run_id: $run_id, project_id: $project_id, outcome: $outcome, message: $message, summary: $summary}' \
    > "$DAEDALUS_MEMORY_DIR/run-record.json"
  log "memory blob written: $DAEDALUS_MEMORY_DIR/run-record.json ($outcome)"
}

on_sigterm() {
  log "received SIGTERM; flushing memory"
  flush_memory "${LAST_OUTCOME:-terminated}" "SIGTERM received"
  exit 143
}
trap on_sigterm TERM INT

# Preconditions
[[ -f "$DAEDALUS_ENVELOPE" ]] || die "envelope not found at $DAEDALUS_ENVELOPE"
command -v jq  >/dev/null || die "jq not installed in image"
command -v git >/dev/null || die "git not installed in image"
command -v gh  >/dev/null || die "gh CLI not installed in image"
command -v claude-code >/dev/null || die "claude-code not installed in image"
[[ -n "${GITHUB_TOKEN:-}" ]] || die "GITHUB_TOKEN not injected"

# Parse envelope
REPO_URL=$(jq -r '.execution.repo_url' "$DAEDALUS_ENVELOPE")
BRANCH=$(jq -r '.execution.branch' "$DAEDALUS_ENVELOPE")
BASE_BRANCH=$(jq -r '.execution.base_branch' "$DAEDALUS_ENVELOPE")
BRIEF_SUMMARY=$(jq -r '.brief.summary' "$DAEDALUS_ENVELOPE")
BRIEF_DETAIL=$(jq -r '.brief.detail // ""' "$DAEDALUS_ENVELOPE")
PROJECT_ID=$(jq -r '.project_id' "$DAEDALUS_ENVELOPE")

log "task $DAEDALUS_TASK_ID run $DAEDALUS_RUN_ID project=$PROJECT_ID"
log "repo=$REPO_URL branch=$BRANCH base=$BASE_BRANCH"

# Configure git
git config --global user.email "daedalus-agent@$PROJECT_ID.daedalus.local"
git config --global user.name  "Daedalus Agent"
# Embed the token in the URL for the clone/push. GH deprecates username/password
# for clone, so use x-access-token:TOKEN (documented GitHub pattern).
AUTHED_URL=$(printf '%s' "$REPO_URL" | sed -E "s#^https://#https://x-access-token:${GITHUB_TOKEN}@#")

# Clone and checkout
mkdir -p "$WORKSPACE"
cd "$WORKSPACE"
log "cloning repo"
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

log "invoking claude-code"
# Non-interactive: --print emits the final response; --permission-mode acceptEdits
# lets claude-code apply filesystem edits without a human in the loop.
set +e
claude-code --print --permission-mode acceptEdits < "$PROMPT_FILE"
CC_STATUS=$?
set -e
LAST_OUTCOME="claude-code-exit-$CC_STATUS"
[[ $CC_STATUS -eq 0 ]] || die "claude-code exited $CC_STATUS"

# Commit any changes
if ! git diff --quiet || ! git diff --cached --quiet; then
  log "committing changes"
  git add -A
  git commit -m "daedalus: $BRIEF_SUMMARY"
else
  log "claude-code produced no changes"
fi

# Push the feature branch
log "pushing branch $BRANCH"
git push -u origin "$BRANCH"

# Open PR
log "opening pull request"
PR_URL=$(gh pr create \
  --base "$BASE_BRANCH" \
  --head "$BRANCH" \
  --title "$BRIEF_SUMMARY" \
  --body "Opened by Daedalus task $DAEDALUS_TASK_ID (run $DAEDALUS_RUN_ID)." \
  2>&1 | tail -1)
log "PR: $PR_URL"
RUN_SUMMARY="Opened $PR_URL for branch $BRANCH"

flush_memory "completed" "$PR_URL"
log "done"
