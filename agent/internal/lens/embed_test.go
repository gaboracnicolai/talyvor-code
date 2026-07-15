package lens

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestEmbed_RoutesThroughLensWithAttribution — Embed POSTs to the OpenAI embeddings
// proxy on Lens, carries the SAME auth + issue-attribution headers as a chat call
// (same trust boundary), sends {model, input}, and parses the returned vectors by
// index.
func TestEmbed_RoutesThroughLensWithAttribution(t *testing.T) {
	var gotPath, gotAuth, gotFeature, gotIssue string
	var gotBody struct {
		Model string   `json:"model"`
		Input []string `json:"input"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotFeature = r.Header.Get("X-Talyvor-Feature")
		gotIssue = r.Header.Get("X-Talyvor-Issue")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		// Returned out of order to prove index-ordering, not arrival-ordering.
		_, _ = io.WriteString(w, `{"data":[{"index":1,"embedding":[0.4,0.5,0.6]},{"index":0,"embedding":[0.1,0.2,0.3]}]}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "tkey")
	vecs, err := c.Embed(context.Background(), []string{"first", "second"}, "text-embedding-3-small", "embed", "ws1", "ENG-1")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if gotPath != "/v1/proxy/openai/v1/embeddings" {
		t.Errorf("path = %q, want the openai embeddings proxy", gotPath)
	}
	if gotAuth != "Bearer tkey" {
		t.Errorf("auth = %q, want Bearer tkey", gotAuth)
	}
	if gotFeature != "code-embed" {
		t.Errorf("feature = %q, want code-embed", gotFeature)
	}
	if gotIssue != "ENG-1" {
		t.Errorf("issue attribution header = %q, want ENG-1", gotIssue)
	}
	if gotBody.Model != "text-embedding-3-small" || len(gotBody.Input) != 2 {
		t.Errorf("body = %+v, want model + 2 inputs", gotBody)
	}
	if len(vecs) != 2 || len(vecs[0]) != 3 || vecs[0][0] != 0.1 || vecs[1][0] != 0.4 {
		t.Errorf("vectors parsed wrong (must be ordered by index): %v", vecs)
	}
}

// TestEmbed_NotConfigured — no URL/key ⇒ a clean error, no panic (callers degrade).
func TestEmbed_NotConfigured(t *testing.T) {
	if _, err := (New("", "")).Embed(context.Background(), []string{"x"}, "m", "embed", "", ""); err == nil {
		t.Error("Embed on an unconfigured client must error")
	}
}

// TestEmbed_ServerError — a 4xx/5xx surfaces as an error, not a silent empty result.
func TestEmbed_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := (New(srv.URL, "k")).Embed(context.Background(), []string{"x"}, "m", "embed", "", ""); err == nil {
		t.Error("a 500 from Lens must return an error")
	}
}
