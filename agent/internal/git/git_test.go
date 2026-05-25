package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initTempRepo materialises a throwaway git repo for tests. It
// configures a local user.name/email so commit() works without
// touching the host's global config.
func initTempRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	run("config", "commit.gpgsign", "false")
	return dir
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func TestGetStagedDiff_EmptyInFreshRepo(t *testing.T) {
	dir := initTempRepo(t)
	chdir(t, dir)
	got, err := GetStagedDiff()
	if err != nil {
		t.Fatalf("GetStagedDiff: %v", err)
	}
	if strings.TrimSpace(got) != "" {
		t.Fatalf("expected empty diff in fresh repo, got %q", got)
	}
}

func TestGetStagedDiff_ReturnsStagedContent(t *testing.T) {
	dir := initTempRepo(t)
	chdir(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("git", "add", "foo.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	got, err := GetStagedDiff()
	if err != nil {
		t.Fatalf("GetStagedDiff: %v", err)
	}
	if !strings.Contains(got, "foo.txt") || !strings.Contains(got, "hello") {
		t.Fatalf("expected diff to mention foo.txt + hello, got:\n%s", got)
	}
}

func TestGetStagedDiff_OutsideGitRepoErrors(t *testing.T) {
	// A bare tmpdir is not a git repo. git diff --staged exits
	// non-zero in that case.
	dir := t.TempDir()
	chdir(t, dir)
	_, err := GetStagedDiff()
	if err == nil {
		t.Fatal("expected error outside git repo")
	}
}

func TestGetCurrentBranch_ReturnsMain(t *testing.T) {
	dir := initTempRepo(t)
	chdir(t, dir)
	// Empty repo has no HEAD until first commit — make one so
	// branch name resolves cleanly.
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, args := range [][]string{{"add", "x.txt"}, {"commit", "-q", "-m", "init"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	got, err := GetCurrentBranch()
	if err != nil {
		t.Fatalf("GetCurrentBranch: %v", err)
	}
	if got != "main" {
		t.Fatalf("branch = %q, want main", got)
	}
}

func TestGetRepoName_ExtractsFromRemoteURL(t *testing.T) {
	dir := initTempRepo(t)
	chdir(t, dir)
	if out, err := exec.Command("git", "remote", "add", "origin", "git@github.com:acme/widgets.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}
	got, err := GetRepoName()
	if err != nil {
		t.Fatalf("GetRepoName: %v", err)
	}
	if got != "widgets" {
		t.Fatalf("repo = %q, want widgets", got)
	}
}

func TestGetRepoName_NoRemoteErrors(t *testing.T) {
	dir := initTempRepo(t)
	chdir(t, dir)
	_, err := GetRepoName()
	if err == nil {
		t.Fatal("expected error when no remote configured")
	}
}

func TestCommit_CreatesCommit(t *testing.T) {
	dir := initTempRepo(t)
	chdir(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("git", "add", "f.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if err := Commit("feat: initial"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	out, err := exec.Command("git", "log", "-1", "--pretty=%s").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if !strings.Contains(string(out), "feat: initial") {
		t.Fatalf("commit subject not found: %q", out)
	}
}
