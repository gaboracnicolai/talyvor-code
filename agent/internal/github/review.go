package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// GetOpenPR looks up the open PR matching `owner:branch`. Returns
// the PR number when exactly one is found, or an error otherwise
// — callers should treat "no open PR" as "skip GitHub posting".
func GetOpenPR(ctx context.Context, token, owner, repo, branch string) (int, error) {
	return getOpenPRWithBase(ctx, defaultBase, token, owner, repo, branch)
}

func getOpenPRWithBase(ctx context.Context, base, token, owner, repo, branch string) (int, error) {
	if strings.TrimSpace(token) == "" {
		return 0, errors.New("github: GITHUB_TOKEN required to look up PRs")
	}
	if owner == "" || repo == "" || branch == "" {
		return 0, errors.New("github: owner/repo/branch are required")
	}
	q := url.Values{}
	q.Set("state", "open")
	q.Set("head", owner+":"+branch)
	endpoint := fmt.Sprintf("%s/repos/%s/%s/pulls?%s",
		strings.TrimRight(base, "/"), owner, repo, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	setGitHubHeaders(req, token)
	resp, err := githubClient().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("github: %d %s: %s", resp.StatusCode, resp.Status, strings.TrimSpace(string(body)))
	}
	var list []struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return 0, err
	}
	if len(list) == 0 {
		return 0, fmt.Errorf("github: no open PR for %s:%s", owner, branch)
	}
	return list[0].Number, nil
}

// PostPRReview posts a review body to the PR with event=COMMENT
// (a passive note, not approve/request-changes). The terminal
// CLI is the wrong place to land binding review actions; comments
// keep the human in the loop.
func PostPRReview(ctx context.Context, token, owner, repo string, prNumber int, body string) error {
	return postPRReviewWithBase(ctx, defaultBase, token, owner, repo, prNumber, body)
}

func postPRReviewWithBase(ctx context.Context, base, token, owner, repo string, prNumber int, body string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("github: GITHUB_TOKEN required to post PR reviews")
	}
	if owner == "" || repo == "" || prNumber == 0 {
		return errors.New("github: owner/repo/prNumber are required")
	}
	payload, err := json.Marshal(map[string]any{
		"body":  body,
		"event": "COMMENT",
	})
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/reviews",
		strings.TrimRight(base, "/"), owner, repo, prNumber)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	setGitHubHeaders(req, token)
	resp, err := githubClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("github: %d %s: %s", resp.StatusCode, resp.Status, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// GetPRFiles lists the files changed in PR #prNumber. Used by
// the review flow to surface a per-file summary alongside the
// diff body.
func GetPRFiles(ctx context.Context, token, owner, repo string, prNumber int) ([]string, error) {
	return getPRFilesWithBase(ctx, defaultBase, token, owner, repo, prNumber)
}

func getPRFilesWithBase(ctx context.Context, base, token, owner, repo string, prNumber int) ([]string, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("github: GITHUB_TOKEN required to list PR files")
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/files",
		strings.TrimRight(base, "/"), owner, repo, prNumber)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	setGitHubHeaders(req, token)
	resp, err := githubClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("github: %d %s: %s", resp.StatusCode, resp.Status, strings.TrimSpace(string(body)))
	}
	var list []struct {
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(list))
	for _, f := range list {
		if f.Filename != "" {
			out = append(out, f.Filename)
		}
	}
	return out, nil
}

// ExtractVerdict scans a review body for the verdict line. We
// look for the three canonical labels in priority order
// (REQUEST CHANGES wins over APPROVE when both appear — a real
// blocker beats a polite "but otherwise approved"). Default
// when none match: NEEDS DISCUSSION, the safe non-action.
func ExtractVerdict(review string) string {
	upper := strings.ToUpper(review)
	if strings.Contains(upper, "REQUEST CHANGES") {
		return "REQUEST CHANGES"
	}
	if strings.Contains(upper, "APPROVE") {
		return "APPROVE"
	}
	if strings.Contains(upper, "NEEDS DISCUSSION") {
		return "NEEDS DISCUSSION"
	}
	return "NEEDS DISCUSSION"
}

// CountFindings walks the review body and reports counts under
// the Critical and Warning headings. Recognises the canonical
// "🔴 Critical" / "🟡 Warning" markers — anything inside those
// sections that looks like a bullet point counts as one finding;
// "None" / empty sections count as zero.
//
// The leading-dash regex tolerates the emojis the model often
// adds at the start of a bullet ("- 🔴 SQL injection in …").
var bulletStart = regexp.MustCompile(`^\s*([-*]|\d+\.)\s+`)

func CountFindings(review string) (critical, warning int) {
	section := ""
	for _, raw := range strings.Split(review, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		// Treat any heading line as a section transition.
		if strings.HasPrefix(line, "#") {
			lower := strings.ToLower(line)
			switch {
			case strings.Contains(lower, "critical"):
				section = "critical"
			case strings.Contains(lower, "warning"):
				section = "warning"
			default:
				section = ""
			}
			continue
		}
		if section == "" {
			continue
		}
		if !bulletStart.MatchString(line) {
			continue
		}
		body := bulletStart.ReplaceAllString(line, "")
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		// Strip a leading emoji + space so "🔴 issue" doesn't
		// look like the literal "None" label.
		if i := strings.Index(body, " "); i > 0 && i <= 4 {
			tail := strings.TrimSpace(body[i+1:])
			if tail != "" {
				body = tail
			}
		}
		lower := strings.ToLower(body)
		if lower == "none" || lower == "none." {
			continue
		}
		if section == "critical" {
			critical++
		} else {
			warning++
		}
	}
	return critical, warning
}

// setGitHubHeaders applies the canonical header set documented
// in https://docs.github.com/rest. Centralised here so a future
// auth/version bump only touches one place.
func setGitHubHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
}

// githubClient returns a sensibly-tuned HTTP client. 15s is
// plenty for the REST endpoints we hit; longer waits indicate a
// real problem worth surfacing.
func githubClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second}
}
