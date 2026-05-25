// Package track is the Go Track client used by the CLI agent. Lean
// surface — only what the agent needs to resolve an issue
// identifier (ENG-42) into a lookup the user can confirm.
package track

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Issue struct {
	ID          string  `json:"id"`
	Identifier  string  `json:"identifier"`
	Title       string  `json:"title"`
	Status      string  `json:"status"`
	Description string  `json:"description"`
	AICostUSD   float64 `json:"ai_cost_usd"`
}

type Client struct {
	url        string
	apiKey     string
	httpClient *http.Client
}

func New(url, apiKey string) *Client {
	return &Client{
		url:        strings.TrimRight(url, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 8 * time.Second},
	}
}

func (c *Client) IsConfigured() bool {
	return c.url != "" && c.apiKey != ""
}

// GetIssue returns nil (no error) when Track is unconfigured so
// callers can treat the lookup as best-effort. Genuine HTTP
// failures surface as an error so the user knows to investigate.
func (c *Client) GetIssue(ctx context.Context, workspaceID, identifier string) (*Issue, error) {
	if !c.IsConfigured() {
		return nil, nil
	}
	if workspaceID == "" || identifier == "" {
		return nil, errors.New("track: workspace_id and identifier required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.url+"/v1/workspaces/"+url.PathEscape(workspaceID)+"/issues/"+url.PathEscape(identifier), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 400 {
		return nil, errors.New("track: " + resp.Status)
	}
	var out Issue
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AddComment posts a comment back to a Track issue. Used by the
// agent CLI to record "agent task completed" notes against the
// active issue so the trail of automated changes is visible in
// Track alongside the human discussion. Unconfigured Track is a
// no-op — best-effort attribution should never block the CLI.
func (c *Client) AddComment(ctx context.Context, workspaceID, issueID, comment string) error {
	if !c.IsConfigured() {
		return nil
	}
	if workspaceID == "" || issueID == "" {
		return errors.New("track: workspace_id and issue_id required")
	}
	body, err := json.Marshal(map[string]string{
		"content":   comment,
		"author_id": "talyvor-agent",
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.url+"/v1/workspaces/"+url.PathEscape(workspaceID)+"/issues/"+url.PathEscape(issueID)+"/comments",
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return errors.New("track: " + resp.Status)
	}
	return nil
}

// ListIssues returns recent issues for the workspace, capped at
// limit. Used by the QuickPick command to populate suggestions
// when the user hasn't typed enough characters for a search.
func (c *Client) ListIssues(ctx context.Context, workspaceID string, limit int) ([]Issue, error) {
	if !c.IsConfigured() {
		return nil, nil
	}
	if workspaceID == "" {
		return nil, errors.New("track: workspace_id required")
	}
	if limit <= 0 {
		limit = 25
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.url+"/v1/workspaces/"+url.PathEscape(workspaceID)+"/issues?limit="+strconv.Itoa(limit), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, errors.New("track: " + resp.Status)
	}
	var out []Issue
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}
