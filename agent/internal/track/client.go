// Package track is the Go Track client used by the CLI agent. Lean
// surface — only what the agent needs to resolve an issue
// identifier (ENG-42) into a lookup the user can confirm.
package track

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
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
