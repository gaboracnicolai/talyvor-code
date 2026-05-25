package lens

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsConfigured(t *testing.T) {
	if New("", "").IsConfigured() {
		t.Fatal("expected unconfigured for empty url+key")
	}
	if !New("http://lens", "tlv_k").IsConfigured() {
		t.Fatal("expected configured for url+key")
	}
}

func TestComplete_SendsAttributionHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hi"}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tlv_k")
	out, err := c.Complete(context.Background(),
		[]Message{{Role: "user", Content: "say hi"}},
		"claude-haiku-4-6", "chat", "ws-1", "ENG-42")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "hi" {
		t.Fatalf("body = %q, want hi", out)
	}
	if got.Get("Authorization") != "Bearer tlv_k" {
		t.Fatalf("missing bearer: %q", got.Get("Authorization"))
	}
	if got.Get("X-Talyvor-Feature") != "code-chat" {
		t.Fatalf("feature header wrong: %q", got.Get("X-Talyvor-Feature"))
	}
	if got.Get("X-Talyvor-Workspace") != "ws-1" {
		t.Fatalf("workspace header wrong: %q", got.Get("X-Talyvor-Workspace"))
	}
	if got.Get("X-Talyvor-Issue") != "ENG-42" {
		t.Fatalf("issue header wrong: %q", got.Get("X-Talyvor-Issue"))
	}
}

func TestComplete_ErrorsWhenUnconfigured(t *testing.T) {
	_, err := New("", "").Complete(context.Background(), nil, "m", "f", "w", "i")
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected unconfigured error, got %v", err)
	}
}
