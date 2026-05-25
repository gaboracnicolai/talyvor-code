// Package lens is the Go Lens client used by the CLI agent. Every
// Complete call carries the X-Talyvor-Issue header so the active
// issue gets credited with the spend.
package lens

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) IsConfigured() bool {
	return c.url != "" && c.apiKey != ""
}

// Complete proxies a chat completion through Lens. `feature` is
// prefixed with `code-` server-side so Lens dashboards bucket
// the spend per IDE affordance.
func (c *Client) Complete(ctx context.Context, messages []Message, model, feature, workspaceID, issueID string) (string, error) {
	if !c.IsConfigured() {
		return "", errors.New("lens: not configured")
	}
	body := map[string]any{
		"model":      model,
		"max_tokens": 2048,
		"messages":   messages,
	}
	enc, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.url+"/v1/proxy/anthropic/v1/messages", bytes.NewReader(enc))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("X-Talyvor-Feature", "code-"+feature)
	req.Header.Set("X-Talyvor-Workspace", workspaceID)
	req.Header.Set("X-Talyvor-Issue", issueID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("lens: %s", resp.Status)
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String(), nil
}

// Status pings /healthz so the CLI's "check" command can report a
// fast yes/no without paying for a real completion.
func (c *Client) Status(ctx context.Context) (bool, error) {
	if c.url == "" {
		return false, errors.New("lens: url empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"/healthz", nil)
	if err != nil {
		return false, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200, nil
}
