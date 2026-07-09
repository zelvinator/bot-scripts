# Zelvinator Bot Scripts

Automation scripts for the [zelvinator](https://github.com/zelvinator) GitHub bot, driven by Hermes Agent cron jobs.

## Overview

The bot watches repositories across configured GitHub orgs for `@zelvinator` mentions and responds automatically:

- **@zelvinator in an issue** → implements the requested feature and opens a PR
- **@zelvinator in a PR** → reviews the PR and posts feedback

## Scripts

### `scripts/find-zelvinator-mentions.sh`

Detects new @zelvinator mentions across all configured repos. Outputs a JSON array of unprocessed items (issues and PRs). Uses GitHub's search API with per-org queries to handle private repos.

**Usage:**
```bash
# Find new mentions
./scripts/find-zelvinator-mentions.sh

# Reset processed-items tracker
./scripts/find-zelvinator-mentions.sh --reset
```

**Output:** JSON array with items containing `type`, `repo`, `number`, `title`, `url`, `trigger_source` (`"body"` or `"comment"`), and `trigger_comment` (the actual comment text when triggered by a comment).

## Configuration

Edit `config.sh` to control:

| Variable | Purpose |
|----------|---------|
| `WHITELIST_USERS` | Users whose @zelvinator mentions trigger bot actions |
| `TARGET_ORGS` | GitHub orgs/accounts to search |
| `HERMES_ENV` | Path to Hermes .env file (normally `~/.hermes/.env`) |

## Credentials

The script reads `GITHUB_TOKEN` from `$HERMES_ENV` (`~/.hermes/.env` by default). No secrets are stored in this repo.

## Cron Integration

This repo is consumed by a Hermes cron job (`zelvinator-mentions`) that runs every 10 minutes. The job runs the detection script and processes any new mentions.
