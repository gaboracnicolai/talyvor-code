package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
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
