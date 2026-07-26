// Package docs is the Talyvor Docs HTTP client used by the CLI
// agent. Three operations: full-text/semantic search, ask-the-
// docs Q&A, and a per-page fetch. Lean surface — anything richer
// stays in Docs' own UI.
package docs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/talyvor/code/internal/safeurl"
	"time"
)

type Client struct {
	url        string
	apiKey     string
	httpClient *http.Client
}

// New builds a client, REFUSING any base URL that would leak the attached API key.
//
// The validation is part of construction, not a step a caller must remember. It used to
// live behind Config.Validate(), which eleven subcommands called and two did not — so
// `serve` and `init` sent the key in cleartext to whatever host was configured. There is
// deliberately no exported constructor that skips this: a new subcommand cannot
// reintroduce the leak by forgetting a call, because the only way to get a *Client is
// through a function that has already checked.
//
// An empty url is allowed and yields an unconfigured client (IsConfigured() == false) —
// Track and Docs are optional integrations.
func New(url, apiKey string) (*Client, error) {
	if err := safeurl.Validate("docs-url", url); err != nil {
		return nil, err
	}
	return &Client{
		url:        strings.TrimRight(url, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (c *Client) IsConfigured() bool {
	return c.url != "" && c.apiKey != ""
}

// SearchResult mirrors the docs/search Result shape so callers
// can render rank-coloured hits without reshaping the payload.
type SearchResult struct {
	PageID     string  `json:"page_id"`
	PageTitle  string  `json:"page_title"`
	SpaceName  string  `json:"space_name"`
	Headline   string  `json:"headline"`
	Rank       float64 `json:"rank,omitempty"`
	Similarity float64 `json:"similarity,omitempty"`
	Source     string  `json:"source"`
	URL        string  `json:"url"`
}

type searchEnvelope struct {
	Results []SearchResult `json:"results"`
	Total   int            `json:"total"`
}

// Search runs the docs full-text/semantic hybrid search. Returns
// an empty slice — not an error — when Docs is unconfigured so
// callers can degrade gracefully.
func (c *Client) Search(ctx context.Context, workspaceID, query string, limit int) ([]SearchResult, error) {
	if !c.IsConfigured() {
		return nil, nil
	}
	if workspaceID == "" {
		return nil, errors.New("docs: workspace_id required")
	}
	if limit <= 0 {
		limit = 5
	}
	q := url.Values{}
	q.Set("q", query)
	q.Set("limit", strconv.Itoa(limit))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.url+"/v1/workspaces/"+url.PathEscape(workspaceID)+"/search?"+q.Encode(), nil)
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
		return nil, errors.New("docs: " + resp.Status)
	}
	var env searchEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, err
	}
	return env.Results, nil
}

type AskSource struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type AskResult struct {
	Answer  string      `json:"answer"`
	Sources []AskSource `json:"sources"`
}

// AskDocs posts a natural-language question to the docs Q&A
// endpoint. Returns nil (no error) when Docs is unconfigured.
func (c *Client) AskDocs(ctx context.Context, workspaceID, question string) (*AskResult, error) {
	if !c.IsConfigured() {
		return nil, nil
	}
	if workspaceID == "" {
		return nil, errors.New("docs: workspace_id required")
	}
	body, err := json.Marshal(map[string]string{"question": question})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.url+"/v1/workspaces/"+url.PathEscape(workspaceID)+"/ai/ask",
		bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, errors.New("docs: " + resp.Status)
	}
	var out AskResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Page mirrors the docs Page shape with the fields the agent
// actually renders. Many more exist on the server side; we keep
// the slim view so the CLI doesn't need to track every column.
type Page struct {
	ID              string  `json:"id"`
	SpaceID         string  `json:"space_id"`
	Title           string  `json:"title"`
	ContentText     string  `json:"content_text"`
	AICostUSD       float64 `json:"ai_cost_usd"`
	LastVerifiedAt  string  `json:"last_verified_at"`
	UpdatedAt       string  `json:"updated_at"`
	FreshnessStatus string  `json:"freshness_status"`
}

// GetPage fetches a single page. The Docs server requires the
// space ID alongside the page ID — search results carry both, so
// callers chain Search → GetPage with the SpaceName/SpaceID they
// already have.
func (c *Client) GetPage(ctx context.Context, spaceID, pageID string) (*Page, error) {
	if !c.IsConfigured() {
		return nil, nil
	}
	if spaceID == "" || pageID == "" {
		return nil, errors.New("docs: space_id and page_id required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.url+"/v1/spaces/"+url.PathEscape(spaceID)+"/pages/"+url.PathEscape(pageID), nil)
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
		return nil, errors.New("docs: " + resp.Status)
	}
	var out Page
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}
