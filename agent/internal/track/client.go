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

	"github.com/talyvor/code/internal/safeurl"
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
	if err := safeurl.Validate("track-url", url); err != nil {
		return nil, err
	}
	return &Client{
		url:        strings.TrimRight(url, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 8 * time.Second},
	}, nil
}

func (c *Client) IsConfigured() bool {
	return c.url != "" && c.apiKey != ""
}

// GetIssue returns nil (no error) when Track is unconfigured so
// callers can treat the lookup as best-effort. Genuine HTTP
// failures surface as an error so the user knows to investigate.
//
// ⚠ IT ASKS TWO ROUTES BECAUSE TRACK HAS TWO AND THEY ARE DISJOINT. `ref` is whatever the caller
// resolved — cfg.ActiveIssue, which internal/issueref recovers FROM A GIT BRANCH NAME, so in
// practice it is a human key ("ENG-42") and only rarely a row id. Track serves
// `/issues/{id}` off the primary key and `/issues/by-identifier/{identifier}` off
// `WHERE identifier = $1 AND workspace_id = $2`; neither answers for the other's input. Before
// this, only the first was ever asked, so every branch-derived lookup 404ed on an issue that
// exists — and because a 404 is the best-effort "no issue" answer, it did so SILENTLY.
// talyvor-track added by-identifier for exactly this client and said so in its handler:
// "the CLI agent's Track client put an identifier in the {id} slot below and got 404 for issues
// that exist — measured on the wire, talyvor-code #57". Its half shipped; this half did not.
//
// ⚠ THE ORDER IS WHAT MAKES THIS BEHAVIOUR-PRESERVING RATHER THAN A POLICY CHOICE. `{id}` is
// tried FIRST, exactly as before, and by-identifier only after a 404 — so every input that
// resolved before resolves the same way, in the same request, to the same issue. This can only
// turn 404s into 200s. Asking by-identifier first would decide which route wins for a string
// that could be both, and that is not a decision this needs to take.
func (c *Client) GetIssue(ctx context.Context, workspaceID, ref string) (*Issue, error) {
	if !c.IsConfigured() {
		return nil, nil
	}
	if workspaceID == "" || ref == "" {
		return nil, errors.New("track: workspace_id and identifier required")
	}
	base := c.url + "/v1/workspaces/" + url.PathEscape(workspaceID) + "/issues/"
	out, err := c.getIssueAt(ctx, base+url.PathEscape(ref))
	if err != nil || out != nil {
		return out, err
	}
	return c.getIssueAt(ctx, base+"by-identifier/"+url.PathEscape(ref))
}

// getIssueAt performs one lookup: (nil, nil) on 404, an error on any other >= 400.
func (c *Client) getIssueAt(ctx context.Context, endpoint string) (*Issue, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
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
//
// ⚠ THE FIELD IS `body`, NOT `content`, AND IT IS NOT A STYLE CHOICE. Track decodes into
// model.Comment (`Body string \`json:"body"\“) with DisallowUnknownFields, so any other key
// is a 400 rather than a silently dropped field: this call posted `content` and could never
// succeed — measured against a real server, 400 `json: unknown field "content"`, and 201 with
// `body`. The tests next door had always passed because their fake unmarshalled into a struct
// the test itself declared; wire_contract_track_test.go is now as strict as Track is.
//
// ⚠ NO author_id IS SENT, DELIBERATELY. Track overwrites it with the verified session member
// (internal/issue/handler.go, SEC-5: "a supplied author_id is ignored, so no caller can
// attribute a comment to another member"). This code used to send "talyvor-agent" and a comment
// above it claimed that made automated changes visible as such; it never did — the measured
// response carried the human member's id. Sending a field the server discards is a claim the
// wire does not support. Distinguishing agent comments needs a Track-side concept of a
// non-human author, which is not this repository's to invent.
func (c *Client) AddComment(ctx context.Context, workspaceID, issueID, comment string) error {
	if !c.IsConfigured() {
		return nil
	}
	if workspaceID == "" || issueID == "" {
		return errors.New("track: workspace_id and issue_id required")
	}
	body, err := json.Marshal(map[string]string{"body": comment})
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

// TeamKeys returns the team identifiers ("ENG", "OPS") the workspace's Track builds issue
// identifiers from — GET /v1/workspaces/{ws}/teams, which answers a JSON array of teams. The CLI
// uses them to decide whether a branch's <key>-<n> is one of this workspace's issues at all.
func (c *Client) TeamKeys(ctx context.Context, workspaceID string) ([]string, error) {
	if !c.IsConfigured() {
		return nil, nil
	}
	if workspaceID == "" {
		return nil, errors.New("track: workspace_id required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.url+"/v1/workspaces/"+url.PathEscape(workspaceID)+"/teams", nil)
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
	var teams []struct {
		Identifier string `json:"identifier"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&teams); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(teams))
	for _, t := range teams {
		if t.Identifier != "" {
			keys = append(keys, t.Identifier)
		}
	}
	return keys, nil
}
