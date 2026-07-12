// Package github provides a GitHub API client for the zelvinator bot.
// It wraps google/go-github to provide a stable interface for the rest of the bot.
package github

import (
	"context"
	"fmt"
	"os"
	"strings"

	gh "github.com/google/go-github/v69/github"
)

// Client wraps the GitHub REST API via go-github.
type Client struct {
	token  string
	client *gh.Client
}

// NewClient creates a GitHub client with a token.
func NewClient(token string) *Client {
	return &Client{
		token:  token,
		client: gh.NewClient(nil).WithAuthToken(token),
	}
}

// NewClientFromEnv creates a client using GITHUB_TOKEN from the environment.
func NewClientFromEnv() (*Client, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("GITHUB_TOKEN not set")
	}
	return NewClient(token), nil
}

// GetJSON performs a GET and decodes the JSON response.
// Used by main.go's find command for ad-hoc API calls.
func (c *Client) GetJSON(url string, target interface{}) error {
	req, err := c.client.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	_, err = c.client.Do(context.Background(), req, target)
	return err
}

// ── Search Types ──

// SearchResult represents a GitHub search result item.
type SearchResult struct {
	Number        int          `json:"number"`
	Title         string       `json:"title"`
	URL           string       `json:"url"`
	HTMLURL       string       `json:"html_url"`
	RepositoryURL string       `json:"repository_url"`
	Repository    Repository   `json:"repository"`
	PullReq       *PullReqInfo `json:"pull_request,omitempty"`
	User          User         `json:"user"`
	Body          string       `json:"body,omitempty"`
	Assignees     []User       `json:"assignees,omitempty"`
	HeadRef       string       `json:"headRefName,omitempty"`
	HeadRefOid    string       `json:"headRefOid,omitempty"`
	UpdatedAt     string       `json:"updatedAt,omitempty"`
}

// RepoName extracts the owner/name from Repository struct or repository_url.
func (s *SearchResult) RepoName() string {
	if s.Repository.NameWithOwner != "" {
		return s.Repository.NameWithOwner
	}
	if s.Repository.FullName != "" {
		return s.Repository.FullName
	}
	if s.RepositoryURL != "" {
		parts := strings.Split(s.RepositoryURL, "/repos/")
		if len(parts) == 2 {
			return parts[1]
		}
	}
	return ""
}

// Repository is a GitHub repo reference.
type Repository struct {
	NameWithOwner string `json:"nameWithOwner"`
	FullName      string `json:"full_name"`
	URL           string `json:"url"`
}

// PullReqInfo indicates this search result is a PR.
type PullReqInfo struct {
	URL string `json:"url"`
}

// User is a GitHub user.
type User struct {
	Login string `json:"login"`
}

// Comment is a GitHub issue/PR comment.
type Comment struct {
	ID        int    `json:"id"`
	User      User   `json:"user"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

// ReviewComment is a PR review comment (inline code discussion on a specific line).
type ReviewComment struct {
	ID        int    `json:"id"`
	User      User   `json:"user"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
}

// ── Helper: owner/repo split ──

func splitRepo(repo string) (string, string) {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

// ── Search Methods ──

// searchIssues runs a general search and returns typed results.
func (c *Client) searchIssues(q string) ([]SearchResult, error) {
	opts := &gh.SearchOptions{
		Sort:  "created",
		Order: "desc",
		ListOptions: gh.ListOptions{PerPage: 100},
	}
	result, _, err := c.client.Search.Issues(context.Background(), q, opts)
	if err != nil {
		return nil, err
	}
	var items []SearchResult
	for _, i := range result.Issues {
		items = append(items, convertIssue(i))
	}
	return items, nil
}

func convertIssue(i *gh.Issue) SearchResult {
	r := SearchResult{
		Number:        i.GetNumber(),
		Title:         i.GetTitle(),
		URL:           i.GetURL(),
		HTMLURL:       i.GetHTMLURL(),
		RepositoryURL: i.GetRepositoryURL(),
		Body:          i.GetBody(),
		User:          User{Login: i.GetUser().GetLogin()},
		HeadRef:       i.GetPullRequestLinks().GetURL(),
	}
	if i.PullRequestLinks != nil {
		r.PullReq = &PullReqInfo{URL: i.PullRequestLinks.GetURL()}
	}
	if i.Repository != nil {
		r.Repository = Repository{
			NameWithOwner: i.Repository.GetFullName(),
			FullName:      i.Repository.GetFullName(),
			URL:           i.Repository.GetURL(),
		}
	}
	for _, a := range i.Assignees {
		r.Assignees = append(r.Assignees, User{Login: a.GetLogin()})
	}
	return r
}

// SearchIssues finds issues mentioning @zelvinator in body/title.
func (c *Client) SearchIssues() ([]SearchResult, error) {
	q := "@zelvinator+in:title,body+is:issue+state:open"
	results, err := c.searchIssues(q)
	if err != nil {
		return nil, err
	}
	var issues []SearchResult
	for _, item := range results {
		if item.PullReq == nil {
			issues = append(issues, item)
		}
	}
	return issues, nil
}

// SearchIssueComments finds issues mentioning @zelvinator in comments.
func (c *Client) SearchIssueComments() ([]SearchResult, error) {
	q := "@zelvinator+in:comments+is:issue+state:open"
	results, err := c.searchIssues(q)
	if err != nil {
		return nil, err
	}
	var issues []SearchResult
	for _, item := range results {
		if item.PullReq == nil {
			issues = append(issues, item)
		}
	}
	return issues, nil
}

// SearchPRs finds PRs mentioning @zelvinator in body/title.
func (c *Client) SearchPRs() ([]SearchResult, error) {
	q := "@zelvinator+in:title,body+type:pr+state:open"
	results, err := c.searchIssues(q)
	if err != nil {
		return nil, err
	}
	var prs []SearchResult
	for _, item := range results {
		if item.PullReq != nil {
			prs = append(prs, item)
		}
	}
	return prs, nil
}

// SearchPRComments finds PRs mentioning @zelvinator in comments.
func (c *Client) SearchPRComments() ([]SearchResult, error) {
	q := "@zelvinator+in:comments+type:pr+state:open"
	results, err := c.searchIssues(q)
	if err != nil {
		return nil, err
	}
	var prs []SearchResult
	for _, item := range results {
		if item.PullReq != nil {
			prs = append(prs, item)
		}
	}
	return prs, nil
}

// SearchAssignedIssues finds open issues assigned to a specific user across all accessible repos.
// Uses GET /issues?filter=assigned which works for the authenticated user (unlike search API).
func (c *Client) SearchAssignedIssues(assignee string) ([]SearchResult, error) {
	// Fetch issues assigned to the authenticated user via the issues API
	var raw []struct {
		Number       int          `json:"number"`
		Title        string       `json:"title"`
		HTMLURL      string       `json:"html_url"`
		Repository   Repository   `json:"repository"`
		User         User         `json:"user"`
		Body         string       `json:"body"`
		PullRequest  interface{}  `json:"pull_request"`
		Assignees    []User       `json:"assignees"`
		RepositoryURL string      `json:"repository_url"`
		URL          string       `json:"url"`
	}
	url := "https://api.github.com/issues?filter=assigned&state=open&per_page=100"
	if err := c.GetJSON(url, &raw); err != nil {
		return nil, err
	}
	var results []SearchResult
	for _, item := range raw {
		// Only include issues (not PRs)
		if item.PullRequest != nil {
			continue
		}
		// Verify the issue is actually assigned to the specified user
		assigneeMatch := false
		for _, a := range item.Assignees {
			if a.Login == assignee {
				assigneeMatch = true
				break
			}
		}
		if !assigneeMatch {
			continue
		}
		sr := SearchResult{
			Number:        item.Number,
			Title:         item.Title,
			HTMLURL:       item.HTMLURL,
			Body:          item.Body,
			User:          item.User,
			Assignees:     item.Assignees,
			RepositoryURL: item.RepositoryURL,
			URL:           item.URL,
		}
		if item.Repository.NameWithOwner != "" {
			sr.Repository = item.Repository
		} else if item.RepositoryURL != "" {
			sr.Repository = Repository{
				FullName: strings.TrimPrefix(item.RepositoryURL, "https://api.github.com/repos/"),
			}
		}
		results = append(results, sr)
	}
	return results, nil
}

// SearchAuthorPRs finds open PRs by a specific author across all accessible repos.
// Uses GET /issues?filter=created which works for the authenticated user (unlike search API).
func (c *Client) SearchAuthorPRs(author string) ([]SearchResult, error) {
	// Fetch issues/PRs created by the authenticated user via the issues API
	var raw []struct {
		Number       int          `json:"number"`
		Title        string       `json:"title"`
		HTMLURL      string       `json:"html_url"`
		Repository   Repository   `json:"repository"`
		User         User         `json:"user"`
		Body         string       `json:"body"`
		PullRequest  interface{}  `json:"pull_request"`
		RepositoryURL string      `json:"repository_url"`
		URL          string       `json:"url"`
	}
	url := "https://api.github.com/issues?filter=created&state=open&per_page=100"
	if err := c.GetJSON(url, &raw); err != nil {
		return nil, err
	}
	var results []SearchResult
	for _, item := range raw {
		// Only include PRs
		if item.PullRequest == nil {
			continue
		}
		sr := SearchResult{
			Number:        item.Number,
			Title:         item.Title,
			HTMLURL:       item.HTMLURL,
			Body:          item.Body,
			User:          item.User,
			RepositoryURL: item.RepositoryURL,
			URL:           item.URL,
			PullReq:       &PullReqInfo{URL: item.URL},
		}
		if item.Repository.NameWithOwner != "" {
			sr.Repository = item.Repository
		} else if item.RepositoryURL != "" {
			sr.Repository = Repository{
				FullName: strings.TrimPrefix(item.RepositoryURL, "https://api.github.com/repos/"),
			}
		}
		results = append(results, sr)
	}
	return results, nil
}

// SearchOpenPRs finds all open PRs across all accessible repos, limited to recently updated.
func (c *Client) SearchOpenPRs() ([]SearchResult, error) {
	q := "is:pr+state:open"
	results, err := c.searchIssues(q)
	if err != nil {
		return nil, err
	}
	var prs []SearchResult
	for _, item := range results {
		if item.PullReq != nil {
			prs = append(prs, item)
		}
	}
	return prs, nil
}

// ── Item Retrieval ──

// GetIssueBody fetches the body of an issue/PR.
func (c *Client) GetIssueBody(repo string, number int) (string, error) {
	owner, name := splitRepo(repo)
	if owner == "" {
		return "", fmt.Errorf("invalid repo: %s", repo)
	}
	issue, _, err := c.client.Issues.Get(context.Background(), owner, name, number)
	if err != nil {
		return "", err
	}
	return issue.GetBody(), nil
}

// GetIssueComments fetches comments on an issue/PR.
func (c *Client) GetIssueComments(repo string, number int) ([]Comment, error) {
	owner, name := splitRepo(repo)
	if owner == "" {
		return nil, fmt.Errorf("invalid repo: %s", repo)
	}
	opts := &gh.IssueListCommentsOptions{
		Sort:        gh.String("created"),
		ListOptions: gh.ListOptions{PerPage: 20},
	}
	ghComments, _, err := c.client.Issues.ListComments(context.Background(), owner, name, number, opts)
	if err != nil {
		return nil, err
	}
	var comments []Comment
	for _, gc := range ghComments {
		comments = append(comments, Comment{
			ID:        int(gc.GetID()),
			User:      User{Login: gc.GetUser().GetLogin()},
			Body:      gc.GetBody(),
			CreatedAt: gc.GetCreatedAt().Format("2006-01-02T15:04:05Z"),
		})
	}
	return comments, nil
}

// GetPRReviewComments fetches review comments on a pull request (inline code discussions).
func (c *Client) GetPRReviewComments(repo string, number int) ([]ReviewComment, error) {
	owner, name := splitRepo(repo)
	if owner == "" {
		return nil, fmt.Errorf("invalid repo: %s", repo)
	}
	opts := &gh.PullRequestListCommentsOptions{
		Sort:        "created",
		ListOptions: gh.ListOptions{PerPage: 50},
	}
	ghComments, _, err := c.client.PullRequests.ListComments(context.Background(), owner, name, number, opts)
	if err != nil {
		return nil, err
	}
	var comments []ReviewComment
	for _, gc := range ghComments {
		comments = append(comments, ReviewComment{
			ID:        int(gc.GetID()),
			User:      User{Login: gc.GetUser().GetLogin()},
			Body:      gc.GetBody(),
			CreatedAt: gc.GetCreatedAt().Format("2006-01-02T15:04:05Z"),
		})
	}
	return comments, nil
}

// ── Actions ──

// CreateComment posts a comment on an issue or PR.
func (c *Client) CreateComment(repo string, number int, body string) error {
	owner, name := splitRepo(repo)
	if owner == "" {
		return fmt.Errorf("invalid repo: %s", repo)
	}
	comment := &gh.IssueComment{Body: gh.String(body)}
	_, _, err := c.client.Issues.CreateComment(context.Background(), owner, name, number, comment)
	return err
}

// CreateReview posts a review on a PR.
func (c *Client) CreateReview(repo string, number int, body string, event string) error {
	owner, name := splitRepo(repo)
	if owner == "" {
		return fmt.Errorf("invalid repo: %s", repo)
	}
	review := &gh.PullRequestReviewRequest{
		Body:  gh.String(body),
		Event: gh.String(event),
	}
	_, _, err := c.client.PullRequests.CreateReview(context.Background(), owner, name, number, review)
	return err
}

// ReplyToReviewComment posts an inline reply to a specific PR review comment.
func (c *Client) ReplyToReviewComment(repo string, number int, reviewCommentID int, body string) error {
	owner, name := splitRepo(repo)
	if owner == "" {
		return fmt.Errorf("invalid repo: %s", repo)
	}
	comment := &gh.PullRequestComment{
		Body:      gh.String(body),
		InReplyTo: gh.Int64(int64(reviewCommentID)),
	}
	_, _, err := c.client.PullRequests.CreateComment(context.Background(), owner, name, number, comment)
	return err
}

// ── CI Check Types ──

// CheckRun is a single check run from the GitHub API.
type CheckRun struct {
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
	DetailsURL string `json:"details_url"`
	URL        string `json:"url"`
}

// StatusItem is a single commit status.
type StatusItem struct {
	Context string `json:"context"`
	State   string `json:"state"`
}

// GetCheckRuns fetches all check runs for a commit SHA and returns only failed ones.
func (c *Client) GetCheckRuns(repo, sha string) ([]CheckRun, error) {
	owner, name := splitRepo(repo)
	if owner == "" {
		return nil, fmt.Errorf("invalid repo: %s", repo)
	}
	opts := &gh.ListCheckRunsOptions{
		ListOptions: gh.ListOptions{PerPage: 50},
	}
	allRuns, _, err := c.client.Checks.ListCheckRunsForRef(context.Background(), owner, name, sha, opts)
	if err != nil {
		return nil, err
	}
	var failed []CheckRun
	for _, cr := range allRuns.CheckRuns {
		switch cr.GetConclusion() {
		case "failure", "action_required", "cancelled", "timed_out", "startup_failure":
			failed = append(failed, CheckRun{
				Name:       cr.GetName(),
				Conclusion: cr.GetConclusion(),
				DetailsURL: cr.GetDetailsURL(),
				URL:        cr.GetURL(),
			})
		}
	}
	return failed, nil
}

// GetStatuses fetches commit statuses for a SHA and returns only failed/error ones.
func (c *Client) GetStatuses(repo, sha string) ([]StatusItem, error) {
	owner, name := splitRepo(repo)
	if owner == "" {
		return nil, fmt.Errorf("invalid repo: %s", repo)
	}
	statuses, _, err := c.client.Repositories.GetCombinedStatus(context.Background(), owner, name, sha, nil)
	if err != nil {
		return nil, err
	}
	var failed []StatusItem
	for _, s := range statuses.Statuses {
		if s.GetState() == "failure" || s.GetState() == "error" {
			failed = append(failed, StatusItem{
				Context: s.GetContext(),
				State:   s.GetState(),
			})
		}
	}
	return failed, nil
}

// ── PR Info ──

// PRInfo contains PR details.
type PRInfo struct {
	Head PRHead `json:"head"`
	Body string `json:"body"`
	Base PRBase `json:"base"`
}

// PRHead is the head of a PR.
type PRHead struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
	Repo struct {
		FullName string `json:"full_name"`
		CloneURL string `json:"clone_url"`
	} `json:"repo"`
}

// PRBase is the base of a PR.
type PRBase struct {
	Ref string `json:"ref"`
	Repo struct {
		CloneURL string `json:"clone_url"`
		FullName string `json:"full_name"`
	} `json:"repo"`
}

// GetPR fetches PR details including head ref and SHA.
func (c *Client) GetPR(repo string, number int) (*PRInfo, error) {
	owner, name := splitRepo(repo)
	if owner == "" {
		return nil, fmt.Errorf("invalid repo: %s", repo)
	}
	pr, _, err := c.client.PullRequests.Get(context.Background(), owner, name, number)
	if err != nil {
		return nil, err
	}
	info := &PRInfo{
		Body: pr.GetBody(),
		Head: PRHead{
			Ref: pr.GetHead().GetRef(),
			SHA: pr.GetHead().GetSHA(),
		},
		Base: PRBase{
			Ref: pr.GetBase().GetRef(),
		},
	}
	if pr.GetHead().GetRepo() != nil {
		info.Head.Repo.FullName = pr.GetHead().GetRepo().GetFullName()
		info.Head.Repo.CloneURL = pr.GetHead().GetRepo().GetCloneURL()
	}
	if pr.GetBase().GetRepo() != nil {
		info.Base.Repo.FullName = pr.GetBase().GetRepo().GetFullName()
		info.Base.Repo.CloneURL = pr.GetBase().GetRepo().GetCloneURL()
	}
	return info, nil
}
