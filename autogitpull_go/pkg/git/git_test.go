package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteWebURL(t *testing.T) {
	tests := map[string]string{
		"git@github.com:owner/repo.git":         "https://github.com/owner/repo",
		"ssh://git@gitlab.com/owner/repo.git":   "https://gitlab.com/owner/repo",
		"https://github.com/owner/repo.git":     "https://github.com/owner/repo",
		"https://example.com/owner/repo?view=1": "https://example.com/owner/repo?view=1",
		"/tmp/local-repo":                       "",
		"file:///tmp/local-repo":                "",
	}
	for remote, want := range tests {
		t.Run(remote, func(t *testing.T) {
			if got := RemoteWebURL(remote); got != want {
				t.Fatalf("RemoteWebURL(%q) = %q, want %q", remote, got, want)
			}
		})
	}
}

func TestGitDiffPatchForFileLimitedContext(t *testing.T) {
	repo := t.TempDir()
	runGit := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init", "-q")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "user.name", "Test")
	file := filepath.Join(repo, "sample.txt")
	before := "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n"
	if err := os.WriteFile(file, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "sample.txt")
	runGit("commit", "-qm", "before")
	fromRev := runGit("rev-parse", "HEAD")
	after := strings.Replace(before, "six", "SIX", 1)
	if err := os.WriteFile(file, []byte(after), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "sample.txt")
	runGit("commit", "-qm", "after")
	toRev := runGit("rev-parse", "HEAD")

	compact, truncated, err := GitDiffPatchForFileLimitedContext(repo, fromRev, toRev, "sample.txt", 4096, 0)
	if err != nil || truncated {
		t.Fatalf("compact diff: truncated=%v err=%v", truncated, err)
	}
	if strings.Contains(compact, "seven") {
		t.Fatalf("zero-context diff contains unchanged neighbors:\n%s", compact)
	}
	detailed, _, err := GitDiffPatchForFileLimitedContext(repo, fromRev, toRev, "sample.txt", 4096, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detailed, "five") || !strings.Contains(detailed, "seven") {
		t.Fatalf("three-line context missing unchanged neighbors:\n%s", detailed)
	}
}
