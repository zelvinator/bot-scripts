// Package main — queue, state, plan, and stale subcommands.
// These provide the SQLite state management interface used by both cron jobs.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/zelvinator/bot-scripts/scripts/zelvinator/internal/state"
)

// runQueue prints items in the given state as JSON.
func runQueue(db *state.DB, args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: zelvinator queue --state=<state> [--state=<state2>...]\n")
		fmt.Fprintf(os.Stderr, "States: discovered, needs_planning, planned, implementing, review_pending, needs_review, fix_needed, done, failed, deferred\n")
		os.Exit(1)
	}

	var states []string
	for _, a := range args {
		if len(a) > 8 && a[:8] == "--state=" {
			states = append(states, a[8:])
		}
	}
	if len(states) == 0 {
		fmt.Fprintf(os.Stderr, "No --state specified\n")
		os.Exit(1)
	}

	items, err := db.QueryByStates(states...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Query error: %v\n", err)
		os.Exit(1)
	}

	if len(items) == 0 {
		fmt.Println("[]")
		return
	}

	data, _ := json.MarshalIndent(items, "", "  ")
	fmt.Println(string(data))
}

// runStateTransition transitions an item's state.
func runStateTransition(db *state.DB, args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: zelvinator state <id> <new_state> [--plan=<file>] [--feedback=<text>] [--pr-url=<url>] [--error=<text>]\n")
		fmt.Fprintf(os.Stderr, "States: discovered, needs_planning, planned, implementing, review_pending, needs_review, fix_needed, done, failed, deferred\n")
		os.Exit(1)
	}

	id := args[0]
	newState := args[1]

	// Parse optional flags
	for _, a := range args[2:] {
		switch {
		case len(a) > 7 && a[:7] == "--plan=":
			planFile := a[7:]
			planData, err := os.ReadFile(planFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Cannot read plan file %s: %v\n", planFile, err)
				os.Exit(1)
			}
			if err := db.SetPlan(id, string(planData)); err != nil {
				fmt.Fprintf(os.Stderr, "Set plan error: %v\n", err)
				os.Exit(1)
			}
		case len(a) > 11 && a[:11] == "--feedback=":
			feedback := a[11:]
			if err := db.SetReviewFeedback(id, feedback); err != nil {
				fmt.Fprintf(os.Stderr, "Set feedback error: %v\n", err)
				os.Exit(1)
			}
		case len(a) > 9 && a[:9] == "--pr-url=":
			prURL := a[9:]
			if err := db.SetPRURL(id, prURL); err != nil {
				fmt.Fprintf(os.Stderr, "Set PR URL error: %v\n", err)
				os.Exit(1)
			}
		case len(a) > 8 && a[:8] == "--error=":
			errMsg := a[8:]
			if err := db.SetError(id, errMsg); err != nil {
				fmt.Fprintf(os.Stderr, "Set error: %v\n", err)
				os.Exit(1)
			}
		}
	}

	if err := db.Transition(id, newState); err != nil {
		fmt.Fprintf(os.Stderr, "Transition error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Transitioned %s → %s\n", id, newState)
}

// runGetPlan prints the plan for an item as JSON.
func runGetPlan(db *state.DB, args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: zelvinator plan <id>\n")
		os.Exit(1)
	}

	plan, err := db.GetPlan(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Get plan error: %v\n", err)
		os.Exit(1)
	}
	if plan == nil {
		fmt.Println("{}")
		return
	}

	data, _ := json.MarshalIndent(plan, "", "  ")
	fmt.Println(string(data))
}

// runStale resets items stuck in "implementing" state.
func runStale(db *state.DB, args []string) {
	maxAge := 20 * time.Minute // default 20 minutes

	for _, a := range args {
		if a == "--reset" {
			n, err := db.ResetStale(maxAge)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Stale reset error: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Reset %d stale items back to 'planned'\n", n)
			return
		}
	}

	// Without --reset, just report
	items, err := db.QueryByState(state.StateImplementing)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Query error: %v\n", err)
		os.Exit(1)
	}

	staleCount := 0
	cutoff := time.Now().Add(-maxAge)
	for _, item := range items {
		updated, err := time.Parse("2006-01-02 15:04:05", item.UpdatedAt)
		if err != nil {
			continue
		}
		if updated.Before(cutoff) {
			staleCount++
			fmt.Printf("STALE: %s (updated %s)\n", item.ID, item.UpdatedAt)
		}
	}

	if staleCount == 0 {
		fmt.Println("No stale items.")
	}
}

// runStats prints item counts per state.
func runStats(db *state.DB) {
	stats, err := db.Stats()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Stats error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("State              Count")
	fmt.Println("─────────────────────────")
	for s, n := range stats {
		fmt.Printf("%-18s %d\n", s, n)
	}
}

// runResetDB resets the state database (requires --confirm).
func runResetDB(db *state.DB, args []string) {
	confirmed := false
	for _, a := range args {
		if a == "--confirm" {
			confirmed = true
		}
	}
	if !confirmed {
		fmt.Fprintln(os.Stderr, "This will delete ALL state data. Use --confirm to proceed.")
		os.Exit(1)
	}

	// Simplest: close and remove the DB file
	fmt.Println("Closing database for reset...")
	db.Close()

	dbPath := os.ExpandEnv("${HOME}/.hermes/zelvinator-bot/state.db")
	if err := os.Remove(dbPath); err != nil {
		fmt.Fprintf(os.Stderr, "Remove DB error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Database reset: %s removed. Will be recreated on next run.\n", dbPath)
}
