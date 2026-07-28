// zelvinator — CLI tool for the zelvinator GitHub bot.
//
// Subcommands:
//
//	find            Find new @zelvinator mentions (inserts into SQLite)
//	find --reset    Info about resetting state
//	queue           Query items by state (--state=planned)
//	state           Transition item state (with optional --plan, --feedback, --pr-url, --error)
//	plan            Get plan for an item
//	stale           Report/reset items stuck in "implementing"
//	stats           Show item counts per state
//	reset           Reset the state database (requires --confirm)
//	comment         Post a comment on an issue or PR
//	review          Post a review on a PR
//	reply-review    Post an inline reply to a PR review comment
//	ci-fix          Diagnose and fix CI failures on a zelvinator PR
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zelvinator/bot-scripts/scripts/zelvinator/internal/config"
	"github.com/zelvinator/bot-scripts/scripts/zelvinator/internal/github"
	"github.com/zelvinator/bot-scripts/scripts/zelvinator/internal/state"
)

// dbPath returns the SQLite database path.
func dbPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".hermes", "zelvinator-bot", "state.db")
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: zelvinator <command> [args...]\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  find           Find new @zelvinator mentions\n")
		fmt.Fprintf(os.Stderr, "  queue          Query items by state (--state=planned)\n")
		fmt.Fprintf(os.Stderr, "  state          Transition item state\n")
		fmt.Fprintf(os.Stderr, "  plan           Get plan for an item\n")
		fmt.Fprintf(os.Stderr, "  stale          Report/reset stale implementing items\n")
		fmt.Fprintf(os.Stderr, "  stats          Show item counts per state\n")
		fmt.Fprintf(os.Stderr, "  reset          Reset state database (--confirm)\n")
		fmt.Fprintf(os.Stderr, "  comment <repo> <number> <body>\n")
		fmt.Fprintf(os.Stderr, "  help <repo> <number>\n")
		fmt.Fprintf(os.Stderr, "  review <repo> <number> <body> [event]\n")
		fmt.Fprintf(os.Stderr, "  reply-review <repo> <number> <review_comment_id> <body>\n")
		fmt.Fprintf(os.Stderr, "  ci-fix <repo> <number>\n")
		os.Exit(1)
	}

	// Load config and token
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
		os.Exit(1)
	}

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token, err = cfg.LoadEnv()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Token error: %v\n", err)
			os.Exit(1)
		}
		os.Setenv("GITHUB_TOKEN", token)
	}

	client := github.NewClient(token)

	cmd := os.Args[1]
	switch cmd {
	// State management commands — need DB
	case "find":
		db, err := state.Open(dbPath())
		if err != nil {
			fmt.Fprintf(os.Stderr, "DB error: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()
		runFind(client, cfg, db, os.Args[2:])
	case "queue":
		db, err := state.Open(dbPath())
		if err != nil {
			fmt.Fprintf(os.Stderr, "DB error: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()
		runQueue(db, os.Args[2:])
	case "state":
		db, err := state.Open(dbPath())
		if err != nil {
			fmt.Fprintf(os.Stderr, "DB error: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()
		runStateTransition(db, os.Args[2:])
	case "plan":
		db, err := state.Open(dbPath())
		if err != nil {
			fmt.Fprintf(os.Stderr, "DB error: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()
		runGetPlan(db, os.Args[2:])
	case "stale":
		db, err := state.Open(dbPath())
		if err != nil {
			fmt.Fprintf(os.Stderr, "DB error: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()
		runStale(db, os.Args[2:])
	case "stats":
		db, err := state.Open(dbPath())
		if err != nil {
			fmt.Fprintf(os.Stderr, "DB error: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()
		runStats(db)
	case "reset":
		db, err := state.Open(dbPath())
		if err != nil {
			fmt.Fprintf(os.Stderr, "DB error: %v\n", err)
			os.Exit(1)
		}
		runResetDB(db, os.Args[2:])

	// GitHub action commands — no DB needed
	case "comment":
		runComment(client, os.Args[2:])
	case "help":
		runHelp(client, os.Args[2:])
	case "review":
		runReview(client, os.Args[2:])
	case "reply-review":
		runReplyReview(client, os.Args[2:])
	case "ci-fix":
		runCIFix(client, cfg, os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		os.Exit(1)
	}
}
