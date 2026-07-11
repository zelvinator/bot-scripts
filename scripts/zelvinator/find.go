// Package main — find command: discover new @zelvinator mentions, assigned issues, and CI failures.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zelvinator/bot-scripts/scripts/zelvinator/internal/config"
	"github.com/zelvinator/bot-scripts/scripts/zelvinator/internal/github"
	"github.com/zelvinator/bot-scripts/scripts/zelvinator/internal/tracker"
)

// OutputItem represents an unprocessed item for the handler.
type OutputItem struct {
	Type            string              `json:"type"`
	Repo            string              `json:"repo"`
	Number          int                 `json:"number"`
	Title           string              `json:"title"`
	URL              string              `json:"url"`
	BodyPreview     string              `json:"body_preview"`
	Branch          string              `json:"branch,omitempty"`
	Author          string              `json:"author,omitempty"`
	TriggerSource   string              `json:"trigger_source"`
	TriggerComment  string              `json:"trigger_comment"`
	ReviewCommentID int                `json:"review_comment_id,omitempty"`
	CommentID       int                `json:"-"` // used for claim key (unique per comment)
	FailedChecks    []github.CheckRun   `json:"failed_checks,omitempty"`
	FailedStatuses  []github.StatusItem `json:"failed_statuses,omitempty"`
}

// joinPath is a shadow-free alias for filepath.Join.
var joinPath = filepath.Join

// runFind discovers unprocessed @zelvinator mentions, assigned issues, and CI failures.
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
	reviewPRSet := make(map[string]int)

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

		reviewComments, err := client.GetPRReviewComments(repo, num)
		if err != nil {
			continue
		}

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

		prInfo, err := client.GetPR(repo, num)
		if err != nil {
			continue
		}

		type prIssue struct {
			Title string      `json:"title"`
			User  github.User `json:"user"`
		}
		var issue prIssue
		issueURL := fmt.Sprintf("https://api.github.com/repos/%s/issues/%d", repo, num)
		if err := client.GetJSON(issueURL, &issue); err != nil {
			continue
		}

		body := prInfo.Body
		if len(body) > 1500 {
			body = body[:1500]
		}

		htmlURL := fmt.Sprintf("https://github.com/%s/pull/%d", repo, num)

		items = append(items, OutputItem{
			Type:            "pr",
			Repo:            repo,
			Number:          num,
			Title:           issue.Title,
			URL:             htmlURL,
			BodyPreview:     body,
			Branch:          prInfo.Head.Ref,
			Author:          issue.User.Login,
			TriggerSource:   "review_comment",
			TriggerComment:  triggerComment,
			ReviewCommentID: commentID,
			CommentID:       commentID,
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

	wl := make(map[string]bool)
	for _, u := range whitelist {
		wl[u] = true
	}

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
