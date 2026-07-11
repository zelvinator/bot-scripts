// Package main — ci-fix command: diagnose and fix CI failures on zelvinator PRs.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zelvinator/bot-scripts/scripts/zelvinator/internal/config"
	"github.com/zelvinator/bot-scripts/scripts/zelvinator/internal/github"
)

// runCIFix diagnoses and attempts to fix CI failures on a zelvinator PR.
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

	prInfo, err := client.GetPR(repo, number)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching PR: %v\n", err)
		os.Exit(1)
	}

	sha := prInfo.Head.SHA
	branch := prInfo.Head.Ref

	failedChecks, _ := client.GetCheckRuns(repo, sha)
	failedStatuses, _ := client.GetStatuses(repo, sha)

	if len(failedChecks) == 0 && len(failedStatuses) == 0 {
		fmt.Printf("No CI failures detected on %s#%d\n", repo, number)
		return
	}

	fmt.Printf("CI failures on %s#%d (branch: %s, sha: %s)\n", repo, number, branch, sha)

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

	summary.WriteString("\n### Local Diagnosis\n\n")

	if _, err := os.Stat(filepath.Join(cloneDir, "Makefile")); err == nil {
		summary.WriteString("**`make`**: ")
		if out, err := runCmd("make", "-C", cloneDir); err != nil {
			summary.WriteString(fmt.Sprintf("❌ Failed\n```\n%s\n```\n", out))
		} else {
			summary.WriteString("✅ Passed\n")
		}
	}

	if _, err := os.Stat(filepath.Join(cloneDir, "go.mod")); err == nil {
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

	client.CreateComment(repo, number, summary.String())
	fmt.Printf("CI diagnosis comment posted on %s#%d\n", repo, number)
}

// runCmd executes a command and returns combined stdout+stderr.
func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
