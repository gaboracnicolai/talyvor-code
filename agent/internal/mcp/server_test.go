package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/talyvor/code/internal/config"
	"github.com/talyvor/code/internal/docs"
	"github.com/talyvor/code/internal/lens"
	"github.com/talyvor/code/internal/track"
)

// callRPC posts a single JSON-RPC call and returns the parsed
// response envelope.
func callRPC(t *testing.T, srv *httptest.Server, body string) rpcResponse {
	t.Helper()
	resp, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	var out rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

// toolText unwraps the {content:[{type:text,text:json}]} shape
// the dispatch returns. Returns the inner JSON string.
func toolText(t *testing.T, resp rpcResponse) string {
	t.Helper()
	m, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result not a map: %v", resp.Result)
	}
	content, _ := m["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("no content: %v", m)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	return text
}

// newServerForTest wires a Server with a fake Lens server and
// an empty config. Callers can pass nil clients for Track/Docs
// to exercise the unconfigured paths.
func newServerForTest(t *testing.T, lensSrv *httptest.Server, trackC *track.Client, docsC *docs.Client) (*Server, *httptest.Server) {
	t.Helper()
	cfg := &config.Config{WorkspaceID: "ws-1"}
	var lc *lens.Client
	if lensSrv != nil {
		lc = lens.New(lensSrv.URL, "tlv_k")
	}
	s := New(lc, trackC, docsC, cfg, "test-0.1")
	mux := http.NewServeMux()
	s.Routes(mux)
	httpSrv := httptest.NewServer(mux)
	t.Cleanup(func() {
		httpSrv.Close()
		s.Stop()
	})
	return s, httpSrv
}

func fakeLens(t *testing.T, replies []string) *httptest.Server {
	t.Helper()
	idx := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if idx >= len(replies) {
			t.Fatalf("fake lens: unexpected extra request (idx=%d)", idx)
		}
		body := replies[idx]
		idx++
		encoded, _ := json.Marshal(body)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":` + string(encoded) + `}],"usage":{"input_tokens":120,"output_tokens":40}}`))
		_ = r
	}))
}

// ─── protocol ──────────────────────────────────────

func TestInitialize_ReturnsProtocolVersion(t *testing.T) {
	_, srv := newServerForTest(t, nil, nil, nil)
	resp := callRPC(t, srv, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}
	m := resp.Result.(map[string]any)
	if m["protocolVersion"] != "2024-11-05" {
		t.Fatalf("protocol version = %v", m["protocolVersion"])
	}
	info := m["serverInfo"].(map[string]any)
	if info["name"] != "talyvor-code" {
		t.Fatalf("server name = %v", info["name"])
	}
}

func TestToolsList_ReturnsAllTenTools(t *testing.T) {
	_, srv := newServerForTest(t, nil, nil, nil)
	resp := callRPC(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}
	tools := resp.Result.(map[string]any)["tools"].([]any)
	if len(tools) != 10 {
		t.Fatalf("expected 10 tools, got %d", len(tools))
	}
	names := map[string]bool{}
	for _, raw := range tools {
		t2 := raw.(map[string]any)
		names[t2["name"].(string)] = true
	}
	for _, want := range []string{
		"ask_code", "generate_tests", "review_code", "get_active_issue",
		"search_codebase", "read_file", "search_docs", "ask_docs",
		"get_codebase_summary", "generate_commit_message",
	} {
		if !names[want] {
			t.Errorf("missing tool: %s", want)
		}
	}
}

func TestUnknownMethod_ReturnsMethodNotFound(t *testing.T) {
	_, srv := newServerForTest(t, nil, nil, nil)
	resp := callRPC(t, srv, `{"jsonrpc":"2.0","id":1,"method":"completely_unknown"}`)
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("expected -32601, got %+v", resp.Error)
	}
}

func TestUnknownTool_ReturnsMethodNotFoundInsideToolsCall(t *testing.T) {
	_, srv := newServerForTest(t, nil, nil, nil)
	resp := callRPC(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"made_up","arguments":{}}}`)
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("expected -32601 for unknown tool, got %+v", resp.Error)
	}
}

func TestMissingRequiredParam_ReturnsInvalidParam(t *testing.T) {
	_, srv := newServerForTest(t, nil, nil, nil)
	// search_codebase requires `query`.
	resp := callRPC(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_codebase","arguments":{}}}`)
	if resp.Error == nil || resp.Error.Code != -32602 {
		t.Fatalf("expected -32602, got %+v", resp.Error)
	}
}

// ─── ask_code ──────────────────────────────────────

func TestAskCode_CallsLensWithFileContext(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "auth.go")
	if err := os.WriteFile(srcPath, []byte("package auth\n\nfunc Verify() {}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var gotBody map[string]any
	lensSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &gotBody)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"It verifies a JWT."}],"usage":{"input_tokens":200,"output_tokens":15}}`))
	}))
	defer lensSrv.Close()

	_, srv := newServerForTest(t, lensSrv, nil, nil)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ask_code","arguments":{"question":"What does Verify do?","files":["` + srcPath + `"],"issue_id":"ENG-42"}}}`
	resp := callRPC(t, srv, body)
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}
	text := toolText(t, resp)
	var got map[string]any
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	if !strings.Contains(got["answer"].(string), "JWT") {
		t.Fatalf("answer missing: %v", got["answer"])
	}
	if got["cost_usd"].(float64) <= 0 {
		t.Fatalf("cost_usd should be > 0, got %v", got["cost_usd"])
	}
	// Confirm the file content actually made it into the Lens body.
	msgs := gotBody["messages"].([]any)
	first := msgs[0].(map[string]any)
	if !strings.Contains(first["content"].(string), "package auth") {
		t.Fatalf("file content not in prompt: %v", first["content"])
	}
}

func TestAskCode_UnconfiguredLensReturnsConfiguredFalse(t *testing.T) {
	_, srv := newServerForTest(t, nil, nil, nil)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ask_code","arguments":{"question":"anything"}}}`
	resp := callRPC(t, srv, body)
	if resp.Error != nil {
		t.Fatalf("expected no error, got %+v", resp.Error)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(toolText(t, resp)), &got)
	if got["configured"] != false {
		t.Fatalf("expected configured=false, got %+v", got)
	}
}

// ─── get_codebase_summary ──────────────────────────

func TestGetCodebaseSummary_ReturnsIndex(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "b.ts"), []byte("export {};\n"), 0o644)

	s, srv := newServerForTest(t, nil, nil, nil)
	s.SetRoot(dir)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_codebase_summary","arguments":{}}}`
	resp := callRPC(t, srv, body)
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(toolText(t, resp)), &got)
	if got["total_files"].(float64) != 2 {
		t.Fatalf("total_files = %v, want 2", got["total_files"])
	}
	if !strings.Contains(got["summary"].(string), "Languages") {
		t.Fatalf("summary missing languages: %v", got["summary"])
	}
}

// ─── search_codebase ───────────────────────────────

func TestSearchCodebase_ReturnsRelevantFiles(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "src", "auth"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "src", "auth", "jwt.ts"), []byte("export {};\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "src", "auth", "session.ts"), []byte("export {};\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "src", "format.ts"), []byte("export {};\n"), 0o644)

	s, srv := newServerForTest(t, nil, nil, nil)
	s.SetRoot(dir)
	if err := s.IndexNow(); err != nil {
		t.Fatalf("IndexNow: %v", err)
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_codebase","arguments":{"query":"auth","limit":5}}}`
	resp := callRPC(t, srv, body)
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(toolText(t, resp)), &got)
	files := got["files"].([]any)
	if len(files) < 2 {
		t.Fatalf("expected ≥ 2 matches, got %d", len(files))
	}
	top := files[0].(map[string]any)
	if !strings.Contains(top["path"].(string), "auth") {
		t.Fatalf("top match should be in auth/, got %v", top["path"])
	}
	if top["relevance"].(float64) <= 0 {
		t.Fatalf("relevance should be > 0, got %v", top["relevance"])
	}
}

// ─── read_file ─────────────────────────────────────

func TestReadFile_ReturnsContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")
	if err := os.WriteFile(p, []byte("package x\n\nfunc X() {}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, srv := newServerForTest(t, nil, nil, nil)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_file","arguments":{"path":"` + p + `"}}}`
	resp := callRPC(t, srv, body)
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(toolText(t, resp)), &got)
	if !strings.Contains(got["content"].(string), "package x") {
		t.Fatalf("content missing: %v", got["content"])
	}
	if got["language"] != "Go" {
		t.Fatalf("language = %v, want Go", got["language"])
	}
}

func TestReadFile_LinesRangeSlices(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(p, []byte("one\ntwo\nthree\nfour\nfive\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, srv := newServerForTest(t, nil, nil, nil)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_file","arguments":{"path":"` + p + `","lines":"2-3"}}}`
	resp := callRPC(t, srv, body)
	var got map[string]any
	_ = json.Unmarshal([]byte(toolText(t, resp)), &got)
	content := got["content"].(string)
	if !strings.Contains(content, "two") || !strings.Contains(content, "three") {
		t.Fatalf("expected lines 2-3, got %q", content)
	}
	if strings.Contains(content, "one") || strings.Contains(content, "four") {
		t.Fatalf("range bled outside 2-3: %q", content)
	}
}

// ─── generate_commit_message ────────────────────────

func TestGenerateCommitMessage_CallsLensWithDiff(t *testing.T) {
	// Set up a git repo with staged content so the tool can read
	// the diff if staged_diff is not supplied. We pass staged_diff
	// directly to keep the test independent of cwd.
	lensSrv := fakeLens(t, []string{"feat: add greeter"})
	defer lensSrv.Close()

	_, srv := newServerForTest(t, lensSrv, nil, nil)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"generate_commit_message","arguments":{"staged_diff":"+++ b/x.go\n+package x\n","issue_id":"ENG-7"}}}`
	resp := callRPC(t, srv, body)
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(toolText(t, resp)), &got)
	if got["message"] != "ENG-7: feat: add greeter" {
		t.Fatalf("message = %q", got["message"])
	}
	if got["cost_usd"].(float64) <= 0 {
		t.Fatalf("cost_usd should be > 0, got %v", got["cost_usd"])
	}
}

func TestGenerateCommitMessage_EmptyDiffErrors(t *testing.T) {
	// No staged diff supplied and not in a git repo → error.
	dir := t.TempDir()
	prev, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	lensSrv := fakeLens(t, []string{})
	defer lensSrv.Close()
	_, srv := newServerForTest(t, lensSrv, nil, nil)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"generate_commit_message","arguments":{}}}`
	resp := callRPC(t, srv, body)
	if resp.Error == nil || resp.Error.Code != -32602 {
		t.Fatalf("expected -32602, got %+v", resp.Error)
	}
}

// ─── get_active_issue (degraded) ────────────────────

func TestGetActiveIssue_UnconfiguredTrackReturnsConfiguredFalse(t *testing.T) {
	_, srv := newServerForTest(t, nil, nil, nil)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_active_issue","arguments":{"workspace_id":"ws-1"}}}`
	resp := callRPC(t, srv, body)
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(toolText(t, resp)), &got)
	if got["configured"] != false {
		t.Fatalf("expected configured=false, got %+v", got)
	}
}

// ─── search_docs (degraded) ─────────────────────────

func TestSearchDocs_UnconfiguredReturnsConfiguredFalse(t *testing.T) {
	_, srv := newServerForTest(t, nil, nil, nil)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_docs","arguments":{"query":"auth","workspace_id":"ws-1"}}}`
	resp := callRPC(t, srv, body)
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(toolText(t, resp)), &got)
	if got["configured"] != false {
		t.Fatalf("expected configured=false, got %+v", got)
	}
}

// ─── SSE endpoint ──────────────────────────────────

func TestSSE_ReturnsEndpointEvent(t *testing.T) {
	_, srv := newServerForTest(t, nil, nil, nil)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/mcp/sse", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content-type = %q", got)
	}
	br := bufio.NewReader(resp.Body)
	// First event line should be `event: endpoint`.
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read line: %v", err)
	}
	if !strings.HasPrefix(line, "event: endpoint") {
		t.Fatalf("first line = %q", line)
	}
	dataLine, _ := br.ReadString('\n')
	if !strings.Contains(dataLine, `"uri":"/mcp"`) {
		t.Fatalf("data = %q", dataLine)
	}
}

// ─── reindex goroutine ─────────────────────────────

func TestStartReindex_StopHaltsGoroutine(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644)
	s, _ := newServerForTest(t, nil, nil, nil)
	s.SetRoot(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.StartReindex(ctx)
	if err := s.IndexNow(); err != nil {
		t.Fatalf("IndexNow: %v", err)
	}
	if s.CurrentIndex() == nil {
		t.Fatal("expected populated index")
	}
	s.Stop()
	// Calling Stop again should be a no-op.
	s.Stop()
}

// ─── exhaust import warnings ───────────────────────

// keep imports referenced in case future tests need them.
var _ = bytes.NewReader
var _ = exec.Command
