#!/usr/bin/env bash
# find-zelvinator-mentions.sh.bash — Find issues/PRs mentioning @zelvinator
# in body, title, or comments, OR assigned to zelvinator.
# Deduplicated, atomically claimed.
#
# Reads config from config.sh (same directory) for whitelist users and env path.
# Credentials sourced from ~/.hermes/.env (GITHUB_TOKEN).
#
# Usage: ./find-zelvinator-mentions.sh [--reset]

set -uo pipefail

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

# ── Lock helpers ──
acquire_lock() {
  local t=5 w=0
  while ! mkdir "$LOCK_FILE" 2>/dev/null; do
    sleep 0.2
    w=$((w + 1))
    if [ "$w" -ge 25 ]; then  # 25 * 0.2 = 5 seconds
      rmdir "$LOCK_FILE" 2>/dev/null || true
      mkdir "$LOCK_FILE" 2>/dev/null && break || return 1
    fi
  done
  return 0
}
release_lock() { rmdir "$LOCK_FILE" 2>/dev/null || true; }

claim() {
  local k="$1"
  acquire_lock || return 1
  if grep -qxF "$k" "$TRACKER_FILE" 2>/dev/null; then
    release_lock
    return 1
  fi
  echo "$k" >> "$TRACKER_FILE"
  release_lock
  return 0
}

# ── Result accumulator ──
RESULTS_FILE=$(mktemp)
echo '[]' > "$RESULTS_FILE"
trap 'rm -f "$RESULTS_FILE" "$LOCK_FILE"' EXIT

add_result() {
  local j="$1"
  local c
  c=$(cat "$RESULTS_FILE") || return
  echo "$c" | jq --argjson i "$j" '. + [$i]' > "$RESULTS_FILE" 2>/dev/null || true
}

# ── Helper: extract repo name from search result item ──
get_repo() {
  local item="$1"
  # Try nameWithOwner first (gh search output), then full_name
  local r
  r=$(echo "$item" | jq -r '.repository.nameWithOwner // .repository.full_name // ""' 2>/dev/null)
  if [ -z "$r" ]; then
    # Try parsing from repository_url
    r=$(echo "$item" | jq -r '.repository_url // ""' 2>/dev/null | sed 's|https://api.github.com/repos/||')
  fi
  echo "$r"
}

# ── Process search results ──
process_search_items() {
  local json="$1" source="$2"
  local items
  items=$(echo "$json" | jq -c '.[]' 2>/dev/null) || return
  [ -z "$items" ] && return

  while IFS= read -r item; do
    [ -z "$item" ] && continue

    # For comment-sourced items, verify a known human wrote the @zelvinator comment
    if [ "$source" = "comment" ]; then
      local num repo
      num=$(echo "$item" | jq -r '.number // 0' 2>/dev/null) || continue
      repo=$(get_repo "$item") || continue
      [ -z "$repo" ] && continue
      local human_found=false
      local trigger_comment=""
      local raw_comments
      raw_comments=$(gh api "/repos/$repo/issues/$num/comments?per_page=20" --jq '.[] | {user: {login: .user.login}, body: .body}' 2>/dev/null || echo "")
      [ -z "$raw_comments" ] && continue
      while IFS= read -r comment_json; do
        [ -z "$comment_json" ] && continue
        local c_author c_body whitelisted=false
        c_author=$(echo "$comment_json" | jq -r '.user.login' 2>/dev/null) || continue
        c_body=$(echo "$comment_json" | jq -r '.body' 2>/dev/null | head -c 500) || continue
        for wl_user in "${WHITELIST_USERS[@]}"; do
          [ "$c_author" = "$wl_user" ] && whitelisted=true && break
        done
        if [ "$whitelisted" = true ] && echo "$c_body" | grep -qi '@zelvinator'; then
          human_found=true
          trigger_comment="$c_body"
          break
        fi
      done <<< "$raw_comments"
      [ "$human_found" = false ] && continue
    fi

    local num repo title url author body branch type
    trigger_comment="${trigger_comment:-}"
    num=$(echo "$item" | jq -r '.number // 0' 2>/dev/null) || continue
    repo=$(get_repo "$item") || continue
    [ -z "$repo" ] && continue
    title=$(echo "$item" | jq -r '.title // ""' 2>/dev/null) || continue
    url=$(echo "$item" | jq -r '.html_url // .url // ""' 2>/dev/null) || continue
    author=$(echo "$item" | jq -r '.user.login // .author.login // ""' 2>/dev/null) || continue
    type=$(echo "$item" | jq -r 'if .pull_request then "pr" else "issue" end // "issue"' 2>/dev/null) || continue

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
        '{type: $type, repo: $repo, number: $num, title: $title, url: $url, body_preview: $body, branch: $branch, author: $author, trigger_source: $source, trigger_comment: $trigger_comment}' 2>/dev/null) || continue
      add_result "$json_result"
    else
      body=$(gh issue view "$num" --repo "$repo" --json body --jq '.body' 2>/dev/null | head -c 1500 || echo "")
      local json_result
      json_result=$(jq -n \
        --arg type "issue" --arg repo "$repo" --argjson num "$num" \
        --arg title "$title" --arg url "$url" \
        --arg body "$body" --arg source "$source" \
        --arg trigger_comment "$trigger_comment" \
        '{type: $type, repo: $repo, number: $num, title: $title, url: $url, body_preview: $body, trigger_source: $source, trigger_comment: $trigger_comment}' 2>/dev/null) || continue
      add_result "$json_result"
    fi
  done <<< "$items"
}

# ── 1) Issues: @zelvinator in title/body ──
BODY_ISSUES=$(gh search issues "@zelvinator" --state open \
  --json number,title,url,repository,isPullRequest --limit 50 2>/dev/null || echo "[]")
BODY_ISSUES=$(echo "$BODY_ISSUES" | jq '[.[] | select(.isPullRequest == false)]' 2>/dev/null || echo "[]")
process_search_items "$BODY_ISSUES" "body"

# ── 2) Issues: @zelvinator in comments ──
COMMENT_ISSUES=$(gh api "search/issues?q=@zelvinator+in:comments+is:issue+state:open&per_page=100&sort=created&order=desc" 2>/dev/null || echo '{"items":[]}')
COMMENT_ISSUES=$(echo "$COMMENT_ISSUES" | jq -c '.items' 2>/dev/null || echo "[]")
process_search_items "$COMMENT_ISSUES" "comment"

# ── 3) PRs: @zelvinator in title/body ──
BODY_PRS=$(gh search prs "@zelvinator" --state open \
  --json number,title,url,repository,author --limit 50 2>/dev/null || echo "[]")
BODY_PRS=$(echo "$BODY_PRS" | jq '[.[] | . + {pull_request: true}]' 2>/dev/null || echo "[]")
process_search_items "$BODY_PRS" "body"

# ── 4) PRs: @zelvinator in comments ──
COMMENT_PRS=$(gh api "search/issues?q=@zelvinator+in:comments+type:pr+state:open&per_page=100&sort=created&order=desc" 2>/dev/null || echo '{"items":[]}')
COMMENT_PRS=$(echo "$COMMENT_PRS" | jq -c '.items' 2>/dev/null || echo "[]")
process_search_items "$COMMENT_PRS" "comment"

# ── 5) PR review comments: @zelvinator in inline code review discussions ──
REVIEW_PRS=$(gh search prs "state:open" --json number,title,url,repository,author --limit 30 2>/dev/null || echo '[]')
while IFS= read -r item; do
[ -z "$item" ] && continue
num=$(echo "$item" | jq -r '.number // 0' 2>/dev/null) || continue
repo=$(get_repo "$item") || continue
[ -z "$repo" ] || [ "$num" = "0" ] && continue

review_comments=$(gh api "/repos/$repo/pulls/$num/comments?per_page=50" --jq '.[] | {id: .id, user: {login: .user.login}, body: .body}' 2>/dev/null || echo "")
[ -z "$review_comments" ] && continue

human_found=false
trigger_comment=""
comment_id=0
while IFS= read -r rc; do
  [ -z "$rc" ] && continue
  c_author=$(echo "$rc" | jq -r '.user.login' 2>/dev/null) || continue
  c_body=$(echo "$rc" | jq -r '.body' 2>/dev/null | head -c 500) || continue
  c_id=$(echo "$rc" | jq -r '.id // 0' 2>/dev/null) || continue
  whitelisted=false
  for wl_user in "${WHITELIST_USERS[@]}"; do
    [ "$c_author" = "$wl_user" ] && whitelisted=true && break
  done
  if [ "$whitelisted" = true ] && echo "$c_body" | grep -qi '@zelvinator'; then
    human_found=true
    trigger_comment="$c_body"
    comment_id="$c_id"
  fi
done <<< "$review_comments"
[ "$human_found" = false ] && continue

claim "pr:${repo}#${num}:comment:${comment_id}" || continue

title=$(echo "$item" | jq -r '.title // ""' 2>/dev/null) || continue
url=$(echo "$item" | jq -r '.html_url // .url // ""' 2>/dev/null) || continue
author=$(echo "$item" | jq -r '.author.login // ""' 2>/dev/null) || continue
branch=$(gh pr view "$num" --repo "$repo" --json headRefName --jq '.headRefName' 2>/dev/null || echo "")
body=$(gh pr view "$num" --repo "$repo" --json body --jq '.body' 2>/dev/null | head -c 1500 || echo "")
json_result=$(jq -n \
  --arg type "pr" --arg repo "$repo" --argjson num "$num" \
  --arg title "$title" --arg url "$url" \
  --arg body "$body" --arg branch "$branch" \
  --arg author "$author" --arg source "review_comment" \
  --arg trigger_comment "$trigger_comment" \
  --argjson review_comment_id "$comment_id" \
  '{type: $type, repo: $repo, number: $num, title: $title, url: $url, body_preview: $body, branch: $branch, author: $author, trigger_source: $source, trigger_comment: $trigger_comment, review_comment_id: $review_comment_id}' 2>/dev/null) || continue
add_result "$json_result"
done < <(echo "$REVIEW_PRS" | jq -c '.[]' 2>/dev/null)

# ── 6) CI failures: zelvinator's PRs with failing checks ──
CI_ATTEMPTS="${SCRIPT_DIR}/.zelvinator-ci-attempts.txt"
touch "$CI_ATTEMPTS"

ci_attempts_left() {
  local key="$1"
  local count
  count=$(grep -c "^$key:" "$CI_ATTEMPTS" 2>/dev/null || echo 0)
  [ "$count" -lt 3 ]
}

ci_mark_attempt() {
  local key="$1"
  echo "$key:$(date +%s)" >> "$CI_ATTEMPTS"
}

# Use /issues API to find PRs by zelvinator (search API doesn't work for the authenticated user)
ZELV_PRS=$(gh api "/issues?filter=created&state=open&per_page=100" --jq '[.[] | select(.pull_request != null) | {number: .number, title: .title, html_url: .html_url, repository: {nameWithOwner: .repository.full_name}, headRefName: "", headRefOid: ""}]' 2>/dev/null || echo "[]")
while IFS= read -r item; do
    [ -z "$item" ] && continue
    num=$(echo "$item" | jq -r '.number' 2>/dev/null) || continue
    repo=$(echo "$item" | jq -r '.repository.nameWithOwner // ""' 2>/dev/null) || continue
    [ -z "$repo" ] && continue

    # Get the actual head SHA for CI check
    sha=$(gh api "repos/$repo/pulls/$num" --jq '.head.sha' 2>/dev/null) || continue
    key="ci:$repo#$num"

    ci_attempts_left "$key" || continue

    # Check check runs for failures
    failed=$(gh api "/repos/$repo/commits/$sha/check-runs" --jq '[.check_runs[] | select(.conclusion == "failure" or .conclusion == "action_required" or .conclusion == "cancelled" or .conclusion == "timed_out") | {name: .name, conclusion: .conclusion, url: .details_url}]' 2>/dev/null || echo "[]")
    fail_count=$(echo "$failed" | jq 'length' 2>/dev/null || echo 0)
    [ "$fail_count" -eq 0 ] && continue

    # Also check commit statuses
    status_failed=$(gh api "/repos/$repo/commits/$sha/status" --jq '[.statuses[] | select(.state == "failure" or .state == "error") | {context: .context, state: .state}]' 2>/dev/null || echo "[]")

    title=$(echo "$item" | jq -r '.title // ""' 2>/dev/null) || continue
    url=$(echo "$item" | jq -r '.html_url // ""' 2>/dev/null) || continue
    branch=$(gh pr view "$num" --repo "$repo" --json headRefName --jq '.headRefName' 2>/dev/null || echo "")
    body=$(gh pr view "$num" --repo "$repo" --json body --jq '.body' 2>/dev/null | head -c 500 || echo "")

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
      '{type: $type, repo: $repo, number: $num, title: $title, url: $url, body_preview: $body, branch: $branch, author: $author, trigger_source: $source, trigger_comment: $trigger_comment, failed_checks: $failed, failed_statuses: $status_failed}' 2>/dev/null) || continue

    claim "pr:${repo}#${num}" || continue
    add_result "$json_result"
    ci_mark_attempt "$key"
  done < <(echo "$ZELV_PRS" | jq -c '.[]' 2>/dev/null)

# ── 7) Issues assigned to zelvinator ──
# Use /issues API instead of search API (search doesn't work for the authenticated user)
ASSIGNED=$(gh api "/issues?filter=assigned&state=open&per_page=100" --jq '[.[] | select(.pull_request == null) | {number: .number, title: .title, html_url: .html_url, body: .body, repository: {nameWithOwner: .repository.full_name}, repository_url: .repository_url, url: .url, user: .user, assignees: .assignees}]' 2>/dev/null || echo "[]")
while IFS= read -r item; do
    [ -z "$item" ] && continue

    # Verify assignment to zelvinator
    local assignee_match=false
    assignees=$(echo "$item" | jq -c '.assignees // []' 2>/dev/null)
    while IFS= read -r a; do
      [ -z "$a" ] && continue
      login=$(echo "$a" | jq -r '.login' 2>/dev/null) || continue
      [ "$login" = "zelvinator" ] && assignee_match=true && break
    done <<< "$(echo "$assignees" | jq -c '.[]' 2>/dev/null)"
    [ "$assignee_match" = false ] && continue

    # Skip if body/title already contains @zelvinator (handled by step 1)
    local body title
    body=$(echo "$item" | jq -r '.body // ""' 2>/dev/null) || continue
    title=$(echo "$item" | jq -r '.title // ""' 2>/dev/null) || continue
    echo "$body$title" | grep -qi '@zelvinator' && continue

    local num repo
    num=$(echo "$item" | jq -r '.number // 0' 2>/dev/null) || continue
    repo=$(echo "$item" | jq -r '.repository.nameWithOwner // (.repository_url | capture("https://api.github.com/repos/(?<r>.+)").r) // ""' 2>/dev/null) || continue
    [ -z "$repo" ] || [ "$num" = "0" ] && continue

    claim "assigned:issue:${repo}#${num}" || continue

    local html_url
    html_url=$(echo "$item" | jq -r '.html_url // ""' 2>/dev/null) || continue
    body=$(echo "$body" | head -c 1500)
    local json_result
    json_result=$(jq -n \
      --arg type "issue" \
      --arg repo "$repo" \
      --argjson num "$num" \
      --arg title "$title" \
      --arg url "$html_url" \
      --arg body "$body" \
      --arg source "assignment" \
      --arg trigger_comment "" \
      '{type: $type, repo: $repo, number: $num, title: $title, url: $url, body_preview: $body, trigger_source: $source, trigger_comment: $trigger_comment}' 2>/dev/null) || continue
    add_result "$json_result"
  done <<< "$(echo "$ASSIGNED" | jq -c '.[]' 2>/dev/null)"

cat "$RESULTS_FILE" | jq '.'
