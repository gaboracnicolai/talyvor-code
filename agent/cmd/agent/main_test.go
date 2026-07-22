package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
		Model:       "claude-haiku-4-5",
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
		Model:       "claude-haiku-4-5",
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

// ─── streaming ─────────────────────────────────────

// sseAnthropicHandler writes one fake-SSE Anthropic event per
// supplied line. Each call wraps the body in `data: <line>\n\n`.
func sseAnthropicHandler(events []string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, ev := range events {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", ev)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func TestAsk_StreamingDeliversChunks(t *testing.T) {
	srv := httptest.NewServer(sseAnthropicHandler([]string{
		`{"type":"message_start","message":{"usage":{"input_tokens":12}}}`,
		`{"type":"content_block_delta","delta":{"text":"Hello "}}`,
		`{"type":"content_block_delta","delta":{"text":"streaming "}}`,
		`{"type":"content_block_delta","delta":{"text":"world"}}`,
		`{"type":"message_delta","usage":{"output_tokens":3}}`,
		`{"type":"message_stop"}`,
		`[DONE]`,
	}))
	defer srv.Close()
	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"ask", "say hi"}, &stdout, &stderr); err != nil {
		t.Fatalf("ask: %v", err)
	}
	if !strings.Contains(stdout.String(), "Hello streaming world") {
		t.Fatalf("stdout missing concatenated stream: %q", stdout.String())
	}
}

func TestAsk_StreamingFallsBackToJSONWhenLensReturnsNonSSE(t *testing.T) {
	// Server returns plain JSON despite the stream:true request
	// — the streaming client should still surface the text.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"non-stream reply"}],"usage":{"input_tokens":10,"output_tokens":4}}`))
	}))
	defer srv.Close()
	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"ask", "anything"}, &stdout, &stderr); err != nil {
		t.Fatalf("ask: %v", err)
	}
	if !strings.Contains(stdout.String(), "non-stream reply") {
		t.Fatalf("fallback content missing: %q", stdout.String())
	}
}

// ─── pr subcommand ─────────────────────────────────

func TestPR_RequiresGitHubToken(t *testing.T) {
	// chdir into a fresh git repo with a github remote so the
	// preflight checks pass up to the token gate.
	dir := initRepoWithStaged(t, "f.txt", "x\n")
	runGit(t, dir, "remote", "add", "origin", "git@github.com:acme/widgets.git")
	chdirT(t, dir)

	t.Setenv("TALYVOR_LENS_URL", "http://localhost:9999")
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	t.Setenv("GITHUB_TOKEN", "")

	var stdout, stderr bytes.Buffer
	err := run([]string{"pr"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Fatalf("expected GITHUB_TOKEN error, got %v", err)
	}
}

func TestPR_RejectsNonGitHubRemote(t *testing.T) {
	dir := initRepoWithStaged(t, "f.txt", "x\n")
	runGit(t, dir, "remote", "add", "origin", "git@gitlab.com:acme/widgets.git")
	chdirT(t, dir)

	t.Setenv("TALYVOR_LENS_URL", "http://localhost:9999")
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	t.Setenv("GITHUB_TOKEN", "tlv_gh")

	var stdout, stderr bytes.Buffer
	err := run([]string{"pr"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "GitHub") {
		t.Fatalf("expected non-GitHub error, got %v", err)
	}
}

// ─── run --heal ────────────────────────────────────

// TestRun_HealSuccessOnFirstBuild asserts the happy path: agent
// applies the change, --heal detects the build command, runs it,
// and succeeds without ever calling Lens for healing.
func TestRun_HealSuccessOnFirstBuild(t *testing.T) {
	dir := t.TempDir()
	// Drop a go.mod so DetectBuildCommand resolves to "go build
	// ./...". We point --heal-cmd at `true` so we don't actually
	// shell out to the Go toolchain in the test.
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
	t.Setenv("TALYVOR_ISSUE", "ENG-42")

	chdirT(t, dir)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"run", "--yes", "--heal", "--heal-cmd", "true", "uppercase foo"}, &stdout, &stderr); err != nil {
		t.Fatalf("run --heal: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Running build check") {
		t.Fatalf("heal banner missing: %q", out)
	}
	if !strings.Contains(out, "Build passes") {
		t.Fatalf("success line missing: %q", out)
	}
}

// TestRun_HealLoopRecoversAfterOneFix exercises the repair pass:
// the fake "build" exits non-zero on the first invocation and
// zero on the second, Lens replies with a single FileFix, and
// the loop reports "Fixed on attempt 1".
func TestRun_HealLoopRecoversAfterOneFix(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	// Stateful "build" command: a script that fails the first
	// time it's run and succeeds the second time. Lives in a
	// tmpdir alongside the source so it's reachable via PATH.
	flagFile := filepath.Join(dir, ".heal-state")
	healScript := filepath.Join(dir, "fake-build.sh")
	if err := os.WriteFile(healScript, []byte(`#!/bin/sh
if [ -f "$0.ran" ]; then
  echo "ok"
  exit 0
fi
touch "$0.ran"
echo "compile error: undefined symbol" >&2
exit 1
`), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	_ = flagFile // kept for clarity; the script writes its own marker

	healJSON := `[{"file":"foo.txt","content":"FIXED\n"}]`
	srv := fakeAgentLens(t, []string{
		`{"plan":["modify foo"],"files":[{"path":"foo.txt","operation":"modify","description":"uppercase"}]}`,
		"HELLO\n",
		healJSON,
	})
	defer srv.Close()

	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")

	chdirT(t, dir)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"run", "--yes", "--heal", "--heal-cmd", healScript, "uppercase foo"}, &stdout, &stderr); err != nil {
		t.Fatalf("run --heal: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Build failed") {
		t.Fatalf("expected first-attempt failure surfaced: %q", out)
	}
	if !strings.Contains(out, "Fixed on attempt 1") {
		t.Fatalf("expected recovery line: %q", out)
	}
	// The heal fix must have been written.
	body, _ := os.ReadFile(filepath.Join(dir, "foo.txt"))
	if string(body) != "FIXED\n" {
		t.Fatalf("heal content not written, got %q", string(body))
	}
}

// TestRun_HealSkipsWhenBuildSystemAbsent exercises the graceful
// degradation path: no go.mod / package.json / etc → heal prints
// a warning and the run completes normally.
func TestRun_HealSkipsWhenBuildSystemAbsent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	srv := fakeAgentLens(t, []string{
		`{"plan":["modify foo"],"files":[{"path":"foo.txt","operation":"modify","description":"x"}]}`,
		"HELLO\n",
	})
	defer srv.Close()
	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")

	chdirT(t, dir)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"run", "--yes", "--heal", "uppercase"}, &stdout, &stderr); err != nil {
		t.Fatalf("run --heal: %v", err)
	}
	// No build markers → graceful skip with warning on stderr.
	if !strings.Contains(stderr.String(), "heal:") {
		t.Fatalf("expected heal warning on stderr: %q", stderr.String())
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

// ─── models subcommand ─────────────────────────────

func TestModels_PrintsTable(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"models"}, &stdout, &stderr); err != nil {
		t.Fatalf("models: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"claude-haiku-4-5", "claude-sonnet-4-6", "claude-opus-4-6",
		"gpt-4o", "gpt-4o-mini", "mistral-large",
		"Provider", "Speed", "Cost",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// ─── --model flag wiring ───────────────────────────

func TestAsk_ModelFlagPicksTheRequestedModel(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &gotBody)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer srv.Close()
	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	t.Setenv("TALYVOR_MODEL", "")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"ask", "--model", "gpt-4o", "what's up?"}, &stdout, &stderr); err != nil {
		t.Fatalf("ask: %v", err)
	}
	if gotBody["model"] != "gpt-4o" {
		t.Fatalf("expected gpt-4o, got %v", gotBody["model"])
	}
}

func TestAsk_InvalidModelErrors(t *testing.T) {
	t.Setenv("TALYVOR_LENS_URL", "http://localhost:9999")
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	t.Setenv("TALYVOR_MODEL", "")
	var stdout, stderr bytes.Buffer
	err := run([]string{"ask", "--model", "no-such-model", "anything"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "unknown model") {
		t.Fatalf("expected unknown-model error, got %v", err)
	}
}

func TestEnvModelOverridesDefault(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &gotBody)
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer srv.Close()
	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	t.Setenv("TALYVOR_MODEL", "claude-opus-4-6")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"ask", "say hi"}, &stdout, &stderr); err != nil {
		t.Fatalf("ask: %v", err)
	}
	if gotBody["model"] != "claude-opus-4-6" {
		t.Fatalf("env model should win, got %v", gotBody["model"])
	}
}

// ─── shell subcommand ──────────────────────────────

// shellLensServer is a tiny fake Lens that captures every
// request body and replies with canned answers in order. Lets us
// assert "shell --explain called Lens twice" cleanly.
type shellLensServer struct {
	srv      *httptest.Server
	requests []map[string]any
	replies  []string
	idx      int
}

func newShellLens(t *testing.T, replies []string) *shellLensServer {
	t.Helper()
	s := &shellLensServer{replies: replies}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &body)
		s.requests = append(s.requests, body)
		text := ""
		if s.idx < len(s.replies) {
			text = s.replies[s.idx]
		}
		s.idx++
		enc, _ := json.Marshal(text)
		fmt.Fprintf(w, `{"content":[{"type":"text","text":%s}],"usage":{"input_tokens":60,"output_tokens":12}}`, enc)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func TestShell_PrintsCommandAndUsesHaiku(t *testing.T) {
	srv := newShellLens(t, []string{"lsof -ti :8080 | xargs kill -9"})

	t.Setenv("TALYVOR_LENS_URL", srv.srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	t.Setenv("SHELL", "/bin/zsh")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"shell", "kill", "port", "8080"}, &stdout, &stderr); err != nil {
		t.Fatalf("shell: %v", err)
	}
	if !strings.Contains(stdout.String(), "$ lsof -ti :8080") {
		t.Fatalf("command not surfaced: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Add --run") {
		t.Fatalf("expected --run hint, got: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "cost: $") {
		t.Fatalf("cost not surfaced: %q", stderr.String())
	}
	if len(srv.requests) != 1 {
		t.Fatalf("expected 1 lens request, got %d", len(srv.requests))
	}
	if srv.requests[0]["model"] != "claude-haiku-4-5" {
		t.Errorf("expected haiku, got %v", srv.requests[0]["model"])
	}
}

func TestShell_AliasSh(t *testing.T) {
	srv := newShellLens(t, []string{"docker ps -a"})
	t.Setenv("TALYVOR_LENS_URL", srv.srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"sh", "list", "containers"}, &stdout, &stderr); err != nil {
		t.Fatalf("sh: %v", err)
	}
	if !strings.Contains(stdout.String(), "docker ps -a") {
		t.Fatalf("alias did not run: %q", stdout.String())
	}
}

func TestShell_ExplainCallsLensTwice(t *testing.T) {
	srv := newShellLens(t, []string{
		"docker ps -a",
		"`docker ps` lists containers; `-a` includes stopped ones.",
	})
	t.Setenv("TALYVOR_LENS_URL", srv.srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"shell", "--explain", "list", "all", "containers"}, &stdout, &stderr); err != nil {
		t.Fatalf("shell: %v", err)
	}
	if len(srv.requests) != 2 {
		t.Fatalf("expected 2 lens requests (gen + explain), got %d", len(srv.requests))
	}
	if !strings.Contains(stdout.String(), "lists containers") {
		t.Fatalf("explanation not surfaced: %q", stdout.String())
	}
}

func TestShell_RunFlagExecutesCommand(t *testing.T) {
	srv := newShellLens(t, []string{`printf hello`})
	t.Setenv("TALYVOR_LENS_URL", srv.srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")

	stdin := strings.NewReader("y\n")
	var stdout, stderr bytes.Buffer
	if err := runShell(stdin, &stdout, &stderr, config.Load(config.Config{}), []string{"--run", "say hello"}); err != nil {
		t.Fatalf("runShell: %v", err)
	}
	if !strings.Contains(stdout.String(), "hello") {
		t.Fatalf("execution output missing: %q", stdout.String())
	}
}

func TestShell_RunFlagAbortsOnNo(t *testing.T) {
	srv := newShellLens(t, []string{"echo banana"})
	t.Setenv("TALYVOR_LENS_URL", srv.srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	stdin := strings.NewReader("n\n")
	var stdout, stderr bytes.Buffer
	if err := runShell(stdin, &stdout, &stderr, config.Load(config.Config{}), []string{"--run", "print banana"}); err != nil {
		t.Fatalf("runShell: %v", err)
	}
	if !strings.Contains(stdout.String(), "Aborted.") {
		t.Fatalf("expected abort, got %q", stdout.String())
	}
	// stdout will contain "$ echo banana" (the printed command).
	// After the abort, no separate execution-output line with
	// just "banana\n" should appear.
	lines := strings.Split(stdout.String(), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "banana" {
			t.Fatalf("execution output leaked despite abort: %q", stdout.String())
		}
	}
}

func TestShell_RequiresDescription(t *testing.T) {
	t.Setenv("TALYVOR_LENS_URL", "http://localhost:9999")
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	var stdout, stderr bytes.Buffer
	err := run([]string{"shell"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "description") {
		t.Fatalf("expected description error, got %v", err)
	}
}

// ─── init subcommand ───────────────────────────────

// TestInit_CreatesRulesAndPlaceholderContext drives the no-Lens
// path through `init`: stdin answers "n" to the auto-generate
// prompt, so we expect both .talyvor-rules and a placeholder
// .talyvor-context to land on disk.
func TestInit_CreatesRulesAndPlaceholderContext(t *testing.T) {
	dir := t.TempDir()
	chdirT(t, dir)
	// init reads from os.Stdin directly via the dispatch — we
	// replace it for the duration of the test.
	origStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()
	_, _ = w.WriteString("n\n")
	_ = w.Close()

	var stdout, stderr bytes.Buffer
	if err := run([]string{"init"}, &stdout, &stderr); err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(stdout.String(), "Created .talyvor-rules") {
		t.Fatalf("expected rules creation message: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Created .talyvor-context") {
		t.Fatalf("expected context creation message: %q", stdout.String())
	}
	rules, err := os.ReadFile(filepath.Join(dir, ".talyvor-rules"))
	if err != nil {
		t.Fatalf("read rules: %v", err)
	}
	if !strings.Contains(string(rules), "[general]") {
		t.Fatalf("rules body wrong: %q", string(rules))
	}
	ctx, err := os.ReadFile(filepath.Join(dir, ".talyvor-context"))
	if err != nil {
		t.Fatalf("read context: %v", err)
	}
	if !strings.Contains(string(ctx), "\"name\"") {
		t.Fatalf("context body wrong: %q", string(ctx))
	}
}

func TestInit_RefusesToOverwriteExistingRules(t *testing.T) {
	dir := t.TempDir()
	chdirT(t, dir)
	existing := "[general]\nKeep me\n"
	if err := os.WriteFile(filepath.Join(dir, ".talyvor-rules"), []byte(existing), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Pipe "n" so the context prompt doesn't try to generate.
	origStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()
	_, _ = w.WriteString("n\n")
	_ = w.Close()

	var stdout, stderr bytes.Buffer
	if err := run([]string{"init"}, &stdout, &stderr); err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(stdout.String(), "Already initialized") {
		t.Fatalf("expected already-initialized message: %q", stdout.String())
	}
	body, _ := os.ReadFile(filepath.Join(dir, ".talyvor-rules"))
	if string(body) != existing {
		t.Fatalf("existing rules overwritten:\n%s", string(body))
	}
}

// ─── scope subcommand ──────────────────────────────

const sampleScopesJSON = `{
  "auth": {
    "name": "Authentication",
    "includes": ["internal/auth/**"],
    "excludes": ["**/*_test.go"],
    "focus": "JWT auth and session management"
  },
  "api": {
    "name": "API Layer",
    "includes": ["internal/api/**"],
    "focus": "REST endpoints"
  }
}`

func TestScope_ListShowsCatalogue(t *testing.T) {
	dir := t.TempDir()
	chdirT(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".talyvor-scopes"), []byte(sampleScopesJSON), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if err := run([]string{"scope", "list"}, &stdout, &stderr); err != nil {
		t.Fatalf("scope list: %v", err)
	}
	for _, want := range []string{"auth", "Authentication", "api", "API Layer"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("missing %q in output:\n%s", want, stdout.String())
		}
	}
}

func TestScope_UsePersistsActiveScope(t *testing.T) {
	dir := t.TempDir()
	chdirT(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".talyvor-scopes"), []byte(sampleScopesJSON), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if err := run([]string{"scope", "use", "auth"}, &stdout, &stderr); err != nil {
		t.Fatalf("scope use: %v", err)
	}
	if !strings.Contains(stdout.String(), "Scope set to: auth") {
		t.Fatalf("expected confirmation: %q", stdout.String())
	}
	body, err := os.ReadFile(filepath.Join(dir, ".talyvor-active-scope"))
	if err != nil {
		t.Fatalf("read active-scope: %v", err)
	}
	if strings.TrimSpace(string(body)) != "auth" {
		t.Fatalf("active-scope body = %q, want auth", string(body))
	}

	// `scope list` now marks auth as active.
	var listOut bytes.Buffer
	if err := run([]string{"scope", "list"}, &listOut, &stderr); err != nil {
		t.Fatalf("scope list: %v", err)
	}
	if !strings.Contains(listOut.String(), "* auth") {
		t.Fatalf("list should mark auth active: %q", listOut.String())
	}
}

func TestScope_ClearRemovesActiveFile(t *testing.T) {
	dir := t.TempDir()
	chdirT(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".talyvor-scopes"), []byte(sampleScopesJSON), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".talyvor-active-scope"), []byte("auth\n"), 0o644); err != nil {
		t.Fatalf("write active: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if err := run([]string{"scope", "clear"}, &stdout, &stderr); err != nil {
		t.Fatalf("scope clear: %v", err)
	}
	if !strings.Contains(stdout.String(), "Scope cleared") {
		t.Fatalf("expected cleared message: %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".talyvor-active-scope")); err == nil {
		t.Fatal("active-scope file should be gone")
	}
}

// TestAsk_PromptIncludesActiveScope confirms .talyvor-scopes is
// auto-prepended to the user-message body (after rules + context).
func TestAsk_PromptIncludesActiveScope(t *testing.T) {
	dir := t.TempDir()
	chdirT(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".talyvor-scopes"), []byte(sampleScopesJSON), 0o644); err != nil {
		t.Fatalf("write scopes: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".talyvor-active-scope"), []byte("auth\n"), 0o644); err != nil {
		t.Fatalf("write active: %v", err)
	}

	var gotContent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &body)
		if msgs, ok := body["messages"].([]any); ok && len(msgs) > 0 {
			if m, ok := msgs[0].(map[string]any); ok {
				gotContent, _ = m["content"].(string)
			}
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer srv.Close()
	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"ask", "what does auth do?"}, &stdout, &stderr); err != nil {
		t.Fatalf("ask: %v", err)
	}
	for _, want := range []string{"Active scope: Authentication", "Focus: JWT auth", "internal/auth/**"} {
		if !strings.Contains(gotContent, want) {
			t.Errorf("prompt missing %q:\n%s", want, gotContent)
		}
	}
}

// ─── context subcommand ────────────────────────────

func TestContext_ShowReportsMissingFile(t *testing.T) {
	chdirT(t, t.TempDir())
	t.Setenv("TALYVOR_LENS_URL", "http://localhost:9999")
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"context", "show"}, &stdout, &stderr); err != nil {
		t.Fatalf("context show: %v", err)
	}
	if !strings.Contains(stdout.String(), "No .talyvor-context") {
		t.Fatalf("expected missing-file note: %q", stdout.String())
	}
}

func TestContext_ShowRendersLoadedContext(t *testing.T) {
	dir := t.TempDir()
	chdirT(t, dir)
	body := `{"name":"X","description":"long enough description over 20","stack":["Go"]}`
	if err := os.WriteFile(filepath.Join(dir, ".talyvor-context"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("TALYVOR_LENS_URL", "http://localhost:9999")
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	var stdout, stderr bytes.Buffer
	if err := run([]string{"context", "show"}, &stdout, &stderr); err != nil {
		t.Fatalf("context show: %v", err)
	}
	for _, want := range []string{"Project context", "Name: X", "Stack: Go"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("missing %q in output:\n%s", want, stdout.String())
		}
	}
}

func TestContext_ValidateFlagsBadFile(t *testing.T) {
	dir := t.TempDir()
	chdirT(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".talyvor-context"), []byte(`{"name":""}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if err := run([]string{"context", "validate"}, &stdout, &stderr); err != nil {
		t.Fatalf("context validate: %v", err)
	}
	if !strings.Contains(stderr.String(), "name is required") {
		t.Fatalf("expected name warning: %q", stderr.String())
	}
}

func TestContext_ValidatePassesForGoodFile(t *testing.T) {
	dir := t.TempDir()
	chdirT(t, dir)
	body := `{"name":"X","description":"long enough description over 20","stack":["Go"]}`
	if err := os.WriteFile(filepath.Join(dir, ".talyvor-context"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if err := run([]string{"context", "validate"}, &stdout, &stderr); err != nil {
		t.Fatalf("context validate: %v", err)
	}
	if !strings.Contains(stdout.String(), "Context file is valid") {
		t.Fatalf("expected valid message: %q", stdout.String())
	}
}

// TestAsk_IncludesContextInPrompt confirms .talyvor-context is
// auto-prepended to the user-message body, ahead of the file
// content and the question.
func TestAsk_IncludesContextInPrompt(t *testing.T) {
	dir := t.TempDir()
	chdirT(t, dir)
	ctxBody := `{"name":"AcmeApp","description":"A long enough description here","stack":["Go","Postgres"]}`
	if err := os.WriteFile(filepath.Join(dir, ".talyvor-context"), []byte(ctxBody), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var gotContent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &body)
		if msgs, ok := body["messages"].([]any); ok && len(msgs) > 0 {
			if m, ok := msgs[0].(map[string]any); ok {
				gotContent, _ = m["content"].(string)
			}
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer srv.Close()
	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"ask", "what stack?"}, &stdout, &stderr); err != nil {
		t.Fatalf("ask: %v", err)
	}
	for _, want := range []string{"Project context", "Name: AcmeApp", "Stack: Go, Postgres"} {
		if !strings.Contains(gotContent, want) {
			t.Errorf("prompt missing %q:\n%s", want, gotContent)
		}
	}
}

// ─── review subcommand ─────────────────────────────

func TestReview_NoStagedChangesErrors(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "t@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	chdirT(t, dir)

	t.Setenv("TALYVOR_LENS_URL", "http://localhost:9999")
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")

	var stdout, stderr bytes.Buffer
	err := run([]string{"review"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "no staged") {
		t.Fatalf("expected no-staged error, got %v", err)
	}
}

func TestReview_OnExplicitFilesHitsSonnet(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "service.go")
	if err := os.WriteFile(target, []byte("package svc\n\nfunc Risky(){\n\t// TODO\n}\n"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	var gotBody map[string]any
	var gotFeature string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &gotBody)
		gotFeature = r.Header.Get("X-Talyvor-Feature")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"## Summary\nLooks fine.\n## Issues Found\n### Critical\nNone.\n"}]}`))
	}))
	defer srv.Close()

	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	t.Setenv("TALYVOR_ISSUE", "ENG-42")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"review", target}, &stdout, &stderr); err != nil {
		t.Fatalf("review: %v", err)
	}
	if gotBody["model"] != "claude-sonnet-4-6" {
		t.Errorf("expected sonnet, got %v", gotBody["model"])
	}
	if gotFeature != "code-code-review" && gotFeature != "code-review" {
		// Lens prepends "code-" to feature tags; either form is acceptable.
		t.Errorf("feature header = %q", gotFeature)
	}
	if !strings.Contains(stdout.String(), "## Summary") {
		t.Fatalf("review output missing structured markdown: %q", stdout.String())
	}
}

// ─── review --pr / --output ─────────────────────────

// initBaseAndFeatureRepo materialises a repo with one base
// commit and one feature-branch commit so review --pr has
// something to look at. Returns the repo dir.
func initBaseAndFeatureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "t@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGit(t, dir, "add", "base.txt")
	runGit(t, dir, "commit", "-q", "-m", "chore: base")
	runGit(t, dir, "checkout", "-q", "-b", "feature/x")
	if err := os.WriteFile(filepath.Join(dir, "feat.txt"), []byte("feature body\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGit(t, dir, "add", "feat.txt")
	runGit(t, dir, "commit", "-q", "-m", "feat: add feat.txt")
	return dir
}

func TestReview_PRSendsPRDiffAndPromptStructure(t *testing.T) {
	dir := initBaseAndFeatureRepo(t)
	chdirT(t, dir)

	var gotBody map[string]any
	var promptContent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &gotBody)
		if msgs, ok := gotBody["messages"].([]any); ok && len(msgs) > 0 {
			if m, ok := msgs[0].(map[string]any); ok {
				promptContent, _ = m["content"].(string)
			}
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"## PR Summary\nAdds feat.txt.\n## Review\n### 🔴 Critical Issues\nNone.\n### 🟡 Warnings\n- minor\n### 💡 Suggestions\n- consider X\n### ✅ Good Patterns\n- tidy structure\n## Verdict\nAPPROVE"}]}`))
	}))
	defer srv.Close()

	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"review", "--pr"}, &stdout, &stderr); err != nil {
		t.Fatalf("review --pr: %v", err)
	}
	// The diff body should mention feat.txt (PR scope) and NOT
	// base.txt (base commit before the fork).
	if !strings.Contains(promptContent, "feat.txt") {
		t.Fatalf("prompt missing feat.txt:\n%s", promptContent)
	}
	if strings.Contains(promptContent, "base.txt") {
		t.Fatalf("prompt should not include base file: present in:\n%s", promptContent)
	}
	// Prompt must include the PR-specific scaffolding so the
	// model produces the structured review.
	for _, want := range []string{"PR Summary", "Verdict", "Good Patterns"} {
		if !strings.Contains(promptContent, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	// Lens called with feature=pr-review.
	if gotBody["model"] != "claude-sonnet-4-6" {
		t.Errorf("expected sonnet, got %v", gotBody["model"])
	}
	if !strings.Contains(stderr.String(), "feature=pr-review") {
		t.Fatalf("stderr should report feature=pr-review: %q", stderr.String())
	}
	// Verdict + review surfaces on stdout.
	if !strings.Contains(stdout.String(), "## PR Summary") {
		t.Fatalf("review not surfaced: %q", stdout.String())
	}
}

func TestReview_OutputJSONIncludesVerdictAndCounts(t *testing.T) {
	dir := initBaseAndFeatureRepo(t)
	chdirT(t, dir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"## PR Summary\nA short description.\n\n## Review\n\n### 🔴 Critical Issues\n- SQL injection\n- Hardcoded token\n\n### 🟡 Warnings\n- N+1 query\n\n### 💡 Suggestions\n- Rename helper\n\n### ✅ Good Patterns\n- Solid error handling\n\n## Verdict\nREQUEST CHANGES"}]}`))
	}))
	defer srv.Close()
	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"review", "--pr", "--output", "json"}, &stdout, &stderr); err != nil {
		t.Fatalf("review --pr --output json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v\n%s", err, stdout.String())
	}
	if got["verdict"] != "REQUEST CHANGES" {
		t.Errorf("verdict = %v", got["verdict"])
	}
	if got["critical_count"].(float64) != 2 {
		t.Errorf("critical_count = %v", got["critical_count"])
	}
	if got["warning_count"].(float64) != 1 {
		t.Errorf("warning_count = %v", got["warning_count"])
	}
	if !strings.Contains(got["summary"].(string), "short description") {
		t.Errorf("summary = %v", got["summary"])
	}
	if !strings.Contains(got["full_review"].(string), "Solid error handling") {
		t.Errorf("full_review missing body")
	}
}

func TestReview_PRWithNoCommitsAhead(t *testing.T) {
	// Repo with only the base commit — switching to main means
	// HEAD == main and the PR diff is empty.
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "t@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGit(t, dir, "add", "f.txt")
	runGit(t, dir, "commit", "-q", "-m", "init")
	chdirT(t, dir)
	t.Setenv("TALYVOR_LENS_URL", "http://localhost:9999")
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")

	var stdout, stderr bytes.Buffer
	err := run([]string{"review", "--pr"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "no commits ahead") {
		t.Fatalf("expected 'no commits ahead' error, got %v", err)
	}
}

// ─── commit subcommand ─────────────────────────────

func TestCommit_NoStagedChangesErrors(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "t@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	chdirT(t, dir)

	t.Setenv("TALYVOR_LENS_URL", "http://localhost:9999")
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")

	var stdout, stderr bytes.Buffer
	err := run([]string{"commit"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "no staged changes") {
		t.Fatalf("expected no-staged error, got %v", err)
	}
}

func TestCommit_HappyPathConfirmsAndCommits(t *testing.T) {
	dir := initRepoWithStaged(t, "hello.txt", "hello\n")
	chdirT(t, dir)

	var gotModel any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &body)
		gotModel = body["model"]
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"feat: add hello file\n"}]}`))
	}))
	defer srv.Close()

	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")

	// User answers "y" to the confirmation prompt.
	stdin := strings.NewReader("y\n")
	var stdout, stderr bytes.Buffer
	if err := runCommit(stdin, &stdout, &stderr, config.Load(config.Config{}), nil); err != nil {
		t.Fatalf("runCommit: %v", err)
	}
	if gotModel != "claude-haiku-4-5" {
		t.Errorf("expected haiku model, got %v", gotModel)
	}
	if !strings.Contains(stdout.String(), "feat: add hello file") {
		t.Fatalf("proposed message not surfaced: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Committed.") {
		t.Fatalf("commit confirmation missing: %q", stdout.String())
	}
	// Verify the commit actually landed.
	out, _ := exec.Command("git", "log", "-1", "--pretty=%s").CombinedOutput()
	if !strings.Contains(string(out), "feat: add hello file") {
		t.Fatalf("commit subject not in log: %q", out)
	}
}

func TestCommit_RejectionAborts(t *testing.T) {
	dir := initRepoWithStaged(t, "x.txt", "x\n")
	chdirT(t, dir)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"chore: x"}]}`))
	}))
	defer srv.Close()
	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")

	stdin := strings.NewReader("n\n")
	var stdout, stderr bytes.Buffer
	if err := runCommit(stdin, &stdout, &stderr, config.Load(config.Config{}), nil); err != nil {
		t.Fatalf("runCommit: %v", err)
	}
	if !strings.Contains(stdout.String(), "Aborted.") {
		t.Fatalf("expected abort message, got %q", stdout.String())
	}
	// No commit should have been created.
	out, _ := exec.Command("git", "log").CombinedOutput()
	if strings.Contains(string(out), "chore: x") {
		t.Fatalf("commit unexpectedly created: %q", out)
	}
}

func TestCommit_IssuePrefixIsPrepended(t *testing.T) {
	dir := initRepoWithStaged(t, "y.txt", "y\n")
	chdirT(t, dir)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"fix: bug in y"}]}`))
	}))
	defer srv.Close()
	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")

	stdin := strings.NewReader("y\n")
	var stdout, stderr bytes.Buffer
	if err := runCommit(stdin, &stdout, &stderr, config.Load(config.Config{}), []string{"--issue", "ENG-42"}); err != nil {
		t.Fatalf("runCommit: %v", err)
	}
	if !strings.Contains(stdout.String(), "ENG-42: fix: bug in y") {
		t.Fatalf("expected issue prefix, got %q", stdout.String())
	}
}

func TestCleanCommitMessage_StripsArtifacts(t *testing.T) {
	cases := map[string]string{
		"```\nfeat: x\n```": "feat: x",
		"\"chore: y\"":      "chore: y",
		"feat: z\n":         "feat: z",
		"  fix: trim  ":     "fix: trim",
	}
	for in, want := range cases {
		got := cleanCommitMessage(in)
		if got != want {
			t.Errorf("cleanCommitMessage(%q) = %q, want %q", in, got, want)
		}
	}
}

// ─── helpers ─────────────────────────────────────

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func chdirT(t *testing.T, dir string) {
	t.Helper()
	prev, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
}

func initRepoWithStaged(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.email", "t@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGit(t, dir, "add", name)
	return dir
}

// ─── docs subcommand ────────────────────────────────

func TestDocs_SearchHitsCorrectEndpoint(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"results":[{"page_id":"p1","page_title":"Auth flow","space_name":"Eng","headline":"how auth works","rank":0.85,"source":"both","url":"/spaces/s1/pages/p1"}],"total":1}`))
	}))
	defer srv.Close()
	t.Setenv("TALYVOR_DOCS_URL", srv.URL)
	t.Setenv("TALYVOR_DOCS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	t.Setenv("TALYVOR_LENS_URL", "")
	t.Setenv("TALYVOR_LENS_API_KEY", "")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"docs", "search", "authentication", "flow"}, &stdout, &stderr); err != nil {
		t.Fatalf("docs search: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/v1/workspaces/ws-1/search") {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "authentication") {
		t.Errorf("query missing terms: %q", gotQuery)
	}
	if !strings.Contains(stdout.String(), "Auth flow") {
		t.Fatalf("output missing title: %q", stdout.String())
	}
}

func TestDocs_AskPostsQuestion(t *testing.T) {
	var gotMethod string
	var gotBody struct {
		Question string `json:"question"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &gotBody)
		_, _ = w.Write([]byte(`{"answer":"Use refresh tokens.","sources":[{"title":"Auth","url":"/spaces/s1/pages/p1"}]}`))
	}))
	defer srv.Close()
	t.Setenv("TALYVOR_DOCS_URL", srv.URL)
	t.Setenv("TALYVOR_DOCS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"docs", "ask", "How", "does", "JWT", "refresh", "work?"}, &stdout, &stderr); err != nil {
		t.Fatalf("docs ask: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q", gotMethod)
	}
	if !strings.Contains(gotBody.Question, "JWT refresh") {
		t.Errorf("question body wrong: %q", gotBody.Question)
	}
	if !strings.Contains(stdout.String(), "Use refresh tokens") {
		t.Fatalf("answer missing: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Sources:") {
		t.Fatalf("sources section missing: %q", stdout.String())
	}
}

func TestDocs_GetParsesSpacePageRef(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/spaces/s1/pages/p1") {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"p1","space_id":"s1","title":"Auth flow","content_text":"How it works"}`))
	}))
	defer srv.Close()
	t.Setenv("TALYVOR_DOCS_URL", srv.URL)
	t.Setenv("TALYVOR_DOCS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")

	var stdout, stderr bytes.Buffer
	if err := run([]string{"docs", "get", "s1/p1"}, &stdout, &stderr); err != nil {
		t.Fatalf("docs get: %v", err)
	}
	if !strings.Contains(stdout.String(), "Auth flow") {
		t.Fatalf("title missing: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "How it works") {
		t.Fatalf("content missing: %q", stdout.String())
	}
}

func TestDocs_RequiresDocsConfig(t *testing.T) {
	t.Setenv("TALYVOR_DOCS_URL", "")
	t.Setenv("TALYVOR_DOCS_API_KEY", "")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	var stdout, stderr bytes.Buffer
	err := run([]string{"docs", "search", "anything"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected docs unconfigured error")
	}
	if !strings.Contains(err.Error(), "docs:") {
		t.Fatalf("expected docs: prefix, got %v", err)
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
