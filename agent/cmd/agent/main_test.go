package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
