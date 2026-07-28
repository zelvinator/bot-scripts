# Zelvinator Bot Scripts

Automation scripts for the [zelvinator](https://github.com/zelvinator) GitHub bot, driven by Hermes Agent cron jobs.

## Overview

The bot watches repositories across configured GitHub orgs for `@zelvinator` mentions and responds automatically using a **two-model architecture**:

- **Qwen 3.6 35B** (worker) — discovery, triage, simple fixes, implementation, file-level review
- **GLM 5.2** (planner) — architectural planning, complex review, takeover fixes

### Architecture

```
┌─────────────────────────────────────────────────────────────┐
│  zelvinator-worker (Qwen 3.6, every 5 min)                  │
│                                                             │
│  Phase 1: Discovery + Triage                                │
│  ├── Run Go binary → discover items → SQLite: discovered    │
│  ├── Post 🐢 acknowledgment comment                         │
│  ├── Simple items (comments, ≤2 file fixes) → handle direct │
│  └── Complex items → state: needs_planning                  │
│                                                             │
│  Phase 2: Implementation (pick up GLM's plans)              │
│  ├── Queue items in "planned" or "fix_needed" state         │
│  ├── Read plan, implement file-by-file, push, open PR       │
│  └── state: review_pending                                  │
│                                                             │
│  Phase 3: Review Triage                                     │
│  ├── Review diffs at file level                             │
│  ├── Clean → done | Simple fix → fix_needed                 │
│  └── Complex → needs_review (escalate to GLM)              │
│                                                             │
│  Phase 4: Stale reset (items stuck >20 min)                 │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│  zelvinator-planner (GLM 5.2, every 15 min)                 │
│                                                             │
│  Phase 1: Planning (items Qwen escalated)                   │
│  ├── Analyze codebase, write structured plan JSON           │
│  └── state: planned                                         │
│                                                             │
│  Phase 2: Complex Review (items Qwen escalated)             │
│  ├── Architectural review of diffs                          │
│  └── done | fix_needed | needs_planning (re-plan)          │
│                                                             │
│  Phase 3: Takeover (Qwen failed twice)                      │
│  └── GLM implements the fix directly → done                 │
└─────────────────────────────────────────────────────────────┘
```

### State Machine

Items flow through a SQLite-backed state machine at `~/.hermes/zelvinator-bot/state.db`:

```
discovered → needs_planning → planned → implementing → review_pending → done
                                ↑                      ↓                ↑
                                │                  fix_needed ──────────┘
                                │                      ↓
                                │             needs_review → done
                                │                      ↓
                                └──────── fix_needed (Qwen retry)
                                                          ↓
                                               GLM takeover → done
```

Key improvement over the old flat-file tracker: **claim-on-complete, not claim-on-discover**. Items enter `discovered` state but only move to `done` after work finishes. If a cron session dies mid-implementation, the item stays in `implementing` and gets reset to `planned` for retry on the next cycle.

## Scripts

### `scripts/find-zelvinator-mentions.sh`

Discovers new @zelvinator mentions across all configured repos. Inserts items into the SQLite state database. Only newly discovered items are returned.

**Usage:**
```bash
./scripts/find-zelvinator-mentions.sh
```

### Zelvinator CLI (`scripts/zelvinator/`)

The Go binary provides all state management and GitHub actions:

```bash
# Discovery
zelvinator find                          # Discover new items, insert into SQLite

# State queries
zelvinator queue --state=discovered      # Items to triage
zelvinator queue --state=planned         # Items ready for implementation
zelvinator queue --state=fix_needed      # Items needing fixes
zelvinator queue --state=review_pending  # Items awaiting review
zelvinator queue --state=needs_planning  # Items Qwen escalated to GLM
zelvinator queue --state=needs_review    # Reviews Qwen escalated to GLM

# State transitions
zelvinator state <id> <new_state> [--plan=<file>] [--feedback=<text>] [--pr-url=<url>] [--error=<text>]

# Plan management
zelvinator plan <id>                     # Get plan JSON for an item

# Maintenance
zelvinator stale --reset                 # Reset items stuck in "implementing" >20 min
zelvinator stats                         # Show item counts per state
zelvinator reset --confirm               # Reset entire state database

# GitHub actions
zelvinator comment <repo> <number> <body>
zelvinator review <repo> <number> <body> [event]
zelvinator reply-review <repo> <number> <review_comment_id> <body>
```

### Rebuilding the Go binary

```bash
cd scripts/zelvinator
go build -o zelvinator .
```

## Configuration

Edit `config.sh`:

| Variable | Purpose |
|----------|---------|
| `WHITELIST_USERS` | Users whose @zelvinator mentions trigger bot actions |
| `TARGET_ORGS` | GitHub orgs/accounts to search |
| `HERMES_ENV` | Path to Hermes .env file (normally `~/.hermes/.env`) |

## Credentials

The bot reads `GITHUB_TOKEN` from `~/.hermes/.env`. No secrets stored in this repo.

## Cron Integration

Two Hermes cron jobs:

| Job | Model | Schedule | Role |
|-----|-------|----------|------|
| `zelvinator-worker` | Qwen 3.6 35B | every 5 min | Discovery, triage, implementation, file-level review |
| `zelvinator-planner` | GLM 5.2 | every 15 min | Planning, complex review, takeover fixes |

Cron prompts are at:
- `references/cron-prompt-worker.md` — Qwen worker prompt
- `references/cron-prompt-planner.md` — GLM planner prompt

## State Database

SQLite database at `~/.hermes/zelvinator-bot/state.db` — **outside the git working directory**, immune to `git stash`/`checkout`/`reset`.

Schema:
```sql
CREATE TABLE items (
    id              TEXT PRIMARY KEY,   -- "issue:owner/repo#123"
    repo            TEXT NOT NULL,
    number          INTEGER NOT NULL,
    type            TEXT NOT NULL,      -- "issue" | "pr"
    trigger_source  TEXT NOT NULL,
    state           TEXT NOT NULL,      -- discovered → ... → done
    plan            TEXT,               -- JSON plan from GLM
    review_feedback TEXT,               -- GLM's review notes
    pr_url          TEXT,
    attempts        INTEGER DEFAULT 0,
    ...
);
```
