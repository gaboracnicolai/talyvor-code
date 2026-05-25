// Package git wraps the small set of `git` shell calls the CLI
// agent needs: staged-diff capture for commit/review, branch +
// remote lookups for context, and commit/push for the commit
// subcommand. All wrappers use exec.Command — never a shell
// string — so file paths and user-supplied messages can't
// expand into command injection.
package git

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// GetStagedDiff returns the unified diff for whatever is in the
// index. Empty string + nil error when there's nothing staged in
// a valid repo; non-nil error when not inside a git repo at all.
func GetStagedDiff() (string, error) {
	out, err := run("git", "diff", "--staged")
	if err != nil {
		return "", err
	}
	return out, nil
}

// GetCurrentBranch returns the symbolic ref of HEAD (e.g. "main",
// "feature/foo"). On a detached HEAD git prints "HEAD" — we
// pass that through unchanged.
func GetCurrentBranch() (string, error) {
	out, err := run("git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// GetRepoName extracts the last path segment from origin's URL,
// dropping any .git suffix. Returns an error if origin is not
// configured (no point guessing in that case).
func GetRepoName() (string, error) {
	out, err := run("git", "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	url := strings.TrimSpace(out)
	if url == "" {
		return "", errors.New("git: origin remote is empty")
	}
	// Trim trailing .git, then take the last path component
	// after either : (ssh) or / (https/ssh).
	trimmed := strings.TrimSuffix(url, ".git")
	for _, sep := range []string{"/", ":"} {
		if i := strings.LastIndex(trimmed, sep); i >= 0 {
			trimmed = trimmed[i+1:]
			break
		}
	}
	if trimmed == "" {
		return "", fmt.Errorf("git: cannot parse repo name from %q", url)
	}
	return trimmed, nil
}

// Commit creates a commit with the supplied message. The message
// is passed via -m so it never expands through a shell.
func Commit(message string) error {
	if strings.TrimSpace(message) == "" {
		return errors.New("git: commit message cannot be empty")
	}
	_, err := run("git", "commit", "-m", message)
	return err
}

// Push runs `git push` against the current upstream. The CLI
// surfaces the user's git config (e.g. push.default = simple)
// rather than trying to second-guess it.
func Push() error {
	_, err := run("git", "push")
	return err
}

// CreateBranch creates `name` and checks it out. Errors when the
// branch already exists (git's own behaviour) so callers can
// guard against accidentally clobbering a feature branch.
func CreateBranch(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("git: branch name cannot be empty")
	}
	_, err := run("git", "checkout", "-b", name)
	return err
}

// BranchExists checks whether `name` exists locally. Pure
// readonly call — no side effects.
func BranchExists(name string) (bool, error) {
	if strings.TrimSpace(name) == "" {
		return false, nil
	}
	out, err := run("git", "branch", "--list", name)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// GetDefaultBranch resolves the repo's default branch. Tries the
// remote HEAD pointer first, then falls back to common names
// (`main` then `master`). Returns an error only if no candidate
// can be resolved.
func GetDefaultBranch() (string, error) {
	if out, err := run("git", "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		ref := strings.TrimSpace(out)
		// Format: "refs/remotes/origin/main" → take the tail.
		if i := strings.LastIndex(ref, "/"); i >= 0 {
			tail := ref[i+1:]
			if tail != "" {
				return tail, nil
			}
		}
	}
	// Fallback: probe well-known names locally.
	for _, name := range []string{"main", "master"} {
		if ok, _ := BranchExists(name); ok {
			return name, nil
		}
	}
	return "", errors.New("git: cannot resolve default branch (no origin HEAD, no local main/master)")
}

// GetRemoteURL returns the origin URL. Errors when origin isn't
// configured — there's nothing useful to guess.
func GetRemoteURL() (string, error) {
	out, err := run("git", "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	url := strings.TrimSpace(out)
	if url == "" {
		return "", errors.New("git: origin remote is empty")
	}
	return url, nil
}

// IsGitHub answers the cheap question "should I bother running
// the PR creation flow at all?". It's a substring check, not a
// strict parser — GitHub Enterprise hosts (github.acme.com) get
// false here on purpose; we'd need a per-host configuration to
// support them properly.
func IsGitHub(remoteURL string) bool {
	return strings.Contains(remoteURL, "github.com")
}

// AddAll stages every change in the working tree. Used by the
// `pr` and `run --pr` flows so the agent's writes plus any
// pre-existing edits land in one commit.
func AddAll() error {
	_, err := run("git", "add", "-A")
	return err
}

// GetDiffStats returns the `git diff --stat HEAD` output. Useful
// for "X files changed, Y insertions(+), Z deletions(-)" headers
// in PR descriptions and confirmation prompts.
func GetDiffStats() (string, error) {
	return run("git", "diff", "--stat", "HEAD")
}

// PushBranch pushes the named branch and sets upstream. Honours
// the user's git config (push.default, etc.) — we don't pass
// any flags beyond what's strictly needed.
func PushBranch(branch string) error {
	if strings.TrimSpace(branch) == "" {
		return errors.New("git: branch name cannot be empty")
	}
	_, err := run("git", "push", "-u", "origin", branch)
	return err
}

// run executes a command and returns its combined stdout/stderr.
// On non-zero exit it wraps the stderr text so the caller can
// surface a useful error to the user.
func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
