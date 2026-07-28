package state

import (
	"fmt"
	"testing"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		input    string
		wantCmd  string
		wantRest string
	}{
		{"@zelvinator /review this PR", "/review", "this PR"},
		{"@zelvinator /fix", "/fix", ""},
		{"@zelvinator /quick-review", "/quick-review", ""},
		{"@zelvinator /plan refactor the client", "/plan", "refactor the client"},
		{"@zelvinator nice work", "", "nice work"},
		{"@zelvinator /unknown", "/unknown", ""},
		{"no mention here", "", "no mention here"},
	}
	for _, tt := range tests {
		cmd, rest := ParseCommand(tt.input)
		if cmd != tt.wantCmd || rest != tt.wantRest {
			t.Errorf("ParseCommand(%q) = (%q, %q), want (%q, %q)", tt.input, cmd, rest, tt.wantCmd, tt.wantRest)
		}
	}
}

func TestIsKnownCommand(t *testing.T) {
	known := []string{"/review", "/quick-review", "/fix", "/quick-fix", "/plan", "/implement", "/quick-implement", "/status", "/help"}
	unknown := []string{"", "/unknown", "/something", "review"}
	for _, cmd := range known {
		if !IsKnownCommand(cmd) {
			t.Errorf("IsKnownCommand(%q) = false, want true", cmd)
		}
	}
	for _, cmd := range unknown {
		if IsKnownCommand(cmd) {
			t.Errorf("IsKnownCommand(%q) = true, want false", cmd)
		}
	}
}

func TestIsQuickCommand(t *testing.T) {
	quick := []string{"/quick-review", "/quick-fix", "/quick-implement"}
	notQuick := []string{"/review", "/fix", "/plan", "/implement", "/status", "/help", ""}
	for _, cmd := range quick {
		if !IsQuickCommand(cmd) {
			t.Errorf("IsQuickCommand(%q) = false, want true", cmd)
		}
	}
	for _, cmd := range notQuick {
		if IsQuickCommand(cmd) {
			t.Errorf("IsQuickCommand(%q) = true, want false", cmd)
		}
	}
}

func TestParseCommandMain(t *testing.T) {
	// Run all tests and print results
	tests := []string{
		"@zelvinator /review this PR",
		"@zelvinator /fix",
		"@zelvinator /quick-review",
		"@zelvinator /plan refactor the client",
		"@zelvinator nice work",
		"@zelvinator /unknown",
		"no mention here",
	}
	for _, s := range tests {
		cmd, rest := ParseCommand(s)
		fmt.Printf("  %-40s cmd=%-15s rest=%q\n", s, cmd, rest)
	}
}
