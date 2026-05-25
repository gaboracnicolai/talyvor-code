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
	out, err := c.CompleteWithUsage(ctx, messages, model, feature, workspaceID, issueID)
	if err != nil {
		return "", err
	}
	return out.Text, nil
}

// Usage tracks the token counts and estimated USD cost for a
// single Lens round-trip. MCP clients use this to report cost
// attribution back to their parent agent.
type Usage struct {
	Text         string
	InputTokens  int
	OutputTokens int
	CostUSD      float64
}

// CompleteWithUsage is the richer sibling to Complete — same
// inputs, but returns input/output token counts and an estimated
// dollar cost. Estimation uses a small hard-coded model price
// table; the actual reconciled cost still lives in Lens analytics.
func (c *Client) CompleteWithUsage(ctx context.Context, messages []Message, model, feature, workspaceID, issueID string) (*Usage, error) {
	if !c.IsConfigured() {
		return nil, errors.New("lens: not configured")
	}
	body := map[string]any{
		"model":      model,
		"max_tokens": 2048,
		"messages":   messages,
	}
	enc, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.url+"/v1/proxy/anthropic/v1/messages", bytes.NewReader(enc))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("X-Talyvor-Feature", "code-"+feature)
	req.Header.Set("X-Talyvor-Workspace", workspaceID)
	req.Header.Set("X-Talyvor-Issue", issueID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("lens: %s", resp.Status)
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return &Usage{
		Text:         b.String(),
		InputTokens:  out.Usage.InputTokens,
		OutputTokens: out.Usage.OutputTokens,
		CostUSD:      EstimateCostUSD(model, out.Usage.InputTokens, out.Usage.OutputTokens),
	}, nil
}

// EstimateCostUSD prices a call from per-million-token rates.
// Keep the table tight; Lens does authoritative reconciliation.
func EstimateCostUSD(model string, inputTokens, outputTokens int) float64 {
	var inRate, outRate float64
	switch model {
	case "claude-sonnet-4-6":
		inRate, outRate = 3.0, 15.0
	case "claude-opus-4-7":
		inRate, outRate = 15.0, 75.0
	default:
		// claude-haiku-4-6 and unknown models fall back to haiku rates.
		inRate, outRate = 0.25, 1.25
	}
	return (float64(inputTokens)*inRate + float64(outputTokens)*outRate) / 1_000_000
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
