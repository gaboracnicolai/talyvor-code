package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/talyvor/code/internal/config"
	"github.com/talyvor/code/internal/lens"
)

func TestRun_VersionPrintsVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"version"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), version) {
		t.Fatalf("expected version in output, got %q", stdout.String())
	}
}

func TestRun_NoArgsPrintsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(nil, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "USAGE") {
		t.Fatalf("expected USAGE help, got %q", stdout.String())
	}
}

func TestRun_UnknownCommandErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"unknownthing"}, &stdout, &stderr); err == nil {
		t.Fatal("expected error for unknown command")
	}
}

// TestRun_AskRequiresConfig — ask validates that lens URL / API
// key / workspace are present before issuing the request. Without
// them we want a clear error rather than a confusing network
// failure mid-fetch.
func TestRun_AskRequiresConfig(t *testing.T) {
	for _, k := range []string{
		"TALYVOR_LENS_URL", "TALYVOR_LENS_API_KEY", "TALYVOR_WORKSPACE_ID",
		"TALYVOR_TRACK_URL", "TALYVOR_TRACK_API_KEY", "TALYVOR_ISSUE",
	} {
		t.Setenv(k, "")
	}
	var stdout, stderr bytes.Buffer
	err := run([]string{"ask", "what is foo?"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "missing required configuration") {
		t.Fatalf("expected config error, got %v", err)
	}
}

// TestRun_AskHitsLensWithIssueHeader exercises the happy path
// end-to-end through a fake Lens server. Confirms the
// X-Talyvor-Issue header propagates and the response body lands
// on stdout.
func TestRun_AskHitsLensWithIssueHeader(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"42"}]}`))
	}))
	defer srv.Close()
	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	t.Setenv("TALYVOR_ISSUE", "ENG-42")
	t.Setenv("TALYVOR_TRACK_URL", "")
	t.Setenv("TALYVOR_TRACK_API_KEY", "")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"ask", "what's the answer?"}, &stdout, &stderr); err != nil {
		t.Fatalf("run ask: %v", err)
	}
	if !strings.Contains(stdout.String(), "42") {
		t.Fatalf("stdout missing answer: %q", stdout.String())
	}
	if got.Get("X-Talyvor-Issue") != "ENG-42" {
		t.Fatalf("issue header wrong: %q", got.Get("X-Talyvor-Issue"))
	}
	if got.Get("X-Talyvor-Feature") != "code-ask" {
		t.Fatalf("feature header wrong: %q", got.Get("X-Talyvor-Feature"))
	}
}

// ─── chat REPL ───────────────────────────────────────

// TestChat_GreetsAndExitsOnEOF — the REPL prints its banner and
// quits cleanly when stdin closes (EOF). No HTTP traffic expected
// because the user never sent a message.
func TestChat_GreetsAndExitsOnEOF(t *testing.T) {
	t.Setenv("TALYVOR_LENS_URL", "http://localhost:9999")
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	t.Setenv("TALYVOR_ISSUE", "ENG-42")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"chat"}, &stdout, &stderr); err != nil {
		t.Fatalf("chat: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Talyvor Code Chat") {
		t.Fatalf("banner missing: %q", out)
	}
	if !strings.Contains(out, "ENG-42") {
		t.Fatalf("issue missing from banner: %q", out)
	}
}

// TestChat_SlashClearAndIssueAffectsState drives the slash
// commands directly via runChat with a stdin buffer so we don't
// need a live Lens. The REPL prints state changes to stdout; we
// assert against those.
func TestChat_SlashClearAndIssueAffectsState(t *testing.T) {
	cfg := config.Config{
		LensURL:     "http://localhost:9999",
		LensAPIKey:  "tlv_k",
		WorkspaceID: "ws-1",
		ActiveIssue: "ENG-1",
		Model:       "claude-haiku-4-6",
	}
	stdin := strings.NewReader("/clear\n/issue ENG-99\nexit\n")
	var stdout, stderr bytes.Buffer
	if err := runChat(stdin, &stdout, &stderr, cfg); err != nil {
		t.Fatalf("runChat: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "History cleared.") {
		t.Fatalf("/clear didn't echo: %q", out)
	}
	if !strings.Contains(out, "Active issue: ENG-99") {
		t.Fatalf("/issue didn't echo: %q", out)
	}
}

// TestChat_SendMessageRoundTrip drives one full message through a
// fake Lens server and asserts the reply lands on stdout, plus
// the X-Talyvor-Issue header carries the active issue.
func TestChat_SendMessageRoundTrip(t *testing.T) {
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hello back"}]}`))
	}))
	defer srv.Close()
	cfg := config.Config{
		LensURL:     srv.URL,
		LensAPIKey:  "tlv_k",
		WorkspaceID: "ws-1",
		ActiveIssue: "ENG-42",
		Model:       "claude-haiku-4-6",
	}
	stdin := strings.NewReader("ping\nexit\n")
	var stdout, stderr bytes.Buffer
	if err := runChat(stdin, &stdout, &stderr, cfg); err != nil {
		t.Fatalf("runChat: %v", err)
	}
	if !strings.Contains(stdout.String(), "hello back") {
		t.Fatalf("reply missing: %q", stdout.String())
	}
	if gotHeaders.Get("X-Talyvor-Issue") != "ENG-42" {
		t.Fatalf("issue header wrong: %q", gotHeaders.Get("X-Talyvor-Issue"))
	}
}

func TestTrimChatHistory_DropsOldestPair(t *testing.T) {
	// 22 messages — over by 2. Expect length 20 with q1 at head.
	in := make([]lens.Message, 0, 22)
	for i := 0; i < 11; i++ {
		in = append(in,
			lens.Message{Role: "user", Content: fmt.Sprintf("q%d", i)},
			lens.Message{Role: "assistant", Content: fmt.Sprintf("a%d", i)},
		)
	}
	out := trimChatHistory(in)
	if len(out) != MaxChatHistory {
		t.Fatalf("len = %d", len(out))
	}
	if out[0].Content != "q1" {
		t.Fatalf("head = %q, want q1", out[0].Content)
	}
}

// ─── test subcommand ────────────────────────────────

// TestTest_HitsLensWithSonnetAndWritesFile drives the full
// happy-path test-generation flow through a fake Lens. Asserts:
// the request uses claude-sonnet-4-6 (quality model), the
// feature header carries code-test-gen, and the response lands
// at the auto-suggested test-file path.
func TestTest_HitsLensWithSonnetAndWritesFile(t *testing.T) {
	var gotBody map[string]any
	var gotFeature string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &gotBody)
		gotFeature = r.Header.Get("X-Talyvor-Feature")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"package foo\n\nimport \"testing\"\n\nfunc TestFoo(t *testing.T) {}\n"}]}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	src := filepath.Join(dir, "foo.go")
	if err := os.WriteFile(src, []byte("package foo\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	t.Setenv("TALYVOR_ISSUE", "ENG-42")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"test", src}, &stdout, &stderr); err != nil {
		t.Fatalf("run test: %v", err)
	}
	if gotBody["model"] != "claude-sonnet-4-6" {
		t.Fatalf("expected sonnet model, got %v", gotBody["model"])
	}
	if gotFeature != "code-test-gen" {
		t.Fatalf("feature header: %q", gotFeature)
	}
	expected := filepath.Join(dir, "foo_test.go")
	body, err := os.ReadFile(expected)
	if err != nil {
		t.Fatalf("expected test file %s: %v", expected, err)
	}
	if !strings.Contains(string(body), "TestFoo") {
		t.Fatalf("test body not written: %q", string(body))
	}
	if !strings.Contains(stdout.String(), expected) {
		t.Fatalf("stdout should mention output path: %q", stdout.String())
	}
}

func TestTest_ExistingOutputRefuses(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "foo.go")
	if err := os.WriteFile(src, []byte("package foo\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	// Pre-create the suggested target — runTest must refuse to
	// overwrite without an explicit --output.
	if err := os.WriteFile(filepath.Join(dir, "foo_test.go"), []byte("// existing\n"), 0o644); err != nil {
		t.Fatalf("write existing: %v", err)
	}
	t.Setenv("TALYVOR_LENS_URL", "http://localhost:9999")
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")

	var stdout, stderr bytes.Buffer
	err := run([]string{"test", src}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected already-exists error, got %v", err)
	}
}

func TestSuggestTestOutput(t *testing.T) {
	cases := []struct {
		path, lang, want string
	}{
		{"/a/foo.go", "go", "/a/foo_test.go"},
		{"/a/bar.py", "python", "/a/test_bar.py"},
		{"/a/baz.ts", "typescript", "/a/baz.test.ts"},
		{"/a/Baz.java", "java", "/a/BazTest.java"},
	}
	for _, c := range cases {
		got := suggestTestOutput(c.path, c.lang)
		if got != c.want {
			t.Errorf("suggestTestOutput(%s, %s) = %s, want %s",
				c.path, c.lang, got, c.want)
		}
	}
}

// ─── run (agent) subcommand ─────────────────────────

// fakeAgentLens returns a planner JSON on the first call, then
// the per-file content on subsequent calls. Lets us drive the
// full plan → execute flow without a real model.
func fakeAgentLens(t *testing.T, replies []string) *httptest.Server {
	t.Helper()
	idx := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if idx >= len(replies) {
			t.Fatalf("fake lens: unexpected extra request (idx=%d)", idx)
		}
		body := replies[idx]
		idx++
		w.Header().Set("Content-Type", "application/json")
		// Encode as the Anthropic content array shape the client
		// expects. JSON-escape via the standard library so the
		// test data can carry arbitrary characters.
		encoded, _ := json.Marshal(body)
		fmt.Fprintf(w, `{"content":[{"type":"text","text":%s}]}`, encoded)
		_ = r
	}))
}

func TestRun_DryRunShowsPlanWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	// Planner says "modify foo.txt"; executor returns new content.
	srv := fakeAgentLens(t, []string{
		`{"plan":["modify foo"],"files":[{"path":"foo.txt","operation":"modify","description":"uppercase"}]}`,
		"HELLO\n",
	})
	defer srv.Close()

	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	t.Setenv("TALYVOR_ISSUE", "ENG-42")

	// chdir into temp dir so workspaceRoot lines up.
	prev, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"run", "--dry-run", "uppercase foo"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Plan:") || !strings.Contains(out, "modify foo") {
		t.Fatalf("plan not surfaced: %q", out)
	}
	if !strings.Contains(out, "-hello") || !strings.Contains(out, "+HELLO") {
		t.Fatalf("diff not surfaced: %q", out)
	}
	// File must NOT have been written under --dry-run.
	body, _ := os.ReadFile(filepath.Join(dir, "foo.txt"))
	if string(body) != "hello\n" {
		t.Fatalf("file modified despite --dry-run: %q", string(body))
	}
}

func TestRun_YesAppliesAllChanges(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	srv := fakeAgentLens(t, []string{
		`{"plan":["modify foo"],"files":[{"path":"foo.txt","operation":"modify","description":"uppercase"}]}`,
		"HELLO\n",
	})
	defer srv.Close()

	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")

	prev, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"run", "--yes", "uppercase foo"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "foo.txt"))
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(body) != "HELLO\n" {
		t.Fatalf("file not updated, got %q", string(body))
	}
	if !strings.Contains(stdout.String(), "Applied 1/1 changes") {
		t.Fatalf("summary missing: %q", stdout.String())
	}
}

func TestParsePlan_StripsMarkdownFence(t *testing.T) {
	raw := "```json\n{\"plan\":[\"a\"],\"files\":[{\"path\":\"x.go\",\"operation\":\"create\",\"description\":\"d\"}]}\n```"
	p, err := parsePlan(raw)
	if err != nil {
		t.Fatalf("parsePlan: %v", err)
	}
	if len(p.Plan) != 1 || p.Plan[0] != "a" {
		t.Fatalf("plan steps wrong: %+v", p.Plan)
	}
	if len(p.Files) != 1 || p.Files[0].Path != "x.go" || p.Files[0].Operation != "create" {
		t.Fatalf("files wrong: %+v", p.Files)
	}
}

// TestRun_AgentPostsTrackCommentAfterSuccess wires a fake Track
// alongside the fake Lens. After a successful agent run we expect
// a POST to /v1/workspaces/ws-1/issues/ENG-42/comments carrying
// the canonical "🤖 Talyvor Agent completed task" body.
func TestRun_AgentPostsTrackCommentAfterSuccess(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	lensSrv := fakeAgentLens(t, []string{
		`{"plan":["modify foo"],"files":[{"path":"foo.txt","operation":"modify","description":"uppercase"}]}`,
		"HELLO\n",
	})
	defer lensSrv.Close()

	var trackPath string
	var trackBody map[string]string
	trackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trackPath = r.URL.Path
		if r.Method == http.MethodPost {
			buf, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(buf, &trackBody)
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer trackSrv.Close()

	t.Setenv("TALYVOR_LENS_URL", lensSrv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	t.Setenv("TALYVOR_ISSUE", "ENG-42")
	t.Setenv("TALYVOR_TRACK_URL", trackSrv.URL)
	t.Setenv("TALYVOR_TRACK_API_KEY", "tlv_track")

	prev, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"run", "--yes", "uppercase foo"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.HasSuffix(trackPath, "/v1/workspaces/ws-1/issues/ENG-42/comments") {
		t.Fatalf("track endpoint not hit, got path %q", trackPath)
	}
	if !strings.Contains(trackBody["content"], "Talyvor Agent completed task") {
		t.Fatalf("comment body wrong: %q", trackBody["content"])
	}
	if trackBody["author_id"] != "talyvor-agent" {
		t.Fatalf("author_id = %q", trackBody["author_id"])
	}
}

// TestRun_AgentSkipsTrackCommentWhenUnconfigured ensures Track
// failure modes don't cascade: if Track isn't configured we just
// log and move on without erroring the CLI.
func TestRun_AgentSkipsTrackCommentWhenUnconfigured(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	srv := fakeAgentLens(t, []string{
		`{"plan":["modify"],"files":[{"path":"foo.txt","operation":"modify","description":"x"}]}`,
		"HELLO\n",
	})
	defer srv.Close()

	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	t.Setenv("TALYVOR_ISSUE", "ENG-42")
	t.Setenv("TALYVOR_TRACK_URL", "")
	t.Setenv("TALYVOR_TRACK_API_KEY", "")

	prev, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := run([]string{"run", "--yes", "uppercase foo"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestBuildAgentCompletionComment_HasExpectedShape(t *testing.T) {
	out := buildAgentCompletionComment("add JWT auth", 3, "claude-sonnet-4-6")
	for _, want := range []string{
		"Talyvor Agent completed task: add JWT auth",
		"Files changed: 3",
		"Model: claude-sonnet-4-6",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in: %s", want, out)
		}
	}
}

func TestParseLineRange(t *testing.T) {
	if a, b, ok := parseLineRange("10-50", 100); !ok || a != 10 || b != 50 {
		t.Fatalf("10-50 → %d,%d,%v", a, b, ok)
	}
	if a, b, ok := parseLineRange("5-200", 30); !ok || a != 5 || b != 30 {
		t.Fatalf("clamp to total: %d,%d,%v", a, b, ok)
	}
	if _, _, ok := parseLineRange("not a range", 30); ok {
		t.Fatal("malformed should return false")
	}
	if _, _, ok := parseLineRange("10-5", 30); ok {
		t.Fatal("reversed bounds should return false")
	}
}
