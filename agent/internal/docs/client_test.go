package docs

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsConfigured(t *testing.T) {
	if New("", "").IsConfigured() {
		t.Fatal("expected unconfigured for empty url+key")
	}
	if !New("http://docs", "tlv_k").IsConfigured() {
		t.Fatal("expected configured for url+key")
	}
}

func TestSearch_HitsCorrectEndpointWithLimit(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"results":[{"page_id":"p1","page_title":"Auth flow","space_name":"Engineering","headline":"...","rank":0.92,"source":"both","url":"/spaces/s1/pages/p1"}],"total":1,"query":"auth","took_ms":12}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tlv_k")
	out, err := c.Search(context.Background(), "ws-1", "auth", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/v1/workspaces/ws-1/search") {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "q=auth") || !strings.Contains(gotQuery, "limit=5") {
		t.Errorf("query = %q", gotQuery)
	}
	if gotAuth != "Bearer tlv_k" {
		t.Errorf("auth = %q", gotAuth)
	}
	if len(out) != 1 || out[0].PageTitle != "Auth flow" || out[0].Rank != 0.92 {
		t.Fatalf("unexpected results: %+v", out)
	}
}

func TestSearch_EncodesQueryWithSpaces(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"results":[],"total":0}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "tlv_k")
	if _, err := c.Search(context.Background(), "ws-1", "JWT refresh tokens", 5); err != nil {
		t.Fatalf("Search: %v", err)
	}
	// Either + or %20 is acceptable URL encoding for spaces.
	if !strings.Contains(gotQuery, "JWT+refresh+tokens") &&
		!strings.Contains(gotQuery, "JWT%20refresh%20tokens") {
		t.Errorf("query missing encoding: %q", gotQuery)
	}
}

func TestSearch_UnconfiguredReturnsEmpty(t *testing.T) {
	c := New("", "")
	out, err := c.Search(context.Background(), "ws-1", "auth", 5)
	if err != nil {
		t.Fatalf("Search unconfigured: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty, got %d", len(out))
	}
}

func TestAskDocs_PostsQuestionWithBody(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody struct {
		Question string `json:"question"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &gotBody)
		_, _ = w.Write([]byte(`{"answer":"Use refresh tokens.","sources":[{"title":"Auth","url":"/spaces/s1/pages/p1"}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tlv_k")
	got, err := c.AskDocs(context.Background(), "ws-1", "How does JWT refresh work?")
	if err != nil {
		t.Fatalf("AskDocs: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/v1/workspaces/ws-1/ai/ask") {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody.Question != "How does JWT refresh work?" {
		t.Errorf("body question = %q", gotBody.Question)
	}
	if got == nil || got.Answer == "" {
		t.Fatalf("answer missing: %+v", got)
	}
	if len(got.Sources) != 1 || got.Sources[0].Title != "Auth" {
		t.Fatalf("sources wrong: %+v", got.Sources)
	}
}

func TestAskDocs_UnconfiguredReturnsNil(t *testing.T) {
	c := New("", "")
	got, err := c.AskDocs(context.Background(), "ws-1", "q?")
	if err != nil {
		t.Fatalf("unconfigured AskDocs: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestGetPage_DecodesPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/spaces/s1/pages/p1") {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"p1","space_id":"s1","title":"Auth flow","content_text":"How auth works","ai_cost_usd":2.5,"updated_at":"2026-05-20T10:00:00Z"}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "tlv_k")
	got, err := c.GetPage(context.Background(), "s1", "p1")
	if err != nil || got == nil {
		t.Fatalf("GetPage: err=%v got=%v", err, got)
	}
	if got.ID != "p1" || got.Title != "Auth flow" || got.AICostUSD != 2.5 {
		t.Fatalf("unexpected page: %+v", got)
	}
}

func TestGetPage_404ReturnsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	c := New(srv.URL, "tlv_k")
	got, err := c.GetPage(context.Background(), "s1", "missing")
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil on 404, got %+v", got)
	}
}
