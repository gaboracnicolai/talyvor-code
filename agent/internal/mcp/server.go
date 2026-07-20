// Package mcp exposes the Talyvor Code MCP server. Mirrors the
// pattern used by Talyvor Lens/Track/Docs: JSON-RPC 2.0 over
// HTTP at /mcp, SSE pings at /mcp/sse. Ten tools cover the
// coding-context surface — codebase inspection, file reads,
// AI-routed Q&A, code review, commit-message generation, docs
// search, and active-issue lookup.
//
// Tools degrade gracefully when their backing service isn't
// configured (Track unset → get_active_issue returns
// {configured: false}). The codebase index is refreshed every 60
// seconds by a background goroutine so long-running MCP clients
// see fresh paths after the user adds files.
package mcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/talyvor/code/internal/codebase"
	"github.com/talyvor/code/internal/config"
	"github.com/talyvor/code/internal/docs"
	gitpkg "github.com/talyvor/code/internal/git"
	"github.com/talyvor/code/internal/lens"
	modelpkg "github.com/talyvor/code/internal/model"
	"github.com/talyvor/code/internal/track"
)

const (
	protocolVersion = "2024-11-05"
	serverName      = "talyvor-code"
	ssePingInterval = 30 * time.Second
	reindexInterval = 60 * time.Second

	rpcErrParse        = -32700
	rpcErrInvalidReq   = -32600
	rpcErrMethodNotFnd = -32601
	rpcErrInvalidParam = -32602
	rpcErrInternal     = -32603
)

// Server wires together the four backing clients and a
// continuously-refreshed codebase index. Pass nil for any
// optional client (Track, Docs) and the relevant tools will
// surface a "not configured" payload rather than failing.
type Server struct {
	lensClient  *lens.Client
	trackClient *track.Client
	docsClient  *docs.Client
	config      *config.Config
	version     string
	root        string
	authToken   string

	mu          sync.RWMutex
	index       *codebase.CodebaseIndex
	indexedAt   time.Time
	reindexStop chan struct{}
}

// New constructs a Server but does not start any background work.
// Call StartReindex to spin up the periodic refresh goroutine.
func New(
	lensClient *lens.Client,
	trackClient *track.Client,
	docsClient *docs.Client,
	cfg *config.Config,
	version string,
) *Server {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &Server{
		lensClient:  lensClient,
		trackClient: trackClient,
		docsClient:  docsClient,
		config:      cfg,
		version:     version,
	}
}

// SetRoot configures the codebase root that indexing scans.
// Defaults to "." when unset.
func (s *Server) SetRoot(root string) {
	s.root = root
}

// SetAuthToken sets the bearer token every request must present.
// The `serve` startup path always supplies a non-empty token
// (env or generated), so an empty token here is a misconfiguration
// — authOK fails closed in that case rather than waving requests
// through.
func (s *Server) SetAuthToken(token string) {
	s.authToken = token
}

// GenerateToken returns a 32-byte cryptographically random token
// as a 64-char hex string. Used by `serve` when TALYVOR_MCP_TOKEN
// is unset so the server is never reachable without a secret.
func GenerateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mcp: generate token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// ResolveServeToken decides the MCP bearer token for a bind. An explicit env token is honoured on any
// host (the operator chose it and can rotate it by unset/reset + restart). With no explicit token, a
// LOOPBACK bind auto-generates one for convenience, but a NON-LOOPBACK bind is REFUSED — we never
// expose an ephemeral, printed-once secret to the network; a LAN operator must set TALYVOR_MCP_TOKEN so
// the token is deliberate and rotatable. (There is intentionally no live rotation: the token is
// memory-only and a restart rotates it; a rotate endpoint would be new attack surface on a single-user
// local server.)
func ResolveServeToken(envToken, host string) (token string, generated bool, err error) {
	if t := strings.TrimSpace(envToken); t != "" {
		return t, false, nil
	}
	if !IsLoopbackHost(host) {
		return "", false, fmt.Errorf("refusing to bind MCP server to non-loopback host %q without an explicit TALYVOR_MCP_TOKEN "+
			"(an auto-generated token must not be exposed to the network; set TALYVOR_MCP_TOKEN so it is operator-chosen and rotatable)", host)
	}
	t, gerr := GenerateToken()
	if gerr != nil {
		return "", false, gerr
	}
	return t, true, nil
}

// IsLoopbackHost reports whether host is a loopback interface.
// `serve` warns loudly when binding anywhere else, since a
// non-loopback bind exposes the server to other machines (still
// token-gated, but worth a klaxon).
func IsLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// IndexNow blocks while a fresh codebase index is built. Used by
// the `serve` startup path so the first MCP call sees a populated
// summary.
func (s *Server) IndexNow() error {
	root := s.root
	if root == "" {
		root = "."
	}
	idx, err := codebase.IndexDirectory(root, codebase.DefaultMaxFiles)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.index = idx
	s.indexedAt = time.Now()
	s.mu.Unlock()
	return nil
}

// CurrentIndex returns the most recent codebase snapshot. May be
// nil before the first IndexNow completes.
func (s *Server) CurrentIndex() *codebase.CodebaseIndex {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.index
}

// StartReindex spins up the periodic re-index goroutine. Safe to
// call from main. Returns immediately; Stop() halts the loop.
func (s *Server) StartReindex(parent context.Context) {
	s.mu.Lock()
	if s.reindexStop != nil {
		s.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	s.reindexStop = stop
	s.mu.Unlock()

	go func() {
		ticker := time.NewTicker(reindexInterval)
		defer ticker.Stop()
		for {
			select {
			case <-parent.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				_ = s.IndexNow()
			}
		}
	}()
}

// Stop halts the background re-index loop. Idempotent.
func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reindexStop == nil {
		return
	}
	close(s.reindexStop)
	s.reindexStop = nil
}

// Routes attaches the MCP handlers to the supplied mux.
func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/mcp", s.withAuth(s.HandleRPC))
	mux.HandleFunc("/mcp/sse", s.withAuth(s.HandleSSE))
}

// withAuth gates a handler behind the bearer-token check. Both
// the JSON-RPC and SSE endpoints are wrapped — the SSE handler is
// a separate code path and must not be left open. On failure we
// answer 401 and never invoke the inner handler (so a rejected SSE
// request never gets the 200 + endpoint event).
func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authOK(r) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// authOK reports whether the request carries the configured bearer
// token. Fails closed: an unset server token rejects everything
// (avoids the subtle.ConstantTimeCompare("","")==1 footgun), and a
// missing/short Authorization header never matches. The compare is
// constant-time to deny timing oracles, mirroring Track's
// gatewayauth.
func (s *Server) authOK(r *http.Request) bool {
	want := s.authToken
	if want == "" {
		return false
	}
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return false
	}
	got := strings.TrimPrefix(h, prefix)
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// ─── JSON-RPC envelopes ─────────────────────────────

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) writeRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	resp := rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	resp := rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleRPC dispatches one JSON-RPC request. MCP clients open
// this endpoint per call; long-lived streaming sits at /mcp/sse.
func (s *Server) HandleRPC(w http.ResponseWriter, r *http.Request) {
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeRPCError(w, nil, rpcErrParse, "Parse error")
		return
	}
	switch req.Method {
	case "initialize":
		s.writeRPCResult(w, req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{
				"name":    serverName,
				"version": s.version,
			},
		})
	case "tools/list":
		s.writeRPCResult(w, req.ID, map[string]any{"tools": toolDefinitions()})
	case "tools/call":
		s.handleToolsCall(w, r.Context(), req.ID, req.Params)
	default:
		s.writeRPCError(w, req.ID, rpcErrMethodNotFnd, "method not found: "+req.Method)
	}
}

// HandleSSE keeps an SSE connection open and pings every 30s.
// The initial `endpoint` event tells the client where to send
// JSON-RPC.
func (s *Server) HandleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	_, _ = fmt.Fprintf(w, "event: endpoint\ndata: {\"uri\":\"/mcp\"}\n\n")
	if flusher != nil {
		flusher.Flush()
	}
	ticker := time.NewTicker(ssePingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			_, _ = fmt.Fprintf(w, "event: ping\ndata: {}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

// ─── Tool catalogue ─────────────────────────────────

func toolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":        "ask_code",
			"description": "Ask an AI question about the codebase. Reads specified files (or discovers relevant ones) and routes the question through Talyvor Lens with cost attribution to the supplied issue.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"question": map[string]any{"type": "string"},
					"files":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"issue_id": map[string]any{"type": "string"},
				},
				"required": []string{"question"},
			},
		},
		{
			"name":        "generate_tests",
			"description": "Generate unit tests for a source file. Returns the test code, a suggested output path, and the estimated AI cost.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file":      map[string]any{"type": "string"},
					"framework": map[string]any{"type": "string"},
					"issue_id":  map[string]any{"type": "string"},
				},
				"required": []string{"file"},
			},
		},
		{
			"name":        "review_code",
			"description": "Review one or more files for bugs, security issues, and performance problems. Returns a markdown review plus counts of critical/warning findings.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"files":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"review_type": map[string]any{"type": "string", "enum": []string{"general", "security", "performance"}, "default": "general"},
					"issue_id":    map[string]any{"type": "string"},
				},
				"required": []string{"files"},
			},
		},
		{
			"name":        "get_active_issue",
			"description": "Fetch the currently active Track issue. Returns its identifier, title, status, description, and AI cost rollup.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workspace_id": map[string]any{"type": "string"},
				},
				"required": []string{"workspace_id"},
			},
		},
		{
			"name":        "search_codebase",
			"description": "Search the indexed codebase by path/filename substrings. Returns the top matches with their language and a relevance score.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
					"limit": map[string]any{"type": "integer", "default": 10},
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "read_file",
			"description": "Read a file's contents. Optional 'lines' range (e.g. '10-50') limits output. Capped at 100KB; longer files are truncated with a marker.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":  map[string]any{"type": "string"},
					"lines": map[string]any{"type": "string"},
				},
				"required": []string{"path"},
			},
		},
		{
			"name":        "search_docs",
			"description": "Search Talyvor Docs (full-text + semantic) for pages relevant to the query. Returns the top matches with title, space, excerpt, and URL.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":        map[string]any{"type": "string"},
					"workspace_id": map[string]any{"type": "string"},
				},
				"required": []string{"query", "workspace_id"},
			},
		},
		{
			"name":        "ask_docs",
			"description": "Ask a natural-language question grounded in Talyvor Docs. Returns the answer plus the source pages used to derive it.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"question":     map[string]any{"type": "string"},
					"workspace_id": map[string]any{"type": "string"},
				},
				"required": []string{"question", "workspace_id"},
			},
		},
		{
			"name":        "get_codebase_summary",
			"description": "Return the indexed codebase summary: languages by file count, total files/lines, git branch, and repo name.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"root": map[string]any{"type": "string"},
				},
				"required": []string{},
			},
		},
		{
			"name":        "generate_commit_message",
			"description": "Generate a conventional-commits message from the staged git diff. Optional issue_id is prepended (e.g. 'ENG-42: …'). Optional staged_diff override skips the local git call.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"staged_diff": map[string]any{"type": "string"},
					"issue_id":    map[string]any{"type": "string"},
				},
				"required": []string{},
			},
		},
	}
}

// ─── tools/call dispatch ────────────────────────────

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) handleToolsCall(w http.ResponseWriter, ctx context.Context, id, raw json.RawMessage) {
	var p toolCallParams
	if err := json.Unmarshal(raw, &p); err != nil {
		s.writeRPCError(w, id, rpcErrInvalidParam, "invalid tools/call params")
		return
	}
	result, code, errMsg := s.dispatchTool(ctx, p.Name, p.Arguments)
	if code != 0 {
		s.writeRPCError(w, id, code, errMsg)
		return
	}
	// MCP tool results follow the {content:[{type:"text",text:json}]} shape.
	body, err := json.Marshal(result)
	if err != nil {
		s.writeRPCError(w, id, rpcErrInternal, "marshal result: "+err.Error())
		return
	}
	s.writeRPCResult(w, id, map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(body)}},
	})
}

func (s *Server) dispatchTool(ctx context.Context, name string, args json.RawMessage) (any, int, string) {
	switch name {
	case "ask_code":
		return s.toolAskCode(ctx, args)
	case "generate_tests":
		return s.toolGenerateTests(ctx, args)
	case "review_code":
		return s.toolReviewCode(ctx, args)
	case "get_active_issue":
		return s.toolGetActiveIssue(ctx, args)
	case "search_codebase":
		return s.toolSearchCodebase(ctx, args)
	case "read_file":
		return s.toolReadFile(args)
	case "search_docs":
		return s.toolSearchDocs(ctx, args)
	case "ask_docs":
		return s.toolAskDocs(ctx, args)
	case "get_codebase_summary":
		return s.toolGetCodebaseSummary(args)
	case "generate_commit_message":
		return s.toolGenerateCommitMessage(ctx, args)
	}
	return nil, rpcErrMethodNotFnd, "unknown tool: " + name
}

// ─── Tools ──────────────────────────────────────────

type askCodeArgs struct {
	Question string   `json:"question"`
	Files    []string `json:"files"`
	IssueID  string   `json:"issue_id"`
}

func (s *Server) toolAskCode(ctx context.Context, raw json.RawMessage) (any, int, string) {
	var a askCodeArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, rpcErrInvalidParam, "invalid arguments"
	}
	if strings.TrimSpace(a.Question) == "" {
		return nil, rpcErrInvalidParam, "question is required"
	}
	if s.lensClient == nil || !s.lensClient.IsConfigured() {
		return map[string]any{"configured": false, "reason": "lens not configured"}, 0, ""
	}
	files := a.Files
	if len(files) == 0 {
		// Discover grounding files from the SEMANTIC index (per-query embed + cosine),
		// replacing the old path-substring guess. No index → no auto-grounding (the
		// answer proceeds ungrounded rather than on a fabricated match).
		if ret, _, ok := s.semanticRetriever(); ok {
			if hits, herr := ret.Retrieve(ctx, a.Question, 5); herr == nil {
				seen := map[string]bool{}
				for _, h := range hits {
					if seen[h.File] {
						continue
					}
					seen[h.File] = true
					files = append(files, filepath.Join(s.rootOrDot(), h.File))
				}
			}
		}
	}
	fileCtx := ""
	if len(files) > 0 {
		out, err := codebase.ReadFilesForContext(files, codebase.DefaultMaxTotalBytes)
		if err == nil {
			fileCtx = out
		}
	}
	prompt := "You are an expert software engineer answering a question about a codebase. Be concise and ground your answer in the supplied files.\n\nQuestion: " + a.Question
	if fileCtx != "" {
		prompt += "\n\nFiles:\n" + fileCtx
	}
	usage, err := s.lensClient.CompleteWithUsage(ctx,
		[]lens.Message{{Role: "user", Content: prompt}},
		modelpkg.DefaultForCommand("chat"), "mcp-ask-code", s.config.WorkspaceID, a.IssueID,
	)
	if err != nil {
		return nil, rpcErrInternal, "lens: " + err.Error()
	}
	return map[string]any{
		"answer":     usage.Text,
		"files_used": files,
		"cost_usd":   usage.CostUSD,
	}, 0, ""
}

type generateTestsArgs struct {
	File      string `json:"file"`
	Framework string `json:"framework"`
	IssueID   string `json:"issue_id"`
}

func (s *Server) toolGenerateTests(ctx context.Context, raw json.RawMessage) (any, int, string) {
	var a generateTestsArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, rpcErrInvalidParam, "invalid arguments"
	}
	if strings.TrimSpace(a.File) == "" {
		return nil, rpcErrInvalidParam, "file is required"
	}
	if s.lensClient == nil || !s.lensClient.IsConfigured() {
		return map[string]any{"configured": false, "reason": "lens not configured"}, 0, ""
	}
	body, err := codebase.ReadFile(a.File, codebase.DefaultMaxFileBytes)
	if err != nil {
		return nil, rpcErrInvalidParam, "read source: " + err.Error()
	}
	lang := codebase.DetectLanguage(a.File)
	framework := a.Framework
	if framework == "" {
		framework = defaultFramework(lang)
	}
	prompt := fmt.Sprintf(
		"You are an expert test engineer. Generate %s tests using %s for the following %s file. Return ONLY the test code — no prose, no markdown fences.\n\n=== %s ===\n%s",
		lang, framework, lang, a.File, body,
	)
	usage, err := s.lensClient.CompleteWithUsage(ctx,
		[]lens.Message{{Role: "user", Content: prompt}},
		modelpkg.DefaultForCommand("test"), "mcp-generate-tests", s.config.WorkspaceID, a.IssueID,
	)
	if err != nil {
		return nil, rpcErrInternal, "lens: " + err.Error()
	}
	return map[string]any{
		"tests":       usage.Text,
		"output_file": suggestTestOutputPath(a.File, lang),
		"cost_usd":    usage.CostUSD,
	}, 0, ""
}

type reviewCodeArgs struct {
	Files      []string `json:"files"`
	ReviewType string   `json:"review_type"`
	IssueID    string   `json:"issue_id"`
}

func (s *Server) toolReviewCode(ctx context.Context, raw json.RawMessage) (any, int, string) {
	var a reviewCodeArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, rpcErrInvalidParam, "invalid arguments"
	}
	if len(a.Files) == 0 {
		return nil, rpcErrInvalidParam, "files is required"
	}
	if s.lensClient == nil || !s.lensClient.IsConfigured() {
		return map[string]any{"configured": false, "reason": "lens not configured"}, 0, ""
	}
	body, _ := codebase.ReadFilesForContext(a.Files, codebase.DefaultMaxTotalBytes)
	prompt := mcpReviewPrompt(a.ReviewType) + "\n\nReview this code:\n\n" + body
	usage, err := s.lensClient.CompleteWithUsage(ctx,
		[]lens.Message{{Role: "user", Content: prompt}},
		modelpkg.DefaultForCommand("review"), "mcp-review", s.config.WorkspaceID, a.IssueID,
	)
	if err != nil {
		return nil, rpcErrInternal, "lens: " + err.Error()
	}
	critical, warning := countReviewMarkers(usage.Text)
	return map[string]any{
		"review":         usage.Text,
		"critical_count": critical,
		"warning_count":  warning,
		"cost_usd":       usage.CostUSD,
	}, 0, ""
}

func mcpReviewPrompt(kind string) string {
	focus := "Bugs and logic errors, security vulnerabilities, performance issues, code quality, and maintainability."
	switch strings.ToLower(kind) {
	case "security":
		focus = "Authentication/authorization gaps, input validation, injection (SQL/command/template), unsafe deserialization, secret handling, CSRF/XSS, dependency CVEs, and data leakage."
	case "performance":
		focus = "Algorithmic complexity, N+1 queries, memory allocations on hot paths, blocking I/O, lock contention, and unnecessary computation in render paths."
	}
	return "You are an expert code reviewer. Focus on: " + focus + "\n\n" +
		"Format your response as Markdown with these sections:\n" +
		"## Summary\n## Issues Found\n### Critical\n### Warnings\n### Suggestions\n## Overall Assessment"
}

// countReviewMarkers walks the review body and counts items in the
// Critical/Warnings sections. A bullet point under those headings
// counts as one finding; empty sections (e.g. "None.") count as 0.
func countReviewMarkers(md string) (critical, warning int) {
	section := ""
	for _, raw := range strings.Split(md, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "### Critical") {
			section = "critical"
			continue
		}
		if strings.HasPrefix(line, "### Warning") {
			section = "warning"
			continue
		}
		if strings.HasPrefix(line, "### ") || strings.HasPrefix(line, "## ") {
			section = ""
			continue
		}
		if section == "" {
			continue
		}
		if !strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "*") && !strings.HasPrefix(line, "1.") {
			continue
		}
		body := strings.TrimSpace(strings.TrimLeft(line, "-*0123456789. "))
		if body == "" || strings.EqualFold(body, "none") || strings.EqualFold(body, "none.") {
			continue
		}
		if section == "critical" {
			critical++
		} else if section == "warning" {
			warning++
		}
	}
	return critical, warning
}

type getActiveIssueArgs struct {
	WorkspaceID string `json:"workspace_id"`
}

func (s *Server) toolGetActiveIssue(ctx context.Context, raw json.RawMessage) (any, int, string) {
	var a getActiveIssueArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, rpcErrInvalidParam, "invalid arguments"
	}
	if a.WorkspaceID == "" {
		return nil, rpcErrInvalidParam, "workspace_id is required"
	}
	if s.trackClient == nil || !s.trackClient.IsConfigured() {
		return map[string]any{"configured": false, "reason": "track not configured"}, 0, ""
	}
	if s.config.ActiveIssue == "" {
		return map[string]any{"configured": true, "issue_id": "", "identifier": "", "title": "", "status": "", "description": "", "ai_cost_usd": 0.0}, 0, ""
	}
	issue, err := s.trackClient.GetIssue(ctx, a.WorkspaceID, s.config.ActiveIssue)
	if err != nil {
		return nil, rpcErrInternal, "track: " + err.Error()
	}
	if issue == nil {
		return map[string]any{"configured": true, "issue_id": s.config.ActiveIssue, "identifier": s.config.ActiveIssue, "title": "", "status": "", "description": "", "ai_cost_usd": 0.0}, 0, ""
	}
	return map[string]any{
		"configured":  true,
		"issue_id":    issue.ID,
		"identifier":  issue.Identifier,
		"title":       issue.Title,
		"status":      issue.Status,
		"description": issue.Description,
		"ai_cost_usd": issue.AICostUSD,
	}, 0, ""
}

// rootOrDot is the workspace root, defaulting to "." when SetRoot was never called.
func (s *Server) rootOrDot() string {
	if s.root == "" {
		return "."
	}
	return s.root
}

// mcpEmbedder adapts the server's Lens client to codebase.Embedder for per-query
// embedding — the SAME trust boundary as chat/ask (only the query text is sent to
// Lens; nothing new leaves the machine). It mirrors cmd/agent's lensEmbedder; the two
// live in different packages and can't share a private adapter, so this keeps the MCP
// rewire self-contained.
type mcpEmbedder struct {
	lc          *lens.Client
	workspaceID string
}

func (e mcpEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return e.lc.Embed(ctx, texts, codebase.DefaultEmbedModel, "embed", e.workspaceID, "")
}

// semanticRetriever loads the persisted semantic index (built by `talyvor-code index`,
// a LOCAL file under the root) and binds it to a Lens-backed query embedder — the same
// real cosine retriever chat/ask/agent use. Returns ok=false when no usable index is
// present, so callers fail HONESTLY ("run `talyvor-code index`") instead of fabricating
// a relevance score.
func (s *Server) semanticRetriever() (codebase.Retriever, *codebase.SemanticIndex, bool) {
	sem, err := codebase.LoadIndex(codebase.IndexPath(s.rootOrDot()))
	if err != nil || sem == nil || len(sem.Chunks) == 0 {
		return nil, nil, false
	}
	return codebase.BoundIndex{Index: sem, Emb: mcpEmbedder{lc: s.lensClient, workspaceID: s.config.WorkspaceID}}, sem, true
}

type searchCodebaseArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// toolSearchCodebase ranks the codebase against a query using the REAL semantic index
// (per-query embedding + cosine similarity) — the same relevance source chat/agent use.
// It replaces the old path-substring FindRelevantFiles + fabricated linear score: with
// no index it fails honestly, and every score returned is a true cosine, never invented.
func (s *Server) toolSearchCodebase(ctx context.Context, raw json.RawMessage) (any, int, string) {
	var a searchCodebaseArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, rpcErrInvalidParam, "invalid arguments"
	}
	if strings.TrimSpace(a.Query) == "" {
		return nil, rpcErrInvalidParam, "query is required"
	}
	if s.lensClient == nil || !s.lensClient.IsConfigured() {
		return map[string]any{"configured": false, "reason": "lens not configured — semantic search needs query embeddings"}, 0, ""
	}
	ret, sem, ok := s.semanticRetriever()
	if !ok {
		return map[string]any{
			"indexed": false,
			"reason":  "no semantic index found — run `talyvor-code index` first",
		}, 0, ""
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 10
	}
	hits, err := ret.Retrieve(ctx, a.Query, limit)
	if err != nil {
		return nil, rpcErrInternal, "search: " + err.Error()
	}
	results := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		results = append(results, map[string]any{
			"path":       h.File,
			"language":   h.Language,
			"start_line": h.StartLine,
			"end_line":   h.EndLine,
			"score":      h.Score, // true cosine similarity, not a fabricated rank
		})
	}
	return map[string]any{"results": results, "chunks_indexed": len(sem.Chunks)}, 0, ""
}

type readFileArgs struct {
	Path  string `json:"path"`
	Lines string `json:"lines"`
}

// confinedReadPath enforces that an MCP file read stays within the configured workspace root (s.root).
// S11: read_file / ask_code / generate_tests / review_code took a raw caller path straight to os.Open,
// letting a token-holding client read any file the process could (../../.ssh/id_rsa, .env, .git/config).
// When no root is configured (s.root == "") there is no workspace boundary to enforce and the path is
// returned as-is; the serve command always SetRoot()s, so production reads are always confined.
func (s *Server) confinedReadPath(p string) (string, error) {
	if s.root == "" {
		return p, nil
	}
	rootAbs, err := filepath.Abs(s.root)
	if err != nil {
		return "", err
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(rootAbs, abs)
	}
	abs = filepath.Clean(abs)
	rel, err := filepath.Rel(rootAbs, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("refusing to read outside workspace root")
	}
	return abs, nil
}

func (s *Server) toolReadFile(raw json.RawMessage) (any, int, string) {
	var a readFileArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, rpcErrInvalidParam, "invalid arguments"
	}
	if strings.TrimSpace(a.Path) == "" {
		return nil, rpcErrInvalidParam, "path is required"
	}
	safe, err := s.confinedReadPath(a.Path)
	if err != nil {
		return nil, rpcErrInvalidParam, "read: path outside workspace"
	}
	body, err := codebase.ReadFile(safe, codebase.DefaultMaxFileBytes)
	if err != nil {
		return nil, rpcErrInvalidParam, "read: " + err.Error()
	}
	if a.Lines != "" {
		start, end, ok := parseLinesRange(a.Lines)
		if ok {
			body = sliceLines(body, start, end)
		}
	}
	lang := codebase.DetectLanguage(a.Path)
	lineCount := strings.Count(body, "\n")
	if !strings.HasSuffix(body, "\n") && body != "" {
		lineCount++
	}
	return map[string]any{
		"content":  body,
		"language": lang,
		"lines":    lineCount,
	}, 0, ""
}

// parseLinesRange accepts "10-50" or "10". Returns 1-indexed
// inclusive bounds.
func parseLinesRange(s string) (int, int, bool) {
	parts := strings.SplitN(s, "-", 2)
	a, err1 := atoi(parts[0])
	if err1 != nil || a < 1 {
		return 0, 0, false
	}
	if len(parts) == 1 {
		return a, a, true
	}
	b, err2 := atoi(parts[1])
	if err2 != nil || b < a {
		return 0, 0, false
	}
	return a, b, true
}

func atoi(s string) (int, error) {
	n := 0
	for _, c := range strings.TrimSpace(s) {
		if c < '0' || c > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func sliceLines(s string, start, end int) string {
	lines := strings.Split(s, "\n")
	if start > len(lines) {
		return ""
	}
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start-1:end], "\n")
}

type searchDocsArgs struct {
	Query       string `json:"query"`
	WorkspaceID string `json:"workspace_id"`
}

func (s *Server) toolSearchDocs(ctx context.Context, raw json.RawMessage) (any, int, string) {
	var a searchDocsArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, rpcErrInvalidParam, "invalid arguments"
	}
	if a.Query == "" || a.WorkspaceID == "" {
		return nil, rpcErrInvalidParam, "query and workspace_id are required"
	}
	if s.docsClient == nil || !s.docsClient.IsConfigured() {
		return map[string]any{"configured": false, "reason": "docs not configured"}, 0, ""
	}
	results, err := s.docsClient.Search(ctx, a.WorkspaceID, a.Query, 10)
	if err != nil {
		return nil, rpcErrInternal, "docs: " + err.Error()
	}
	out := make([]map[string]any, 0, len(results))
	for _, r := range results {
		out = append(out, map[string]any{
			"title":   r.PageTitle,
			"space":   r.SpaceName,
			"excerpt": r.Headline,
			"url":     r.URL,
		})
	}
	return map[string]any{"results": out}, 0, ""
}

type askDocsArgs struct {
	Question    string `json:"question"`
	WorkspaceID string `json:"workspace_id"`
}

func (s *Server) toolAskDocs(ctx context.Context, raw json.RawMessage) (any, int, string) {
	var a askDocsArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, rpcErrInvalidParam, "invalid arguments"
	}
	if a.Question == "" || a.WorkspaceID == "" {
		return nil, rpcErrInvalidParam, "question and workspace_id are required"
	}
	if s.docsClient == nil || !s.docsClient.IsConfigured() {
		return map[string]any{"configured": false, "reason": "docs not configured"}, 0, ""
	}
	res, err := s.docsClient.AskDocs(ctx, a.WorkspaceID, a.Question)
	if err != nil {
		return nil, rpcErrInternal, "docs: " + err.Error()
	}
	sources := make([]map[string]any, 0)
	if res != nil {
		for _, src := range res.Sources {
			sources = append(sources, map[string]any{"title": src.Title, "url": src.URL})
		}
	}
	answer := ""
	if res != nil {
		answer = res.Answer
	}
	return map[string]any{"answer": answer, "sources": sources}, 0, ""
}

type codebaseSummaryArgs struct {
	Root string `json:"root"`
}

func (s *Server) toolGetCodebaseSummary(raw json.RawMessage) (any, int, string) {
	var a codebaseSummaryArgs
	if len(raw) > 0 {
		// arguments are optional
		_ = json.Unmarshal(raw, &a)
	}
	idx := s.CurrentIndex()
	if a.Root != "" || idx == nil {
		root := a.Root
		if root == "" {
			root = s.root
		}
		if root == "" {
			root = "."
		}
		fresh, err := codebase.IndexDirectory(root, codebase.DefaultMaxFiles)
		if err != nil {
			return nil, rpcErrInternal, "index: " + err.Error()
		}
		idx = fresh
	}
	return map[string]any{
		"summary":     idx.Summary(),
		"languages":   idx.Languages,
		"total_files": len(idx.Files),
		"total_lines": idx.TotalLines,
		"git_branch":  idx.GitBranch,
		"git_repo":    idx.GitRepo,
	}, 0, ""
}

type genCommitArgs struct {
	StagedDiff string `json:"staged_diff"`
	IssueID    string `json:"issue_id"`
}

func (s *Server) toolGenerateCommitMessage(ctx context.Context, raw json.RawMessage) (any, int, string) {
	var a genCommitArgs
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &a)
	}
	if s.lensClient == nil || !s.lensClient.IsConfigured() {
		return map[string]any{"configured": false, "reason": "lens not configured"}, 0, ""
	}
	diff := a.StagedDiff
	if strings.TrimSpace(diff) == "" {
		got, err := gitpkg.GetStagedDiff()
		if err != nil {
			return nil, rpcErrInvalidParam, "git: " + err.Error()
		}
		diff = got
	}
	if strings.TrimSpace(diff) == "" {
		return nil, rpcErrInvalidParam, "no staged changes"
	}
	prompt := "Generate a concise git commit message. Follow the conventional-commits format:\n" +
		"<type>(<scope>): <description>\n\n" +
		"Types: feat, fix, docs, refactor, test, chore. Keep the subject under 72 characters. " +
		"Return ONLY the commit message — no explanation, no markdown fences, no quotes.\n\n" +
		"=== staged diff ===\n" + diff
	usage, err := s.lensClient.CompleteWithUsage(ctx,
		[]lens.Message{{Role: "user", Content: prompt}},
		modelpkg.DefaultForCommand("commit"), "mcp-commit", s.config.WorkspaceID, a.IssueID,
	)
	if err != nil {
		return nil, rpcErrInternal, "lens: " + err.Error()
	}
	msg := strings.TrimSpace(usage.Text)
	msg = strings.TrimPrefix(msg, "```")
	msg = strings.TrimSuffix(msg, "```")
	msg = strings.TrimSpace(msg)
	if a.IssueID != "" {
		msg = a.IssueID + ": " + msg
	}
	return map[string]any{
		"message":  msg,
		"cost_usd": usage.CostUSD,
	}, 0, ""
}

// ─── helpers ───────────────────────────────────────

func defaultFramework(lang string) string {
	switch lang {
	case "Go":
		return "Go testing"
	case "TypeScript", "JavaScript":
		return "Jest"
	case "Python":
		return "pytest"
	case "Java":
		return "JUnit"
	case "Ruby":
		return "RSpec"
	case "Rust":
		return "Rust testing"
	case "Swift":
		return "XCTest"
	}
	return "Generic"
}

// suggestTestOutputPath proposes a sibling file for the generated
// tests. Mirrors the test-generator pure helper used by the
// extension so users see consistent suggestions.
func suggestTestOutputPath(path, lang string) string {
	dir, base := filepath.Split(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	switch lang {
	case "Go":
		return dir + stem + "_test" + ext
	case "Python":
		return dir + "test_" + stem + ext
	case "Ruby":
		return dir + stem + "_spec" + ext
	case "Java", "Swift":
		return dir + stem + "Tests" + ext
	}
	return dir + stem + ".test" + ext
}

// runGit is a thin helper used by tests + tools that need a raw
// git output line rather than the wrappers in internal/git.
func runGit(args ...string) (string, error) {
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return string(bytes.TrimSpace(out)), nil
}

// Compile-time guard: the unused symbols above are still expected
// to compile cleanly under go vet.
var _ = runGit
