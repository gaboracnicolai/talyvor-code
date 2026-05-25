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
