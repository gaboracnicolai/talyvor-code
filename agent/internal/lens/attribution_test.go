package lens

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestReportAttribution — the thin Lens caller: POST /v1/outputs/{id}/attribution with
// {target_kind,target_ref} + Bearer auth; 2xx → nil; 409 (already attributed, first-wins)
// → success-equivalent (nil); other 4xx/5xx → error; empty id / unconfigured → no-op nil.
func TestReportAttribution(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotBody string
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(status)
	}))
	defer srv.Close()
	c := New(srv.URL, "tlv_secret")

	if err := c.ReportAttribution(context.Background(), "oid-1", "pr", "https://gh/o/r/pull/5"); err != nil {
		t.Fatalf("2xx must be nil; got %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/outputs/oid-1/attribution" {
		t.Errorf("wrong request: %s %s", gotMethod, gotPath)
	}
	if gotAuth != "Bearer tlv_secret" {
		t.Errorf("auth = %q, want Bearer tlv_secret", gotAuth)
	}
	if !strings.Contains(gotBody, `"target_kind":"pr"`) || !strings.Contains(gotBody, `"target_ref":"https://gh/o/r/pull/5"`) {
		t.Errorf("body = %q, want target_kind+target_ref", gotBody)
	}

	status = 409
	// A 409 (already attributed to a DIFFERENT ref) now returns the ErrAttributionConflict
	// sentinel so the caller can LOG it — still non-fatal / success-equivalent (not a hard error).
	if err := c.ReportAttribution(context.Background(), "oid-2", "pr", "ref"); !errors.Is(err, ErrAttributionConflict) {
		t.Errorf("409 must return ErrAttributionConflict (logged, non-fatal); got %v", err)
	}

	status = 500
	if err := c.ReportAttribution(context.Background(), "oid-3", "pr", "ref"); err == nil {
		t.Error("500 must be an error")
	}

	if err := c.ReportAttribution(context.Background(), "", "pr", "ref"); err != nil {
		t.Errorf("empty output_id must be a no-op nil; got %v", err)
	}
	if err := New("", "").ReportAttribution(context.Background(), "oid", "pr", "ref"); err != nil {
		t.Errorf("unconfigured client must be a no-op nil; got %v", err)
	}
}
