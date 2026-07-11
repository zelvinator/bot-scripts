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
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zelvinator/bot-scripts/scripts/zelvinator/internal/config"
	"github.com/zelvinator/bot-scripts/scripts/zelvinator/internal/github"
	"github.com/zelvinator/bot-scripts/scripts/zelvinator/internal/tracker"
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

// ── Item structure for output ──

// OutputItem represents an unprocessed item for the handler.
type OutputItem struct {
	Type           string              `json:"type"`
	Repo           string              `json:"repo"`
	Number         int                 `json:"number"`
	Title          string              `json:"title"`
	URL            string              `json:"url"`
	BodyPreview    string              `json:"body_preview"`
	Branch         string              `json:"branch,omitempty"`
	Author         string              `json:"author,omitempty"`
	TriggerSource  string              `json:"trigger_source"`
	TriggerComment string              `json:"trigger_comment"`
	ReviewCommentID int                `json:"review_comment_id,omitempty"`
	CommentID      int                 `json:"-"` // used for claim key (unique per comment)
	FailedChecks   []github.CheckRun   `json:"failed_checks,omitempty"`
	FailedStatuses []github.StatusItem `json:"failed_statuses,omitempty"`
}

// Helper for filepath join (shadowed by import).
var joinPath = filepath.Join

// ── Find Command ──

func runFind(client *github.Client, cfg *config.Config, args []string) {
	// Handle --reset
	for _, a := range args {
		if a == "--reset" {
			t, err := tracker.NewTracker(cfg.ScriptDir, ".zelvinator-processed.txt")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Tracker error: %v\n", err)
				os.Exit(1)
			}
			if err := t.Reset(); err != nil {
				fmt.Fprintf(os.Stderr, "Reset error: %v\n", err)
				os.Exit(1)
			}
			// Also reset CI attempts
			ciTracker, err := tracker.NewTracker(joinPath(cfg.ScriptDir, "scripts"), ".zelvinator-ci-attempts.txt")
			if err == nil {
				ciTracker.Reset()
			}
			fmt.Println("Tracker reset.")
			return
		}
	}

	t, err := tracker.NewTracker(cfg.ScriptDir, ".zelvinator-processed.txt")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Tracker error: %v\n", err)
		os.Exit(1)
	}

	// CI attempts tracker uses a separate file to count attempts
	var ciTracker *tracker.Tracker
	ciTracker, _ = tracker.NewTracker(cfg.ScriptDir, ".zelvinator-ci-attempts.txt")

	var items = make([]OutputItem, 0)

	// 1) Issues: @zelvinator in title/body
	for _, org := range cfg.TargetOrgs {
		results, err := client.SearchIssues(org)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Search issues (org=%s): %v\n", org, err)
			continue
		}
		for _, r := range results {
			// Verify the result actually contains @zelvinator (guard against search API false positives)
			if !strings.Contains(r.Body, "@zelvinator") && !strings.Contains(r.Title, "@zelvinator") {
				continue
			}
			items = append(items, makeIssueItem(r, "body", ""))
		}
	}

	// 2) Issues: @zelvinator in comments
	for _, org := range cfg.TargetOrgs {
		results, err := client.SearchIssueComments(org)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Search issue comments (org=%s): %v\n", org, err)
			continue
		}
		for _, r := range results {
			triggerComment, commentID := findHumanTriggerComment(client, r, cfg.WhitelistUsers)
			if triggerComment == "" {
				continue
			}
			item := makeIssueItem(r, "comment", triggerComment)
			item.CommentID = commentID
			items = append(items, item)
		}
	}

	// 3) PRs: @zelvinator in title/body
	for _, org := range cfg.TargetOrgs {
		results, err := client.SearchPRs(org)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Search PRs (org=%s): %v\n", org, err)
			continue
		}
		for _, r := range results {
			// Verify the result actually contains @zelvinator (guard against search API false positives)
			if !strings.Contains(r.Body, "@zelvinator") && !strings.Contains(r.Title, "@zelvinator") {
				continue
			}
			items = append(items, makePRItem(r, client, "body", ""))
		}
	}

	// 4) PRs: @zelvinator in comments
	for _, org := range cfg.TargetOrgs {
		results, err := client.SearchPRComments(org)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Search PR comments (org=%s): %v\n", org, err)
			continue
		}
		for _, r := range results {
			triggerComment, commentID := findHumanTriggerComment(client, r, cfg.WhitelistUsers)
			if triggerComment == "" {
				continue
			}
			item := makePRItem(r, client, "comment", triggerComment)
			item.CommentID = commentID
			items = append(items, item)
		}
	}

	// 5) PR review comments: @zelvinator in inline code review discussions
	// Check review comments on all open PRs across target orgs. This catches
	// @zelvinator mentions in inline code reviews that the in:comments search misses.
	reviewPRSet := make(map[string]int) // "repo#number" -> number

	// Collect all open PRs per org (up to 30 most recently updated per org)
	for _, org := range cfg.TargetOrgs {
		openPRs, err := client.SearchOpenPRs(org)
		if err == nil {
			for _, r := range openPRs {
				repo := r.RepoName()
				if repo != "" {
					reviewPRSet[fmt.Sprintf("%s#%d", repo, r.Number)] = r.Number
				}
			}
		}
	}

	// Build whitelist set for review comments
	wlSet := make(map[string]bool)
	for _, u := range cfg.WhitelistUsers {
		wlSet[u] = true
	}

	for key := range reviewPRSet {
		parts := strings.SplitN(key, "#", 2)
		if len(parts) != 2 {
			continue
		}
		repo := parts[0]
		num, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}

		// Fetch PR review comments
		reviewComments, err := client.GetPRReviewComments(repo, num)
		if err != nil {
			continue
		}

		// Find the most recent @zelvinator review comment from a whitelisted user
		var triggerComment string
		var commentID int
		for _, rc := range reviewComments {
			if wlSet[rc.User.Login] && strings.Contains(strings.ToLower(rc.Body), "@zelvinator") {
				triggerComment = rc.Body
				commentID = rc.ID
			}
		}
		if triggerComment == "" {
			continue
		}

		// Fetch PR details for the output item
		prInfo, err := client.GetPR(repo, num)
		if err != nil {
			continue
		}

		// Get PR title from a lightweight search result. Since we already have the PR
		// in reviewPRSet (from SearchPRs or SearchOpenPRs), we can use it, but we need the title.
		// Fetch the issue to get title and author.
		type prIssue struct {
			Title string    `json:"title"`
			User  github.User `json:"user"`
		}
		var issue prIssue
		issueURL := fmt.Sprintf("https://api.github.com/repos/%s/issues/%d", repo, num)
		if err := client.GetJSON(issueURL, &issue); err != nil {
			// Skip if we can't get the title — unlikely but possible
			continue
		}

		body := prInfo.Body
		if len(body) > 1500 {
			body = body[:1500]
		}

		htmlURL := fmt.Sprintf("https://github.com/%s/pull/%d", repo, num)

		items = append(items, OutputItem{
			Type:           "pr",
			Repo:           repo,
			Number:         num,
			Title:          issue.Title,
			URL:            htmlURL,
			BodyPreview:    body,
			Branch:         prInfo.Head.Ref,
			Author:         issue.User.Login,
			TriggerSource:  "review_comment",
			TriggerComment: triggerComment,
			ReviewCommentID: commentID,
			CommentID:      commentID,
		})
	}

	// 6) CI failures: zelvinator's PRs with failing checks
	for _, org := range cfg.TargetOrgs {
		results, err := client.SearchAuthorPRs(org, "zelvinator")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Search zelvinator PRs (org=%s): %v\n", org, err)
			continue
		}
		for _, r := range results {
			repo := r.RepoName()
			if repo == "" {
				continue
			}

			// Resolve SHA and ref
			prInfo, err := client.GetPR(repo, r.Number)
			if err != nil {
				continue
			}
			sha := prInfo.Head.SHA
			branch := prInfo.Head.Ref

			failedChecks, err := client.GetCheckRuns(repo, sha)
			if err != nil {
				continue
			}
			failedStatuses, err := client.GetStatuses(repo, sha)
			if err != nil {
				continue
			}
			if len(failedChecks) == 0 && len(failedStatuses) == 0 {
				continue
			}

			key := fmt.Sprintf("ci:%s#%d", repo, r.Number)
			// Check attempt limit using the CI attempts tracker
			if ciTracker != nil {
				claimed, _ := ciTracker.Claim(key)
				if !claimed {
					continue
				}
			}

			body, _ := client.GetIssueBody(repo, r.Number)
			if len(body) > 1500 {
				body = body[:1500]
			}

			htmlURL := fmt.Sprintf("https://github.com/%s/pull/%d", repo, r.Number)

			items = append(items, OutputItem{
				Type:           "pr",
				Repo:           repo,
				Number:         r.Number,
				Title:          r.Title,
				URL:            htmlURL,
				BodyPreview:    body,
				Branch:         branch,
				Author:         "zelvinator",
				TriggerSource:  "ci_failure",
				TriggerComment: "",
				FailedChecks:   failedChecks,
				FailedStatuses: failedStatuses,
			})
		}
	}

	// 7) Issues assigned to zelvinator
	for _, org := range cfg.TargetOrgs {
		results, err := client.SearchAssignedIssues(org, "zelvinator")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Search assigned issues (org=%s): %v\n", org, err)
			continue
		}
		for _, r := range results {
			// Safety: double-check the issue is actually assigned to zelvinator
			// (search API returns issues where assignee matches — but we verify)
			assigneeMatch := false
			if r.Assignees != nil {
				for _, a := range r.Assignees {
					if a.Login == "zelvinator" {
						assigneeMatch = true
						break
					}
				}
			}
			if !assigneeMatch {
				continue
			}
			// Skip issues that also contain @zelvinator in body/title —
			// those will be picked up by step 1 and we don't want duplicates.
			// But since we use a different key prefix, we let them both through
			// and dedup later. Actually, better to check: if the body or title
			// already has @zelvinator, skip this assignment path since the
			// @mention path handled it already.
			if strings.Contains(r.Body, "@zelvinator") || strings.Contains(r.Title, "@zelvinator") {
				continue
			}
			items = append(items, makeIssueItem(r, "assignment", ""))
		}
	}

	// Deduplicate and claim
	output := make([]OutputItem, 0)
	seen := make(map[string]bool)
	for _, item := range items {
		key := fmt.Sprintf("%s:%s#%d", item.Type, item.Repo, item.Number)
		if item.CommentID != 0 {
			key = fmt.Sprintf("%s:%s#%d:comment:%d", item.Type, item.Repo, item.Number, item.CommentID)
		}
		// Assignment-triggered items get a separate key prefix so they don't
		// conflict with @mention-triggered items for the same issue.
		if item.TriggerSource == "assignment" {
			key = "assigned:" + key
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		claimed, err := t.Claim(key)
		if err != nil || !claimed {
			continue
		}
		output = append(output, item)
	}

	// Output JSON
	data, _ := json.MarshalIndent(output, "", "  ")
	fmt.Println(string(data))
}

func makeIssueItem(r github.SearchResult, source, triggerComment string) OutputItem {
	repo := r.RepoName()
	htmlURL := r.HTMLURL
	if htmlURL == "" {
		htmlURL = fmt.Sprintf("https://github.com/%s/issues/%d", repo, r.Number)
	}
	body := r.Body
	if len(body) > 1500 {
		body = body[:1500]
	}
	return OutputItem{
		Type:           "issue",
		Repo:           repo,
		Number:         r.Number,
		Title:          r.Title,
		URL:            htmlURL,
		BodyPreview:    body,
		TriggerSource:  source,
		TriggerComment: triggerComment,
	}
}

func makePRItem(r github.SearchResult, client *github.Client, source, triggerComment string) OutputItem {
	repo := r.RepoName()

	htmlURL := r.HTMLURL
	if htmlURL == "" {
		htmlURL = fmt.Sprintf("https://github.com/%s/pull/%d", repo, r.Number)
	}

	// Get branch and body
	var branch string
	var body string
	prInfo, err := client.GetPR(repo, r.Number)
	if err == nil {
		branch = prInfo.Head.Ref
		body = prInfo.Body
	}
	if body == "" {
		body = r.Body
	}
	if len(body) > 1500 {
		body = body[:1500]
	}

	return OutputItem{
		Type:           "pr",
		Repo:           repo,
		Number:         r.Number,
		Title:          r.Title,
		URL:            htmlURL,
		BodyPreview:    body,
		Branch:         branch,
		Author:         r.User.Login,
		TriggerSource:  source,
		TriggerComment: triggerComment,
	}
}

func findHumanTriggerComment(client *github.Client, item github.SearchResult, whitelist []string) (string, int) {
	repo := item.RepoName()
	if repo == "" {
		return "", 0
	}
	comments, err := client.GetIssueComments(repo, item.Number)
	if err != nil {
		return "", 0
	}

	// Build whitelist set
	wl := make(map[string]bool)
	for _, u := range whitelist {
		wl[u] = true
	}

	// Find the most recent @zelvinator comment from a whitelisted user
	var trigger string
	var commentID int
	for _, c := range comments {
		if wl[c.User.Login] && strings.Contains(strings.ToLower(c.Body), "@zelvinator") {
			trigger = c.Body
			commentID = c.ID
		}
	}
	return trigger, commentID
}

// ── Comment Command ──

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

// ── Review Command ──

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

// ── Reply to Review Comment Command ──

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

// ── CI Fix Command ──

func runCIFix(client *github.Client, cfg *config.Config, args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: zelvinator ci-fix <repo> <number>\n")
		os.Exit(1)
	}
	repo := args[0]
	number, err := strconv.Atoi(args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid number: %s\n", args[1])
		os.Exit(1)
	}

	// Get PR info
	prInfo, err := client.GetPR(repo, number)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching PR: %v\n", err)
		os.Exit(1)
	}

	sha := prInfo.Head.SHA
	branch := prInfo.Head.Ref

	// Get failed checks
	failedChecks, _ := client.GetCheckRuns(repo, sha)
	failedStatuses, _ := client.GetStatuses(repo, sha)

	if len(failedChecks) == 0 && len(failedStatuses) == 0 {
		fmt.Printf("No CI failures detected on %s#%d\n", repo, number)
		return
	}

	fmt.Printf("CI failures on %s#%d (branch: %s, sha: %s)\n", repo, number, branch, sha)

	// Collect failure details for the comment
	var summary strings.Builder
	summary.WriteString("## CI Failure Diagnosis\n\n")
	summary.WriteString(fmt.Sprintf("Branch: `%s` | SHA: `%s`\n\n", branch, sha[:8]))

	if len(failedChecks) > 0 {
		summary.WriteString("### Failed Checks\n\n")
		summary.WriteString("| Check | Conclusion |\n")
		summary.WriteString("|-------|-----------|\n")
		for _, cr := range failedChecks {
			summary.WriteString(fmt.Sprintf("| %s | %s |\n", cr.Name, cr.Conclusion))
		}
		summary.WriteString("\n")
	}

	if len(failedStatuses) > 0 {
		summary.WriteString("### Failed Statuses\n\n")
		summary.WriteString("| Status | State |\n")
		summary.WriteString("|--------|-------|\n")
		for _, s := range failedStatuses {
			summary.WriteString(fmt.Sprintf("| %s | %s |\n", s.Context, s.State))
		}
		summary.WriteString("\n")
	}

	// Clone the repo
	cloneDir := fmt.Sprintf("/tmp/zelvinator-ci-%s-%d", strings.ReplaceAll(repo, "/", "-"), number)
	os.RemoveAll(cloneDir)

	token := os.Getenv("GITHUB_TOKEN")
	cloneURL := fmt.Sprintf("https://x-access-token:%s@github.com/%s.git", token, repo)

	fmt.Printf("Cloning %s (branch: %s)...\n", repo, branch)
	if out, err := runCmd("git", "clone", "--depth=50", "--branch", branch, cloneURL, cloneDir); err != nil {
		comment := fmt.Sprintf("%s\n**Clone failed:** `%v`\n```\n%s\n```\n", summary.String(), err, out)
		client.CreateComment(repo, number, comment)
		fmt.Printf("Clone error: %v\n%s\n", err, out)
		return
	}

	// Try to build/test
	summary.WriteString("\n### Local Diagnosis\n\n")

	// Check for Makefile
	if _, err := os.Stat(joinPath(cloneDir, "Makefile")); err == nil {
		summary.WriteString("**`make`**: ")
		if out, err := runCmd("make", "-C", cloneDir); err != nil {
			summary.WriteString(fmt.Sprintf("❌ Failed\n```\n%s\n```\n", out))
		} else {
			summary.WriteString("✅ Passed\n")
		}
	}

	// Check for go.mod
	if _, err := os.Stat(joinPath(cloneDir, "go.mod")); err == nil {
		summary.WriteString("**`go build ./...`**: ")
		if out, err := runCmd("go", "build", "./..."); err != nil {
			summary.WriteString(fmt.Sprintf("❌ Failed\n```\n%s\n```\n", out))
		} else {
			summary.WriteString("✅ Passed\n")
		}
		summary.WriteString("**`go test ./...`**: ")
		if out, err := runCmd("go", "test", "./..."); err != nil {
			summary.WriteString(fmt.Sprintf("❌ Failed\n```\n%s\n```\n", out))
		} else {
			summary.WriteString("✅ Passed\n")
		}
	}

	// Post a comment on the PR
	client.CreateComment(repo, number, summary.String())
	fmt.Printf("CI diagnosis comment posted on %s#%d\n", repo, number)
}

// runCmd executes a command, returns combined stdout+stderr.
func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
