// Package state provides SQLite-backed state management for the zelvinator bot.
// It replaces the flat-file tracker with a proper state machine.
package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Item states.
const (
	StateDiscovered     = "discovered"
	StateNeedsPlanning  = "needs_planning"
	StatePlanned        = "planned"
	StateImplementing   = "implementing"
	StateReviewPending  = "review_pending"
	StateNeedsReview    = "needs_review"
	StateFixNeeded      = "fix_needed"
	StateDone           = "done"
	StateFailed         = "failed"
	StateDeferred       = "deferred"
)

// ValidTransitions defines allowed state transitions.
var validTransitions = map[string][]string{
	StateDiscovered:    {StateNeedsPlanning, StateReviewPending, StateDone, StateDeferred},
	StateNeedsPlanning: {StatePlanned},
	StatePlanned:       {StateImplementing},
	StateImplementing:  {StateReviewPending, StateFailed},
	StateReviewPending: {StateDone, StateFixNeeded, StateNeedsReview},
	StateNeedsReview:   {StateDone, StateFixNeeded},
	StateFixNeeded:     {StateImplementing, StateDone}, // Done = GLM takeover
	StateFailed:        {StatePlanned}, // Allow retry from failed
	StateDeferred:      {StateNeedsPlanning}, // Can be re-evaluated
	StateDone:          {}, // Terminal
}

// Item represents a work item in the state machine.
type Item struct {
	ID             string `json:"id"`
	Repo           string `json:"repo"`
	Number         int    `json:"number"`
	Type           string `json:"type"`
	TriggerSource  string `json:"trigger_source"`
	TriggerComment string `json:"trigger_comment"`
	Title          string `json:"title"`
	BodyPreview    string `json:"body_preview"`
	Branch         string `json:"branch"`
	Author         string `json:"author"`

	State          string  `json:"state"`
	Command        string  `json:"command"` // slash command: /review, /fix, /plan, /implement, /quick-*, /status, or ""
	Plan           *string `json:"plan"`
	ReviewFeedback *string `json:"review_feedback"`
	PRURL          *string `json:"pr_url"`
	Attempts       int     `json:"attempts"`
	MaxAttempts    int     `json:"max_attempts"`
	Error          *string `json:"error"`

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Plan is the structured implementation plan from GLM to Qwen.
type Plan struct {
	Summary           string         `json:"summary"`
	Files             []PlanFile     `json:"files"`
	AcceptanceCriteria []string      `json:"acceptance_criteria"`
	Notes             string         `json:"notes"`
}

// PlanFile describes changes to a single file.
type PlanFile struct {
	Path    string   `json:"path"`
	Action  string   `json:"action"` // "modify" | "create" | "delete"
	Changes []string `json:"changes"`
}

// DB wraps the SQLite database.
type DB struct {
	db *sql.DB
}

// schemaSQL is the database schema.
const schemaSQL = `
CREATE TABLE IF NOT EXISTS items (
    id              TEXT PRIMARY KEY,
    repo            TEXT NOT NULL,
    number          INTEGER NOT NULL,
    type            TEXT NOT NULL,
    trigger_source  TEXT NOT NULL,
    trigger_comment TEXT,
    command         TEXT NOT NULL DEFAULT '',
    title           TEXT,
    body_preview    TEXT,
    branch          TEXT,
    author          TEXT,
    state           TEXT NOT NULL DEFAULT 'discovered',
    plan            TEXT,
    review_feedback TEXT,
    pr_url          TEXT,
    attempts        INTEGER NOT NULL DEFAULT 0,
    max_attempts    INTEGER NOT NULL DEFAULT 3,
    error           TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_state ON items(state);
CREATE INDEX IF NOT EXISTS idx_updated ON items(updated_at);
`

// Open opens or creates the SQLite database at the given path.
func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}

	// Enable WAL mode for better concurrent read performance
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}

	// Create schema
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	// Migration: add command column if it doesn't exist (for existing DBs)
	db.Exec("ALTER TABLE items ADD COLUMN command TEXT NOT NULL DEFAULT ''")

	return &DB{db: db}, nil
}

// Close closes the database.
func (d *DB) Close() error {
	return d.db.Close()
}

// MakeID generates a stable item ID from type, repo, and number.
func MakeID(itemType, repo string, number int) string {
	return fmt.Sprintf("%s:%s#%d", itemType, repo, number)
}

// MakeIDWithComment generates an item ID that includes comment ID for uniqueness.
func MakeIDWithComment(itemType, repo string, number, commentID int) string {
	return fmt.Sprintf("%s:%s#%d:comment:%d", itemType, repo, number, commentID)
}

// MakeAssignmentID generates an item ID for assignment-triggered items.
func MakeAssignmentID(repo string, number int) string {
	return fmt.Sprintf("assigned:issue:%s#%d", repo, number)
}

// MakeCIID generates an item ID for CI failure items.
func MakeCIID(repo string, number int) string {
	return fmt.Sprintf("ci:pr:%s#%d", repo, number)
}

// InsertIfNew inserts a new item if it doesn't already exist.
// Returns true if the item was newly inserted, false if it already existed.
func (d *DB) InsertIfNew(item Item) (bool, error) {
	_, err := d.db.Exec(`
		INSERT OR IGNORE INTO items (id, repo, number, type, trigger_source, trigger_comment, command, title, body_preview, branch, author, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'discovered', datetime('now'), datetime('now'))
	`,
		item.ID, item.Repo, item.Number, item.Type, item.TriggerSource,
		item.TriggerComment, item.Command, item.Title, item.BodyPreview, item.Branch, item.Author,
	)
	if err != nil {
		return false, fmt.Errorf("insert item %s: %w", item.ID, err)
	}

	rows, err := d.db.Query("SELECT changes()")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	if rows.Next() {
		var n int
		rows.Scan(&n)
		return n > 0, nil
	}
	return false, nil
}

// Get retrieves an item by ID.
func (d *DB) Get(id string) (*Item, error) {
	row := d.db.QueryRow(`
		SELECT id, repo, number, type, trigger_source, trigger_comment, command, title, body_preview, branch, author,
		       state, plan, review_feedback, pr_url, attempts, max_attempts, error, created_at, updated_at
		FROM items WHERE id = ?
	`, id)

	return scanItem(row)
}

// Transition changes an item's state with validation.
func (d *DB) Transition(id, newState string) error {
	current, err := d.Get(id)
	if err != nil {
		return fmt.Errorf("get item for transition: %w", err)
	}

	allowed, ok := validTransitions[current.State]
	if !ok {
		return fmt.Errorf("unknown current state: %s", current.State)
	}

	valid := false
	for _, s := range allowed {
		if s == newState {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("invalid transition: %s → %s", current.State, newState)
	}

	// Increment attempts when entering implementing from fix_needed or planned
	attemptsInc := 0
	if newState == StateImplementing && (current.State == StateFixNeeded || current.State == StatePlanned) {
		attemptsInc = 1
	}

	_, err = d.db.Exec(`
		UPDATE items SET state = ?, attempts = attempts + ?, updated_at = datetime('now') WHERE id = ?
	`, newState, attemptsInc, id)
	return err
}

// SetPlan stores a plan JSON for an item.
func (d *DB) SetPlan(id string, planJSON string) error {
	_, err := d.db.Exec(`
		UPDATE items SET plan = ?, updated_at = datetime('now') WHERE id = ?
	`, planJSON, id)
	return err
}

// GetPlan retrieves the plan for an item as a parsed Plan struct.
func (d *DB) GetPlan(id string) (*Plan, error) {
	var planJSON *string
	err := d.db.QueryRow("SELECT plan FROM items WHERE id = ?", id).Scan(&planJSON)
	if err != nil {
		return nil, err
	}
	if planJSON == nil {
		return nil, nil
	}

	var plan Plan
	if err := json.Unmarshal([]byte(*planJSON), &plan); err != nil {
		return nil, fmt.Errorf("parse plan: %w", err)
	}
	return &plan, nil
}

// SetReviewFeedback stores review feedback for an item.
func (d *DB) SetReviewFeedback(id, feedback string) error {
	_, err := d.db.Exec(`
		UPDATE items SET review_feedback = ?, updated_at = datetime('now') WHERE id = ?
	`, feedback, id)
	return err
}

// SetPRURL stores the PR URL for an item.
func (d *DB) SetPRURL(id, prURL string) error {
	_, err := d.db.Exec(`
		UPDATE items SET pr_url = ?, updated_at = datetime('now') WHERE id = ?
	`, prURL, id)
	return err
}

// SetError stores an error message for an item.
func (d *DB) SetError(id, errMsg string) error {
	_, err := d.db.Exec(`
		UPDATE items SET error = ?, updated_at = datetime('now') WHERE id = ?
	`, errMsg, id)
	return err
}

// QueryByState returns all items in the given state.
func (d *DB) QueryByState(state string) ([]Item, error) {
	rows, err := d.db.Query(`
		SELECT id, repo, number, type, trigger_source, trigger_comment, command, title, body_preview, branch, author,
		       state, plan, review_feedback, pr_url, attempts, max_attempts, error, created_at, updated_at
		FROM items WHERE state = ?
		ORDER BY created_at ASC
	`, state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Item
	for rows.Next() {
		item, err := scanItemRows(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, nil
}

// QueryByStates returns all items in any of the given states.
func (d *DB) QueryByStates(states ...string) ([]Item, error) {
	if len(states) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(states))
	args := make([]interface{}, len(states))
	for i, s := range states {
		placeholders[i] = "?"
		args[i] = s
	}
	query := fmt.Sprintf(`
		SELECT id, repo, number, type, trigger_source, trigger_comment, command, title, body_preview, branch, author,
		       state, plan, review_feedback, pr_url, attempts, max_attempts, error, created_at, updated_at
		FROM items WHERE state IN (%s)
		ORDER BY created_at ASC
	`, strings.Join(placeholders, ","))

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Item
	for rows.Next() {
		item, err := scanItemRows(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, nil
}

// ResetStale resets items stuck in "implementing" for longer than the given duration
// back to "planned" so they can be retried.
func (d *DB) ResetStale(maxAge time.Duration) (int, error) {
	// Use strftime to compare timestamps in SQLite
	seconds := int(maxAge.Seconds())
	res, err := d.db.Exec(`
		UPDATE items
		SET state = 'planned', updated_at = datetime('now')
		WHERE state = 'implementing'
		  AND strftime('%%s', updated_at) < strftime('%%s', datetime('now')) - ?
	`, seconds)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// Stats returns a count of items in each state.
func (d *DB) Stats() (map[string]int, error) {
	rows, err := d.db.Query("SELECT state, COUNT(*) FROM items GROUP BY state")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make(map[string]int)
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return nil, err
		}
		stats[state] = count
	}
	return stats, nil
}

// ── Helpers ──

// scannable is an interface satisfied by both *sql.Row and *sql.Rows.
type scannable interface {
	Scan(dest ...any) error
}

func scanItem(row scannable) (*Item, error) {
	var item Item
	var triggerComment, title, bodyPreview, branch, author, plan, reviewFeedback, prURL, errMsg sql.NullString

	err := row.Scan(
		&item.ID, &item.Repo, &item.Number, &item.Type, &item.TriggerSource,
		&triggerComment, &item.Command, &title, &bodyPreview, &branch, &author,
		&item.State, &plan, &reviewFeedback, &prURL,
		&item.Attempts, &item.MaxAttempts, &errMsg,
		&item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	item.TriggerComment = triggerComment.String
	item.Title = title.String
	item.BodyPreview = bodyPreview.String
	item.Branch = branch.String
	item.Author = author.String

	if plan.Valid {
		s := plan.String
		item.Plan = &s
	}
	if reviewFeedback.Valid {
		s := reviewFeedback.String
		item.ReviewFeedback = &s
	}
	if prURL.Valid {
		s := prURL.String
		item.PRURL = &s
	}
	if errMsg.Valid {
		s := errMsg.String
		item.Error = &s
	}

	return &item, nil
}

// scanItemRows wraps scanItem for *sql.Rows (identical interface, separate for clarity).
func scanItemRows(rows *sql.Rows) (*Item, error) {
	return scanItem(rows)
}

// ParseCommand extracts a slash command from a trigger comment.
// Returns the command (e.g. "/review", "/quick-fix") and the rest of the text.
// If no command is found, returns "" and the full text.
//
// Examples:
//
//	"@zelvinator /review this PR"  → "/review", "this PR"
//	"@zelvinator /fix"             → "/fix", ""
//	"@zelvinator /quick-review"    → "/quick-review", ""
//	"@zelvinator nice work"        → "", "nice work"
func ParseCommand(comment string) (command string, rest string) {
	// Find "@zelvinator" (case-insensitive) and look for a /command after it
	lower := strings.ToLower(comment)
	idx := strings.Index(lower, "@zelvinator")
	if idx < 0 {
		return "", comment
	}

	// Get everything after "@zelvinator"
	after := strings.TrimSpace(comment[idx+len("@zelvinator"):])

	// Check if it starts with a slash command
	if !strings.HasPrefix(after, "/") {
		return "", after
	}

	// Extract the command (up to first space or end of string)
	spaceIdx := strings.Index(after, " ")
	if spaceIdx < 0 {
		return after, ""
	}
	return after[:spaceIdx], strings.TrimSpace(after[spaceIdx+1:])
}

// IsKnownCommand returns true if the command is a recognized slash command.
func IsKnownCommand(cmd string) bool {
	switch cmd {
	case "/review", "/quick-review",
		"/fix", "/quick-fix",
		"/plan",
		"/implement", "/quick-implement",
		"/status", "/help":
		return true
	default:
		return false
	}
}

// IsQuickCommand returns true if the command is a /quick-* variant (Qwen only, no GLM).
func IsQuickCommand(cmd string) bool {
	return strings.HasPrefix(cmd, "/quick-")
}
