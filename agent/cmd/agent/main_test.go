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
		"claude-haiku-4-6", "claude-sonnet-4-6", "claude-opus-4-6",
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
	if srv.requests[0]["model"] != "claude-haiku-4-6" {
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

func TestInit_CreatesRulesFile(t *testing.T) {
	dir := t.TempDir()
	chdirT(t, dir)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"init"}, &stdout, &stderr); err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(stdout.String(), "Created .talyvor-rules") {
		t.Fatalf("expected creation message, got %q", stdout.String())
	}
	body, err := os.ReadFile(filepath.Join(dir, ".talyvor-rules"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "[general]") {
		t.Fatalf("expected [general] section in output: %q", string(body))
	}
}

func TestInit_RefusesToOverwriteExistingRules(t *testing.T) {
	dir := t.TempDir()
	chdirT(t, dir)
	existing := "[general]\nKeep me\n"
	if err := os.WriteFile(filepath.Join(dir, ".talyvor-rules"), []byte(existing), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if err := run([]string{"init"}, &stdout, &stderr); err != nil {
		t.Fatalf("init: %v", err)
	}
	if !strings.Contains(stdout.String(), "Already initialized") {
		t.Fatalf("expected already-initialized message, got %q", stdout.String())
	}
	body, _ := os.ReadFile(filepath.Join(dir, ".talyvor-rules"))
	if string(body) != existing {
		t.Fatalf("existing file was overwritten:\n%s", string(body))
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
	if gotModel != "claude-haiku-4-6" {
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
		"```\nfeat: x\n```":    "feat: x",
		"\"chore: y\"":          "chore: y",
		"feat: z\n":             "feat: z",
		"  fix: trim  ":         "fix: trim",
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
