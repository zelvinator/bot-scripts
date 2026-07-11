// Package github provides a GitHub API client for the zelvinator bot.
package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// Client wraps the GitHub REST API.
type Client struct {
	token  string
	client *http.Client
}

// NewClient creates a GitHub client with a token.
func NewClient(token string) *Client {
	return &Client{
		token:  token,
		client: &http.Client{},
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

// doRequest performs an authenticated GitHub API request.
func (c *Client) doRequest(method, url string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "zelvinator-bot/1.0")
	return c.client.Do(req)
}

// GetJSON performs a GET and decodes the JSON response.
func (c *Client) GetJSON(url string, target interface{}) error {
	resp, err := c.doRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET %s: HTTP %d: %s", url, resp.StatusCode, string(body))
	}

	return json.NewDecoder(resp.Body).Decode(target)
}

// postJSON performs a POST with a JSON body.
func (c *Client) postJSON(url string, payload, target interface{}) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal payload: %w", err)
		}
		body = strings.NewReader(string(data))
	}

	resp, err := c.doRequest("POST", url, body)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST %s: HTTP %d: %s", url, resp.StatusCode, string(respBody))
	}

	if target != nil {
		return json.NewDecoder(resp.Body).Decode(target)
	}
	return nil
}

// ── Search Types ──

// SearchResult represents a GitHub search result item.
type SearchResult struct {
	Number        int          `json:"number"`
	Title         string       `json:"title"`
	URL           string       `json:"url"`
	HTMLURL       string       `json:"html_url"`
	RepositoryURL string       `json:"repository_url"`
	Repository    Repository  `json:"repository"`
	PullReq       *PullReqInfo `json:"pull_request,omitempty"`
	User          User        `json:"user"`
	Body          string      `json:"body,omitempty"`
	HeadRef       string      `json:"headRefName,omitempty"`
	HeadRefOid    string      `json:"headRefOid,omitempty"`
	UpdatedAt     string      `json:"updatedAt,omitempty"`
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

// SearchResponse is the top-level GitHub search response.
type SearchResponse struct {
	Items []SearchResult `json:"items"`
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

// ── Search Methods ──

// SearchIssues finds issues mentioning @zelvinator in body/title.
func (c *Client) SearchIssues(org string) ([]SearchResult, error) {
	query := fmt.Sprintf("@zelvinator+in:title,body+is:issue+org:%s+state:open", org)
	url := fmt.Sprintf("https://api.github.com/search/issues?q=%s&per_page=50&sort=created&order=desc", query)
	var resp SearchResponse
	if err := c.GetJSON(url, &resp); err != nil {
		return nil, err
	}
	// Filter out PRs (they shouldn't appear but just in case)
	var issues []SearchResult
	for _, item := range resp.Items {
		if item.PullReq == nil {
			issues = append(issues, item)
		}
	}
	return issues, nil
}

// SearchIssueComments finds issues mentioning @zelvinator in comments.
func (c *Client) SearchIssueComments(org string) ([]SearchResult, error) {
	query := fmt.Sprintf("@zelvinator+in:comments+is:issue+org:%s+state:open", org)
	url := fmt.Sprintf("https://api.github.com/search/issues?q=%s&per_page=100&sort=created&order=desc", query)
	var resp SearchResponse
	if err := c.GetJSON(url, &resp); err != nil {
		return nil, err
	}
	var issues []SearchResult
	for _, item := range resp.Items {
		if item.PullReq == nil {
			issues = append(issues, item)
		}
	}
	return issues, nil
}

// SearchPRs finds PRs mentioning @zelvinator in body/title.
func (c *Client) SearchPRs(org string) ([]SearchResult, error) {
	query := fmt.Sprintf("@zelvinator+in:title,body+type:pr+org:%s+state:open", org)
	url := fmt.Sprintf("https://api.github.com/search/issues?q=%s&per_page=50&sort=created&order=desc", query)
	var resp SearchResponse
	if err := c.GetJSON(url, &resp); err != nil {
		return nil, err
	}
	var prs []SearchResult
	for _, item := range resp.Items {
		if item.PullReq != nil {
			prs = append(prs, item)
		}
	}
	return prs, nil
}

// SearchPRComments finds PRs mentioning @zelvinator in comments.
func (c *Client) SearchPRComments(org string) ([]SearchResult, error) {
	query := fmt.Sprintf("@zelvinator+in:comments+type:pr+org:%s+state:open", org)
	url := fmt.Sprintf("https://api.github.com/search/issues?q=%s&per_page=100&sort=created&order=desc", query)
	var resp SearchResponse
	if err := c.GetJSON(url, &resp); err != nil {
		return nil, err
	}
	var prs []SearchResult
	for _, item := range resp.Items {
		if item.PullReq != nil {
			prs = append(prs, item)
		}
	}
	return prs, nil
}

// SearchAuthorPRs finds open PRs by a specific author in an org.
func (c *Client) SearchAuthorPRs(org, author string) ([]SearchResult, error) {
	query := fmt.Sprintf("author:%s+is:pr+state:open+org:%s", author, org)
	url := fmt.Sprintf("https://api.github.com/search/issues?q=%s&per_page=30&sort=updated&order=desc", query)
	var resp SearchResponse
	if err := c.GetJSON(url, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

// SearchOpenPRs finds all open PRs in an org, limited to recently updated.
// Used to discover PRs that mention @zelvinator only in review comments.
func (c *Client) SearchOpenPRs(org string) ([]SearchResult, error) {
	query := fmt.Sprintf("is:pr+state:open+org:%s", org)
	url := fmt.Sprintf("https://api.github.com/search/issues?q=%s&per_page=30&sort=updated&order=desc", query)
	var resp SearchResponse
	if err := c.GetJSON(url, &resp); err != nil {
		return nil, err
	}
	var prs []SearchResult
	for _, item := range resp.Items {
		if item.PullReq != nil {
			prs = append(prs, item)
		}
	}
	return prs, nil
}

// ── Item Retrieval ──

// GetIssueBody fetches the body of an issue/PR.
func (c *Client) GetIssueBody(repo string, number int) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/issues/%d", repo, number)
	var result struct {
		Body string `json:"body"`
	}
	if err := c.GetJSON(url, &result); err != nil {
		return "", err
	}
	return result.Body, nil
}

// GetIssueComments fetches comments on an issue/PR.
func (c *Client) GetIssueComments(repo string, number int) ([]Comment, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/issues/%d/comments?per_page=20&sort=created", repo, number)
	var comments []Comment
	if err := c.GetJSON(url, &comments); err != nil {
		return nil, err
	}
	return comments, nil
}

// GetPRReviewComments fetches review comments on a pull request (inline code discussions).
func (c *Client) GetPRReviewComments(repo string, number int) ([]ReviewComment, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d/comments?per_page=50&sort=created", repo, number)
	var comments []ReviewComment
	if err := c.GetJSON(url, &comments); err != nil {
		return nil, err
	}
	return comments, nil
}

// ── Actions ──

// CreateComment posts a comment on an issue or PR.
func (c *Client) CreateComment(repo string, number int, body string) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/issues/%d/comments", repo, number)
	payload := map[string]string{"body": body}
	return c.postJSON(url, payload, nil)
}

// ReviewPayload is the payload for creating a PR review.
type ReviewPayload struct {
	Body  string `json:"body"`
	Event string `json:"event"` // "APPROVE", "REQUEST_CHANGES", "COMMENT"
}

// CreateReview posts a review on a PR.
func (c *Client) CreateReview(repo string, number int, body string, event string) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d/reviews", repo, number)
	payload := ReviewPayload{Body: body, Event: event}
	return c.postJSON(url, payload, nil)
}

// ReplyToReviewComment posts an inline reply to a specific PR review comment.
// The reply appears threaded under the original review comment on the PR's Files changed tab.
type ReplyPayload struct {
	Body       string `json:"body"`
	InReplyTo  int    `json:"in_reply_to"`
}

func (c *Client) ReplyToReviewComment(repo string, number int, reviewCommentID int, body string) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d/comments", repo, number)
	payload := ReplyPayload{Body: body, InReplyTo: reviewCommentID}
	return c.postJSON(url, payload, nil)
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

// CheckRunResponse is the GitHub check runs API response.
type CheckRunResponse struct {
	CheckRuns []CheckRun `json:"check_runs"`
}

// StatusResponse is the GitHub commit status API response.
type StatusResponse struct {
	Statuses []StatusItem `json:"statuses"`
}

// GetCheckRuns fetches all check runs for a commit SHA.
func (c *Client) GetCheckRuns(repo, sha string) ([]CheckRun, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/commits/%s/check-runs", repo, sha)
	var resp CheckRunResponse
	if err := c.GetJSON(url, &resp); err != nil {
		return nil, err
	}
	var failed []CheckRun
	for _, cr := range resp.CheckRuns {
		switch cr.Conclusion {
		case "failure", "action_required", "cancelled", "timed_out", "startup_failure":
			failed = append(failed, cr)
		}
	}
	return failed, nil
}

// GetStatuses fetches commit statuses for a SHA.
func (c *Client) GetStatuses(repo, sha string) ([]StatusItem, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/commits/%s/status", repo, sha)
	var resp StatusResponse
	if err := c.GetJSON(url, &resp); err != nil {
		return nil, err
	}
	var failed []StatusItem
	for _, s := range resp.Statuses {
		if s.State == "failure" || s.State == "error" {
			failed = append(failed, s)
		}
	}
	return failed, nil
}

// CheckRunLogs fetches the logs for a check run.
func (c *Client) CheckRunLogs(repo string, checkRunID int) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/check-runs/%d", repo, checkRunID)
	var cr struct {
		Output struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
			Text    string `json:"text"`
		} `json:"output"`
	}
	if err := c.GetJSON(url, &cr); err != nil {
		return "", err
	}
	return cr.Output.Text, nil
}

// GetPR fetches PR details including head ref and SHA.
func (c *Client) GetPR(repo string, number int) (*PRInfo, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/pulls/%d", repo, number)
	var info PRInfo
	if err := c.GetJSON(url, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

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
