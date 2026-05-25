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

// ─── branch/remote helpers ─────────────────────────

func TestCreateBranch_AndBranchExists(t *testing.T) {
	dir := initTempRepo(t)
	chdir(t, dir)
	// Need an initial commit before a branch can be created.
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, args := range [][]string{{"add", "f.txt"}, {"commit", "-q", "-m", "init"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	exists, err := BranchExists("feature/foo")
	if err != nil {
		t.Fatalf("BranchExists: %v", err)
	}
	if exists {
		t.Fatal("branch should not exist yet")
	}
	if err := CreateBranch("feature/foo"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	exists, err = BranchExists("feature/foo")
	if err != nil {
		t.Fatalf("BranchExists 2: %v", err)
	}
	if !exists {
		t.Fatal("branch should exist after CreateBranch")
	}
	// Re-creating must fail rather than silently switch.
	if err := CreateBranch("feature/foo"); err == nil {
		t.Fatal("expected error re-creating existing branch")
	}
}

func TestGetDefaultBranch_ResolvesFromHeadOrFallback(t *testing.T) {
	dir := initTempRepo(t)
	chdir(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, args := range [][]string{{"add", "f.txt"}, {"commit", "-q", "-m", "init"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// No remote configured → falls back to "main" (the repo was
	// initialised with -b main in initTempRepo).
	got, err := GetDefaultBranch()
	if err != nil {
		t.Fatalf("GetDefaultBranch: %v", err)
	}
	if got != "main" {
		t.Fatalf("default = %q, want main", got)
	}
}

func TestGetRemoteURL_ReturnsConfiguredOrigin(t *testing.T) {
	dir := initTempRepo(t)
	chdir(t, dir)
	if out, err := exec.Command("git", "remote", "add", "origin", "git@github.com:acme/widgets.git").CombinedOutput(); err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}
	url, err := GetRemoteURL()
	if err != nil {
		t.Fatalf("GetRemoteURL: %v", err)
	}
	if !strings.Contains(url, "acme/widgets") {
		t.Fatalf("url = %q", url)
	}
}

func TestIsGitHub(t *testing.T) {
	cases := map[string]bool{
		"git@github.com:acme/widgets.git":            true,
		"https://github.com/acme/widgets.git":        true,
		"https://github.com/acme/widgets":            true,
		"git@gitlab.com:acme/widgets.git":            false,
		"https://gitea.example.com/acme/widgets.git": false,
		"":                                           false,
	}
	for url, want := range cases {
		if got := IsGitHub(url); got != want {
			t.Errorf("IsGitHub(%q) = %v, want %v", url, got, want)
		}
	}
}

func TestAddAll_StagesEverything(t *testing.T) {
	dir := initTempRepo(t)
	chdir(t, dir)
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := AddAll(); err != nil {
		t.Fatalf("AddAll: %v", err)
	}
	out, _ := exec.Command("git", "diff", "--staged", "--name-only").CombinedOutput()
	staged := string(out)
	if !strings.Contains(staged, "a.txt") || !strings.Contains(staged, "b.txt") {
		t.Fatalf("expected both files staged: %q", staged)
	}
}

func TestGetDiffStats_SummarisesChanges(t *testing.T) {
	dir := initTempRepo(t)
	chdir(t, dir)
	// Need one commit so HEAD resolves.
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, args := range [][]string{{"add", "f.txt"}, {"commit", "-q", "-m", "init"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// Now make a change and stage it.
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a\nb\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if out, err := exec.Command("git", "add", "f.txt").CombinedOutput(); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	stats, err := GetDiffStats()
	if err != nil {
		t.Fatalf("GetDiffStats: %v", err)
	}
	if !strings.Contains(stats, "f.txt") {
		t.Fatalf("stats should mention f.txt: %q", stats)
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
