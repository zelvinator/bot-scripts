// Package main — comment, review, and reply-review commands.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/zelvinator/bot-scripts/scripts/zelvinator/internal/github"
)

// runComment posts a comment on an issue or PR.
func runComment(client *github.Client, args []string) {
	if len(args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: zelvinator comment <repo> <number> <body>\n")
		os.Exit(1)
	}
	repo := args[0]
	number, err := strconv.Atoi(args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid number: %s\n", args[1])
		os.Exit(1)
	}
	body := strings.Join(args[2:], " ")

	if err := client.CreateComment(repo, number, body); err != nil {
		fmt.Fprintf(os.Stderr, "Comment error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Comment posted on %s#%d\n", repo, number)
}

// runReview posts a review on a PR.
func runReview(client *github.Client, args []string) {
	if len(args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: zelvinator review <repo> <number> <body> [event]\n")
		fmt.Fprintf(os.Stderr, "  event: APPROVE | REQUEST_CHANGES | COMMENT (default: COMMENT)\n")
		os.Exit(1)
	}
	repo := args[0]
	number, err := strconv.Atoi(args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid number: %s\n", args[1])
		os.Exit(1)
	}
	body := args[2]
	event := "COMMENT"
	if len(args) >= 4 {
		event = args[3]
	}

	if err := client.CreateReview(repo, number, body, event); err != nil {
		fmt.Fprintf(os.Stderr, "Review error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Review posted on %s#%d (event=%s)\n", repo, number, event)
}

// runReplyReview posts an inline reply to a specific PR review comment.
func runReplyReview(client *github.Client, args []string) {
	if len(args) < 4 {
		fmt.Fprintf(os.Stderr, "Usage: zelvinator reply-review <repo> <number> <review_comment_id> <body>\n")
		fmt.Fprintf(os.Stderr, "  Posts an inline reply to a specific PR review comment (threaded under it).\n")
		os.Exit(1)
	}
	repo := args[0]
	number, err := strconv.Atoi(args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid number: %s\n", args[1])
		os.Exit(1)
	}
	reviewCommentID, err := strconv.Atoi(args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid review_comment_id: %s\n", args[2])
		os.Exit(1)
	}
	body := strings.Join(args[3:], " ")

	if err := client.ReplyToReviewComment(repo, number, reviewCommentID, body); err != nil {
		fmt.Fprintf(os.Stderr, "Reply error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Inline reply posted on %s#%d (review comment %d)\n", repo, number, reviewCommentID)
}
