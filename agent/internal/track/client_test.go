package track

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestIsConfigured covers the gating used everywhere — without a
// url+key the agent is allowed to call into Track without it
// blowing up (the methods return zero values).
func TestIsConfigured(t *testing.T) {
	if New("", "").IsConfigured() {
		t.Fatal("expected unconfigured for empty url+key")
	}
	if !New("http://track", "tlv_k").IsConfigured() {
		t.Fatal("expected configured for url+key")
	}
}

func TestGetIssue_Returns404AsNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	c := New(srv.URL, "tlv_k")
	got, err := c.GetIssue(context.Background(), "ws-1", "ENG-42")
	if err != nil {
		t.Fatalf("GetIssue: unexpected error %v", err)
	}
	if got != nil {
		t.Fatalf("GetIssue: expected nil for 404, got %+v", got)
	}
}

func TestGetIssue_DecodesPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sanity-check the URL shape.
		if !strings.HasSuffix(r.URL.Path, "/v1/workspaces/ws-1/issues/ENG-42") {
			t.Errorf("bad path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"i1","identifier":"ENG-42","title":"Bug","status":"In Progress","description":"d","ai_cost_usd":1.5}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "tlv_k")
	got, err := c.GetIssue(context.Background(), "ws-1", "ENG-42")
	if err != nil || got == nil {
		t.Fatalf("GetIssue: err=%v got=%v", err, got)
	}
	if got.Identifier != "ENG-42" || got.AICostUSD != 1.5 {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

func TestAddComment_PostsToCorrectEndpoint(t *testing.T) {
	var gotPath string
	var gotMethod string
	var gotAuth string
	var gotBody struct {
		Content  string `json:"content"`
		AuthorID string `json:"author_id"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	c := New(srv.URL, "tlv_k")
	if err := c.AddComment(context.Background(), "ws-1", "ENG-42", "agent done"); err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/v1/workspaces/ws-1/issues/ENG-42/comments") {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer tlv_k" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotBody.Content != "agent done" {
		t.Errorf("body content = %q", gotBody.Content)
	}
	if gotBody.AuthorID != "talyvor-agent" {
		t.Errorf("author_id = %q, want talyvor-agent", gotBody.AuthorID)
	}
}

func TestAddComment_UnconfiguredIsNoOp(t *testing.T) {
	// Unconfigured Track must not error — the agent always tries
	// to comment but the user may not have set Track up. The CLI
	// shouldn't fail because of that.
	c := New("", "")
	if err := c.AddComment(context.Background(), "ws-1", "ENG-42", "x"); err != nil {
		t.Fatalf("AddComment unconfigured: %v", err)
	}
}

func TestListIssues_ReturnsIssuesWithLimit(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`[
			{"id":"i1","identifier":"ENG-1","title":"a","status":"Open","description":"","ai_cost_usd":0},
			{"id":"i2","identifier":"ENG-2","title":"b","status":"In Progress","description":"","ai_cost_usd":3.2}
		]`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tlv_k")
	out, err := c.ListIssues(context.Background(), "ws-1", 25)
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[1].Identifier != "ENG-2" || out[1].AICostUSD != 3.2 {
		t.Fatalf("bad issue[1]: %+v", out[1])
	}
	if !strings.Contains(gotQuery, "limit=25") {
		t.Errorf("query = %q, expected limit=25", gotQuery)
	}
}

func TestListIssues_UnconfiguredReturnsEmpty(t *testing.T) {
	c := New("", "")
	out, err := c.ListIssues(context.Background(), "ws-1", 10)
	if err != nil {
		t.Fatalf("ListIssues unconfigured: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty, got %d", len(out))
	}
}
