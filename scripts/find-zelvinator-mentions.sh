#!/usr/bin/env bash
# find-zelvinator-mentions.sh — Find issues/PRs mentioning @zelvinator
# in body, title, or comments. Deduplicated, atomically claimed.
#
# Reads config from config.sh (same directory) for whitelist and target orgs.
# Credentials sourced from ~/.hermes/.env (GITHUB_TOKEN).
#
# Usage: ./find-zelvinator-mentions.sh [--reset]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Load config — whitelist users, target orgs, env path
CONFIG="${SCRIPT_DIR}/../config.sh"
if [ -f "$CONFIG" ]; then
  source "$CONFIG"
else
  echo "Error: config.sh not found at $CONFIG" >&2
  exit 1
fi

TRACKER_FILE="${SCRIPT_DIR}/.zelvinator-processed.txt"
LOCK_FILE="${TRACKER_FILE}.lock"

# Source credentials
if [ -f "$HERMES_ENV" ]; then
  set -a
  source "$HERMES_ENV"
  set +a
fi
TOKEN="${GITHUB_TOKEN:-}"
[ -z "$TOKEN" ] && { echo '[]'; exit 0; }
export GH_TOKEN="$TOKEN"

[ "${1:-}" = "--reset" ] && { rm -f "$TRACKER_FILE"; echo "Tracker reset."; exit 0; }
touch "$TRACKER_FILE"

acquire_lock() {
  local t=5 w=0
  while ! mkdir "$LOCK_FILE" 2>/dev/null; do
    sleep 0.2; w=$(echo "$w+0.2"|bc 2>/dev/null||echo 1)
    [ "$(echo "$w>$t"|bc 2>/dev/null||echo 1)" = 1 ] && { rmdir "$LOCK_FILE" 2>/dev/null||true; mkdir "$LOCK_FILE" 2>/dev/null&&break; return 1; }
  done; return 0
}
release_lock() { rmdir "$LOCK_FILE" 2>/dev/null||true; }

claim() {
  local k="$1"; acquire_lock||return 1
  if grep -qxF "$k" "$TRACKER_FILE" 2>/dev/null; then release_lock; return 1; fi
  echo "$k" >> "$TRACKER_FILE"; release_lock; return 0
}

RESULTS_FILE=$(mktemp); echo '[]' > "$RESULTS_FILE"
trap 'rm -f "$RESULTS_FILE" "$LOCK_FILE"' EXIT
add_result() { local j="$1"; local c; c=$(cat "$RESULTS_FILE"); echo "$c" | jq --argjson i "$j" '. + [$i]' > "$RESULTS_FILE"; }

# Build a case pattern from the whitelist for use in shell case statements
join_by() { local d="$1"; shift; echo "$(printf "$d%s" "$@")"; }
WHITELIST_PATTERN=$(join_by "|" "${WHITELIST_USERS[@]}")

# ── Process search results ──
process_search_items() {
  local json="$1" source="$2"
  local items; items=$(echo "$json" | jq -c '.[]' 2>/dev/null)
  [ -z "$items" ] && return

  while IFS= read -r item; do
    [ -z "$item" ] && continue

    # For comment-sourced items, verify a known human wrote the @zelvinator comment
    if [ "$source" = "comment" ]; then
      local num repo
      num=$(echo "$item" | jq -r '.number // 0')
      repo=$(echo "$item" | jq -r '.repository.nameWithOwner // .repository.full_name // (.repository_url | capture("https://api.github.com/repos/(?<r>.+)").r // "")' 2>/dev/null)
      [ -z "$repo" ] && continue

      local human_found=false
      trigger_comment=""
      while IFS= read -r comment_json; do
        [ -z "$comment_json" ] && continue
        local c_author c_body
        c_author=$(echo "$comment_json" | jq -r '.user.login')
        c_body=$(echo "$comment_json" | jq -r '.body' | head -c 500)
        case "$c_author" in
          $WHITELIST_PATTERN)
            if echo "$c_body" | grep -qi '@zelvinator'; then
              human_found=true
              trigger_comment="$c_body"
              break
            fi
            ;;
        esac
      done < <(gh api "/repos/$repo/issues/$num/comments?per_page=20" --jq '.[] | {user: {login: .user.login}, body: .body}' 2>/dev/null)

      if [ "$human_found" = false ]; then continue; fi
    fi

    local num repo title url author body branch type
    [ -z "${trigger_comment:-}" ] && trigger_comment=""
    num=$(echo "$item" | jq -r '.number // 0')
    repo=$(echo "$item" | jq -r '.repository.nameWithOwner // .repository.full_name // (.repository_url | capture("https://api.github.com/repos/(?<r>.+)").r // "")' 2>/dev/null)
    title=$(echo "$item" | jq -r '.title // ""')
    url=$(echo "$item" | jq -r '.html_url // .url // ""')
    author=$(echo "$item" | jq -r '.user.login // .author.login // ""')
    type=$(echo "$item" | jq -r 'if .pull_request then "pr" else "issue" end // "issue"')
    [ -z "$repo" ] && continue
    claim "${type}:${repo}#${num}" || continue

    if [ "$type" = "pr" ]; then
      branch=$(gh pr view "$num" --repo "$repo" --json headRefName --jq '.headRefName' 2>/dev/null || echo "")
      body=$(gh pr view "$num" --repo "$repo" --json body --jq '.body' 2>/dev/null | head -c 1500 || echo "")
      local json_result
      json_result=$(jq -n \
        --arg type "pr" --arg repo "$repo" --argjson num "$num" \
        --arg title "$title" --arg url "$url" \
        --arg body "$body" --arg branch "$branch" \
        --arg author "$author" --arg source "$source" \
        --arg trigger_comment "$trigger_comment" \
        '{type: $type, repo: $repo, number: $num, title: $title, url: $url, body_preview: $body, branch: $branch, author: $author, trigger_source: $source, trigger_comment: $trigger_comment}')
      add_result "$json_result"
    else
      body=$(gh issue view "$num" --repo "$repo" --json body --jq '.body' 2>/dev/null | head -c 1500 || echo "")
      local json_result
      json_result=$(jq -n \
        --arg type "issue" --arg repo "$repo" --argjson num "$num" \
        --arg title "$title" --arg url "$url" \
        --arg body "$body" --arg source "$source" \
        --arg trigger_comment "$trigger_comment" \
        '{type: $type, repo: $repo, number: $num, title: $title, url: $url, body_preview: $body, trigger_source: $source, trigger_comment: $trigger_comment}')
      add_result "$json_result"
    fi
  done <<< "$items"
}

# ── 1) Issues: @zelvinator in title/body ──
BODY_ISSUES=$(gh search issues "@zelvinator" --state open \
  --json number,title,url,repository,isPullRequest --limit 50 2>/dev/null || echo "[]")
BODY_ISSUES=$(echo "$BODY_ISSUES" | jq '[.[] | select(.isPullRequest == false)]' 2>/dev/null || echo "[]")
process_search_items "$BODY_ISSUES" "body"

# ── 2) Issues: @zelvinator in comments (per-org) ──
COMMENT_ISSUES='[]'
for org in "${TARGET_ORGS[@]}"; do
  ORG_RESULT=$(gh api "search/issues?q=@zelvinator+in:comments+is:issue+org:$org+state:open&per_page=100&sort=created&order=desc" 2>/dev/null || echo '{"items":[]}')
  ORG_ITEMS=$(echo "$ORG_RESULT" | jq -c '.items' 2>/dev/null || echo "[]")
  COMMENT_ISSUES=$(echo "$COMMENT_ISSUES" "$ORG_ITEMS" | jq -s 'add' 2>/dev/null || echo "[]")
done
process_search_items "$COMMENT_ISSUES" "comment"

# ── 3) PRs: @zelvinator in title/body ──
BODY_PRS=$(gh search prs "@zelvinator" --state open \
  --json number,title,url,repository,author --limit 50 2>/dev/null || echo "[]")
BODY_PRS=$(echo "$BODY_PRS" | jq '[.[] | . + {pull_request: true}]' 2>/dev/null || echo "[]")
process_search_items "$BODY_PRS" "body"

# ── 4) PRs: @zelvinator in comments (per-org) ──
COMMENT_PRS='[]'
for org in "${TARGET_ORGS[@]}"; do
  ORG_RESULT=$(gh api "search/issues?q=@zelvinator+in:comments+type:pr+org:$org+state:open&per_page=100&sort=created&order=desc" 2>/dev/null || echo '{"items":[]}')
  ORG_ITEMS=$(echo "$ORG_RESULT" | jq -c '.items' 2>/dev/null || echo "[]")
  COMMENT_PRS=$(echo "$COMMENT_PRS" "$ORG_ITEMS" | jq -s 'add' 2>/dev/null || echo "[]")
done
process_search_items "$COMMENT_PRS" "comment"

# ── 5) CI failures: zelvinator's PRs with failing checks ──
# Uses a separate attempts tracker (max 3 tries per PR)
CI_ATTEMPTS="${SCRIPT_DIR}/.zelvinator-ci-attempts.txt"
touch "$CI_ATTEMPTS"

ci_attempts_left() {
  local key="$1"
  local count
  count=$(grep -c "^$key:" "$CI_ATTEMPTS" 2>/dev/null || echo 0)
  [ "$count" -lt 3 ] && return 0 || return 1
}

ci_mark_attempt() {
  local key="$1"
  echo "$key:$(date +%s)" >> "$CI_ATTEMPTS"
}

for org in "${TARGET_ORGS[@]}"; do
  ZELV_PRS=$(gh search prs "author:zelvinator is:pr state:open org:$org" \
    --json number,title,url,repository,headRefName,headRefOid,updatedAt --limit 30 2>/dev/null || echo "[]")
  while IFS= read -r item; do
    [ -z "$item" ] && continue
    local num repo sha key
    num=$(echo "$item" | jq -r '.number')
    repo=$(echo "$item" | jq -r '.repository.nameWithOwner')
    sha=$(echo "$item" | jq -r '.headRefOid')
    key="ci:$repo#$num"
    
    # Check attempt limit
    ci_attempts_left "$key" || continue
    
    # Check check runs for failures
    local failed
    failed=$(gh api "/repos/$repo/commits/$sha/check-runs" --jq '[.check_runs[] | select(.conclusion == "failure" or .conclusion == "action_required" or .conclusion == "cancelled" or .conclusion == "timed_out") | {name: .name, conclusion: .conclusion, url: .details_url}]' 2>/dev/null || echo "[]")
    local fail_count
    fail_count=$(echo "$failed" | jq 'length' 2>/dev/null || echo 0)
    [ "$fail_count" -eq 0 ] && continue
    
    # Also check commit statuses
    local status_failed
    status_failed=$(gh api "/repos/$repo/commits/$sha/status" --jq '[.statuses[] | select(.state == "failure" or .state == "error") | {context: .context, state: .state}]' 2>/dev/null || echo "[]")
    
    # Output as a CI failure item
    local title body branch author
    title=$(echo "$item" | jq -r '.title')
    url=$(echo "$item" | jq -r '.url')
    branch=$(echo "$item" | jq -r '.headRefName')
    body=$(gh pr view "$num" --repo "$repo" --json body --jq '.body' 2>/dev/null | head -c 500 || echo "")
    
    local json_result
    json_result=$(jq -n \
      --arg type "pr" \
      --arg repo "$repo" \
      --argjson num "$num" \
      --arg title "$title" \
      --arg url "$url" \
      --arg body "$body" \
      --arg branch "$branch" \
      --arg author "zelvinator" \
      --arg source "ci_failure" \
      --argjson failed "$failed" \
      --argjson status_failed "$status_failed" \
      --arg trigger_comment "" \
      '{type: $type, repo: $repo, number: $num, title: $title, url: $url, body_preview: $body, branch: $branch, author: $author, trigger_source: $source, trigger_comment: $trigger_comment, failed_checks: $failed, failed_statuses: $status_failed}')
    
    # Claim in the main tracker
    claim "pr:${repo}#${num}" || continue
    add_result "$json_result"
    ci_mark_attempt "$key"
  done < <(echo "$ZELV_PRS" | jq -c '.[]' 2>/dev/null)
done

cat "$RESULTS_FILE" | jq '.'
