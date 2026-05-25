// Package github is the small slice of the GitHub REST API
// Talyvor Code's CLI needs: parse the origin URL into owner/repo
// and POST a pull request. We use the stdlib HTTP client rather
// than pull in google/go-github — the surface is tiny and the
// dep-tree hygiene matters for a CLI install.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// defaultBase is the GitHub REST API origin. Overridable via
// createPRWithBase for tests; callers go through CreatePR.
const defaultBase = "https://api.github.com"

// PRConfig is the request shape callers fill out. Draft maps to
// the GitHub `draft` flag; an empty Body still produces a valid
// PR (GitHub renders "(no description)").
type PRConfig struct {
	Owner string
	Repo  string
	Title string
	Body  string
	Head  string
	Base  string
	Draft bool
}

// PRResult mirrors the slice of the GitHub response we surface
// to callers — the number, the HTML URL, the title (in case it
// was sanitised server-side), and the state.
type PRResult struct {
	Number int    `json:"number"`
	URL    string `json:"html_url"`
	Title  string `json:"title"`
	State  string `json:"state"`
}

var (
	// httpsRepo matches `https://github.com/owner/repo[.git]`.
	httpsRepo = regexp.MustCompile(`^https?://github\.com/([^/]+)/([^/]+?)(?:\.git)?/?$`)
	// sshRepo matches `git@github.com:owner/repo[.git]`.
	sshRepo = regexp.MustCompile(`^git@github\.com:([^/]+)/([^/]+?)(?:\.git)?$`)
)

// ParseRepoFromURL extracts owner + repo from a GitHub remote.
// Accepts both HTTPS and SSH shapes (with or without `.git`).
// Returns an error for non-GitHub remotes — callers should skip
// the PR flow when this fires.
func ParseRepoFromURL(remoteURL string) (string, string, error) {
	url := strings.TrimSpace(remoteURL)
	if url == "" {
		return "", "", errors.New("github: remote URL is empty")
	}
	if m := httpsRepo.FindStringSubmatch(url); len(m) == 3 {
		return m[1], m[2], nil
	}
	if m := sshRepo.FindStringSubmatch(url); len(m) == 3 {
		return m[1], m[2], nil
	}
	return "", "", fmt.Errorf("github: not a GitHub repository URL (%q)", url)
}

// MaxBranchSlugLen caps branch names at 50 chars so the GitHub
// branch picker stays readable. Beyond that we lose the prefix
// hint anyway because the UI elides the tail.
const MaxBranchSlugLen = 50

var (
	// branchScrub strips anything that isn't alphanumeric, dot,
	// dash, or underscore — the safe set GitHub accepts in branch
	// names without quoting.
	branchScrub = regexp.MustCompile(`[^a-z0-9\-_.]+`)
	// branchSqueeze collapses runs of dashes into a single dash so
	// "add  --  jwt" doesn't slug to "add----jwt".
	branchSqueeze = regexp.MustCompile(`-+`)
)

// SlugifyBranch turns a free-form task description into a
// conventional-commits-style branch name. Picks "fix/" when the
// description leans bug-fix, "feat/" otherwise; caps at
// MaxBranchSlugLen so we don't push branches GitHub will truncate
// in its UI. Empty input returns a timestamped fallback so the
// caller never has to special-case it.
func SlugifyBranch(description string) string {
	desc := strings.ToLower(strings.TrimSpace(description))
	if desc == "" {
		return fmt.Sprintf("feat/talyvor-%d", time.Now().Unix())
	}
	prefix := "feat/"
	for _, kw := range []string{"fix", "bug", "patch", "hotfix", "resolve"} {
		// Single-word boundary check — "fixed" / "fixing" still
		// trigger but "prefix" doesn't.
		if strings.HasPrefix(desc, kw) || strings.Contains(" "+desc+" ", " "+kw+" ") {
			prefix = "fix/"
			break
		}
	}
	clean := branchScrub.ReplaceAllString(desc, "-")
	clean = branchSqueeze.ReplaceAllString(clean, "-")
	clean = strings.Trim(clean, "-_.")
	if clean == "" {
		clean = fmt.Sprintf("talyvor-%d", time.Now().Unix())
	}
	out := prefix + clean
	if len(out) > MaxBranchSlugLen {
		out = strings.TrimRight(out[:MaxBranchSlugLen], "-_.")
	}
	return out
}

// GeneratePRBody renders the canonical PR description. The
// "AI Cost Attribution" footer is the moat — every Talyvor-
// opened PR carries provable spend attached to a Track issue.
func GeneratePRBody(
	issueID, issueTitle, taskDesc string,
	changedFiles []string,
	costUSD float64,
) string {
	var b strings.Builder
	b.WriteString("## Summary\n\n")
	if issueID != "" {
		title := issueTitle
		if title == "" {
			title = "(no title)"
		}
		fmt.Fprintf(&b, "Implements: %s — %s\n\n", issueID, title)
	}
	if strings.TrimSpace(taskDesc) != "" {
		b.WriteString(strings.TrimSpace(taskDesc))
		b.WriteString("\n\n")
	}
	if len(changedFiles) > 0 {
		b.WriteString("## Changes\n\n")
		for _, f := range changedFiles {
			fmt.Fprintf(&b, "- `%s`\n", f)
		}
		b.WriteString("\n")
	}
	b.WriteString("## AI Cost Attribution\n\n")
	b.WriteString("This PR was implemented with Talyvor Code.\n")
	if issueID != "" {
		fmt.Fprintf(&b, "AI cost: $%.2f (attributed to %s)\n", costUSD, issueID)
	} else {
		fmt.Fprintf(&b, "AI cost: $%.2f\n", costUSD)
	}
	b.WriteString("\n---\n*Opened by [Talyvor Code](https://github.com/gaboracnicolai/talyvor-code)*\n")
	return b.String()
}

// CreatePR posts a pull request via the GitHub REST API.
// Returns the parsed PRResult or a non-nil error wrapping the
// HTTP status + response body for diagnostics.
func CreatePR(ctx context.Context, token string, cfg PRConfig) (*PRResult, error) {
	return createPRWithBase(ctx, defaultBase, token, cfg)
}

// createPRWithBase is the testable seam — same as CreatePR but
// the API base URL is injectable so httptest servers can stand
// in for github.com.
func createPRWithBase(ctx context.Context, base, token string, cfg PRConfig) (*PRResult, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("github: GITHUB_TOKEN required for PR creation")
	}
	if cfg.Owner == "" || cfg.Repo == "" || cfg.Head == "" || cfg.Base == "" {
		return nil, errors.New("github: owner/repo/head/base are required")
	}
	body, err := json.Marshal(map[string]any{
		"title": cfg.Title,
		"body":  cfg.Body,
		"head":  cfg.Head,
		"base":  cfg.Base,
		"draft": cfg.Draft,
	})
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/repos/%s/%s/pulls",
		strings.TrimRight(base, "/"), cfg.Owner, cfg.Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("github: %d %s: %s",
			resp.StatusCode, resp.Status, strings.TrimSpace(string(respBody)))
	}
	var out PRResult
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("github: decode response: %w", err)
	}
	return &out, nil
}
