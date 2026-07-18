# Zelvinator Bot Scripts

Automation scripts for the [zelvinator](https://github.com/zelvinator) GitHub bot,
driven by [Hermes Agent](https://hermes-agent.nousresearch.com) cron jobs.

## Overview

The bot reacts to `@zelvinator` mentions and issue assignments across ANY repo
the bot account has access to. No TARGET_ORGS config needed — just invite the
`zelvinator` GitHub user as a collaborator to your repo/org, and the bot
automatically picks up interactions on the next cron cycle.

**Reacts to:**
- `@zelvinator` in issue/PR body or title → implements feature / reviews PR
- `@zelvinator` in issue/PR comments or review comments → replies specifically
- Issue assigned to `zelvinator` user → implements the feature
- Bot's own PR with failing CI → diagnoses and fixes (max 3 attempts)

## Model: People-reacting, not org-watching

The bot does NOT need to know which orgs or repos to watch. It searches ALL
repos its GitHub token has access to using:

| Detection step | Method |
|---|---|
| Issues/PRs mentioning @zelvinator | Search API (across all accessible repos) |
| Comments mentioning @zelvinator | Search API + comment verification (whitelisted users only) |
| PR review comments | Iterates open PRs, checks inline review comments |
| CI failures on bot's PRs | Issues API (`filter=created`) — bot's own PRs |
| Assigned issues | Issues API (`filter=assigned`) — issues assigned to bot |

## Prompt Injection Defense

User-supplied content (issue bodies, comments, titles) could contain prompt
injection attacks. Two-tier defense:

**Tier 1 — Content boundary markers (Go binary):** All user-controlled fields
are wrapped in `╔═══ USER-SUPPLIED CONTENT ═══╗` markers before reaching the
LLM, making the data/instruction boundary visually unambiguous. Structural
anomalies (zero-width Unicode chars, encoded payloads) trigger a
`content_warning` flag.

**Tier 2 — Subagent judge (cron prompt):** Flagged items are NOT processed
directly. Instead, a zero-tools subagent (no MCP, no terminal, no filesystem)
is spawned solely to classify the content as SAFE or INJECTION. Only SAFE
items proceed to processing.

## Scripts

### `scripts/find-zelvinator-mentions.sh`

Thin wrapper that delegates to the Go binary. Falls back to the Bash
implementation if the binary is missing.

**Usage:**
```bash
# Find new mentions (outputs JSON array of unprocessed items)
./scripts/find-zelvinator-mentions.sh

# Reset processed-items tracker
./scripts/find-zelvinator-mentions.sh --reset
```

**Output:** JSON array with items containing:
- `type` — `"issue"` or `"pr"`
- `repo` — `"owner/name"`
- `number`, `title`, `url`
- `trigger_source` — `"body"`, `"comment"`, `"assignment"`, `"ci_failure"`, or `"review_comment"`
- `trigger_comment` — the comment text that triggered (when applicable)
- `content_warning` — set to `"structural_anomaly"` if suspicious patterns detected

### `scripts/find-zelvinator-mentions.sh.bash`

Standalone Bash implementation of the detection logic. Used as fallback if the
Go binary is not compiled. Same behavior, all logic in pure Bash + `gh` CLI.

### `scripts/zelvinator/` (Go binary source)

The primary detection engine. Written in Go for performance and reliability.

| File | Purpose |
|---|---|
| `main.go` | Entry point, command dispatch |
| `find.go` | Discovery of mentions, assignments, CI failures |
| `comment.go` | Post comments, reviews, reply to review comments |
| `cifix.go` | Diagnose and fix CI failures on bot PRs |
| `internal/config/config.go` | Config loader (WHITELIST_USERS only) |
| `internal/github/client.go` | GitHub API client (go-github wrapper) |
| `internal/tracker/tracker.go` | Atomic claim tracker (deduplication) |

## Configuration

Edit `config.sh`:

```bash
# Users whose @zelvinator mentions trigger bot actions
WHITELIST_USERS=(Hnatekmar xbedna MichalPustka mroncka)

# Path to Hermes .env file
HERMES_ENV="${HERMES_HOME:-$HOME/.hermes}/.env"
```

**There is no TARGET_ORGS.** The bot searches all repos its token can see.
To add the bot to a new repo, invite the `zelvinator` GitHub user as a
collaborator (no code changes needed).

## Credentials

The bot needs a single `GITHUB_TOKEN` in `~/.hermes/.env`:
```
GITHUB_TOKEN=ghp_...
```

The token determines which repos the bot can see. Must have `repo` scope
(full control of private repos).

## Cron Setup

The bot runs as a Hermes cron job (`zelvinator-mentions`) every 10 minutes.
Set it up with:

```bash
cronjob action=create \
  name=zelvinator-mentions \
  schedule='*/10 * * * *' \
  deliver=local \
  enabled_toolsets='["terminal","file","web","search"]' \
  workdir=/root/workspace/zelvinator \
  prompt="..."
```

See [references/cron-prompt.md](references/cron-prompt.md) for
the full cron prompt text (including injection defense guardrails).

The cron job must run as the user who has:
- Access to `~/.hermes/.env` (contains GITHUB_TOKEN)
- The Go binary compiled at `~/.hermes/zelvinator-bot/scripts/zelvinator/zelvinator`
- `gh` CLI authenticated (optional, for fallback script)

## Rebuilding the Go binary

After pulling new source:
```bash
cd ~/.hermes/zelvinator-bot/scripts/zelvinator
go build -o zelvinator .
```

## Adding the bot to a new repo

1. Invite the `zelvinator` GitHub user as a collaborator to the repo
2. Ensure the bot's `GITHUB_TOKEN` has access (repo scope)
3. That's it — the bot picks it up on the next 10-minute cron cycle
