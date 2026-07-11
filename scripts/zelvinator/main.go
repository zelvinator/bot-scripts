// zelvinator — CLI tool for the zelvinator GitHub bot.
//
// Subcommands:
//
//	find         Find new @zelvinator mentions (replaces the bash script)
//	comment      Post a comment on an issue or PR
//	review       Post a review on a PR
//	ci-fix       Diagnose and fix CI failures on a zelvinator PR
package main

import (
	"fmt"
	"os"

	"github.com/zelvinator/bot-scripts/scripts/zelvinator/internal/config"
	"github.com/zelvinator/bot-scripts/scripts/zelvinator/internal/github"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: zelvinator <command> [args...]\n")
		fmt.Fprintf(os.Stderr, "Commands:\n")
		fmt.Fprintf(os.Stderr, "  find           Find new @zelvinator mentions\n")
		fmt.Fprintf(os.Stderr, "  find --reset   Reset the processed-items tracker\n")
		fmt.Fprintf(os.Stderr, "  comment <repo> <number> <body>\n")
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
	case "find":
		runFind(client, cfg, os.Args[2:])
	case "comment":
		runComment(client, os.Args[2:])
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
