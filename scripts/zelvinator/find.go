// Package main — find command: discover new @zelvinator mentions, assigned issues, and CI failures.
// Inserts discovered items into the SQLite state database. Does NOT claim items —
// the orchestrator/worker cron jobs handle state transitions.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/zelvinator/bot-scripts/scripts/zelvinator/internal/config"
	"github.com/zelvinator/bot-scripts/scripts/zelvinator/internal/github"
	"github.com/zelvinator/bot-scripts/scripts/zelvinator/internal/state"
)

// FindItem represents a discovered item for the find output.
type FindItem struct {
	ID             string `json:"id"`
	Type           string `json:"type"`
	Repo           string `json:"repo"`
	Number         int    `json:"number"`
	Title          string `json:"title"`
	URL            string `json:"url"`
	BodyPreview    string `json:"body_preview"`
	Branch         string `json:"branch,omitempty"`
	Author         string `json:"author,omitempty"`
	TriggerSource  string `json:"trigger_source"`
	TriggerComment string `json:"trigger_comment"`
	ReviewCommentID int   `json:"review_comment_id,omitempty"`
	IsNew          bool   `json:"is_new"`
}

// runFind discovers unprocessed @zelvinator mentions, assigned issues, and CI failures.
// Items are inserted into the SQLite state database. Only newly discovered items
// are returned in the JSON output.
func runFind(client *github.Client, cfg *config.Config, db *state.DB, args []string) {
	// Handle --reset
	for _, a := range args {
		if a == "--reset" {
			// Reset means: clear all non-terminal items back to discovered
			stats, _ := db.Stats()
			fmt.Fprintf(os.Stderr, "Pre-reset stats: %+v\n", stats)
			// For full reset, we close and recreate the DB
			fmt.Println("Use 'zelvinator reset --confirm' to reset the state database.")
			return
		}
	}

	var newItems = make([]FindItem, 0)

	// 1) Issues: @zelvinator in title/body
	for _, org := range cfg.TargetOrgs {
		results, err := client.SearchIssues(org)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Search issues (org=%s): %v\n", org, err)
			continue
		}
		for _, r := range results {
			if !strings.Contains(r.Body, "@zelvinator") && !strings.Contains(r.Title, "@zelvinator") {
				continue
			}
			item := makeIssueFindItem(r, "body", "")
			if inserted, _ := db.InsertIfNew(toStateItem(item)); inserted {
				item.IsNew = true
				newItems = append(newItems, item)
			}
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
			item := makeIssueFindItem(r, "comment", triggerComment)
			item.ReviewCommentID = commentID
			item.ID = state.MakeIDWithComment(item.Type, item.Repo, item.Number, commentID)
			if inserted, _ := db.InsertIfNew(toStateItem(item)); inserted {
				item.IsNew = true
				newItems = append(newItems, item)
			}
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
			if !strings.Contains(r.Body, "@zelvinator") && !strings.Contains(r.Title, "@zelvinator") {
				continue
			}
			item := makePRFindItem(r, client, "body", "")
			if inserted, _ := db.InsertIfNew(toStateItem(item)); inserted {
				item.IsNew = true
				newItems = append(newItems, item)
			}
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
			item := makePRFindItem(r, client, "comment", triggerComment)
			item.ReviewCommentID = commentID
			item.ID = state.MakeIDWithComment(item.Type, item.Repo, item.Number, commentID)
			if inserted, _ := db.InsertIfNew(toStateItem(item)); inserted {
				item.IsNew = true
				newItems = append(newItems, item)
			}
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
			if !strings.Contains(strings.ToLower(rc.Body), "@zelvinator") {
				continue
			}
			if wlSet[rc.User.Login] {
				triggerComment = rc.Body
				commentID = rc.ID
			} else if rc.User.Login == "zelvinator" {
				cmd, _ := state.ParseCommand(rc.Body)
				if cmd != "" && state.IsKnownCommand(cmd) {
					triggerComment = rc.Body
					commentID = rc.ID
				}
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

		item := FindItem{
			ID:              state.MakeIDWithComment("pr", repo, num, commentID),
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
		}

		if inserted, _ := db.InsertIfNew(toStateItem(item)); inserted {
			item.IsNew = true
			newItems = append(newItems, item)
		}
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

			body, _ := client.GetIssueBody(repo, r.Number)
			if len(body) > 1500 {
				body = body[:1500]
			}

			htmlURL := fmt.Sprintf("https://github.com/%s/pull/%d", repo, r.Number)

			item := FindItem{
				ID:            state.MakeCIID(repo, r.Number),
				Type:          "pr",
				Repo:          repo,
				Number:        r.Number,
				Title:         r.Title,
				URL:           htmlURL,
				BodyPreview:   body,
				Branch:        branch,
				Author:        "zelvinator",
				TriggerSource: "ci_failure",
			}

			if inserted, _ := db.InsertIfNew(toStateItem(item)); inserted {
				item.IsNew = true
				newItems = append(newItems, item)
			}
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
			item := makeIssueFindItem(r, "assignment", "")
			item.ID = state.MakeAssignmentID(item.Repo, item.Number)
			if inserted, _ := db.InsertIfNew(toStateItem(item)); inserted {
				item.IsNew = true
				newItems = append(newItems, item)
			}
		}
	}

	// Output only newly discovered items as JSON
	data, _ := json.MarshalIndent(newItems, "", "  ")
	fmt.Println(string(data))
}

func makeIssueFindItem(r github.SearchResult, source, triggerComment string) FindItem {
	repo := r.RepoName()
	htmlURL := r.HTMLURL
	if htmlURL == "" {
		htmlURL = fmt.Sprintf("https://github.com/%s/issues/%d", repo, r.Number)
	}
	body := r.Body
	if len(body) > 1500 {
		body = body[:1500]
	}
	return FindItem{
		ID:            state.MakeID("issue", repo, r.Number),
		Type:          "issue",
		Repo:          repo,
		Number:        r.Number,
		Title:         r.Title,
		URL:           htmlURL,
		BodyPreview:   body,
		TriggerSource: source,
		TriggerComment: triggerComment,
	}
}

func makePRFindItem(r github.SearchResult, client *github.Client, source, triggerComment string) FindItem {
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

	return FindItem{
		ID:            state.MakeID("pr", repo, r.Number),
		Type:          "pr",
		Repo:          repo,
		Number:        r.Number,
		Title:         r.Title,
		URL:           htmlURL,
		BodyPreview:   body,
		Branch:        branch,
		Author:        r.User.Login,
		TriggerSource: source,
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
		if !strings.Contains(strings.ToLower(c.Body), "@zelvinator") {
			continue
		}
		// Whitelisted humans can trigger anything.
		// zelvinator itself can only trigger slash commands (self-triggering for pipeline automation).
		if wl[c.User.Login] {
			trigger = c.Body
			commentID = c.ID
		} else if c.User.Login == "zelvinator" {
			cmd, _ := state.ParseCommand(c.Body)
			if cmd != "" && state.IsKnownCommand(cmd) {
				trigger = c.Body
				commentID = c.ID
			}
		}
	}
	return trigger, commentID
}

// toStateItem converts a FindItem to a state.Item for DB insertion.
func toStateItem(f FindItem) state.Item {
	cmd, _ := state.ParseCommand(f.TriggerComment)
	return state.Item{
		ID:             f.ID,
		Repo:           f.Repo,
		Number:         f.Number,
		Type:           f.Type,
		TriggerSource:  f.TriggerSource,
		TriggerComment: f.TriggerComment,
		Command:        cmd,
		Title:          f.Title,
		BodyPreview:    f.BodyPreview,
		Branch:         f.Branch,
		Author:         f.Author,
	}
}
