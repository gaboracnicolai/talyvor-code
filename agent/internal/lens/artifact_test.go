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

// TestCommitArtifact — the thin Lens caller for the H5 artifact commitment:
// POST /v1/outputs/{id}/artifact {output_path, context_manifest} + Bearer auth. 200 → (committed, nil)
// with committed parsed from the body (append-once: a second commit is 200 committed:false, silent).
// 409 = the output has NO content binding (permanent — Lens captured no output_content_sha256) → the
// ErrArtifactNoContentBinding sentinel so the caller can LOG it; other 4xx/5xx → error; empty id /
// unconfigured → no-op (false, nil). Client-side, NO authority — Lens owns ownership + append-once.
func TestCommitArtifact(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotBody string
	status, respBody := 200, `{"artifact_sha256":"abc","committed":true,"output_id":"oid-1"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
	defer srv.Close()
	c := New(srv.URL, "tlv_secret")

	manifest := []ManifestEntry{{Path: "go.mod", ContentSHA256: "gomodhash"}}
	committed, err := c.CommitArtifact(context.Background(), "oid-1", "main.go", manifest)
	if err != nil || !committed {
		t.Fatalf("200 committed:true must return (true, nil); got (%v, %v)", committed, err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/outputs/oid-1/artifact" {
		t.Errorf("wrong request: %s %s", gotMethod, gotPath)
	}
	if gotAuth != "Bearer tlv_secret" {
		t.Errorf("auth = %q, want Bearer tlv_secret", gotAuth)
	}
	for _, want := range []string{`"output_path":"main.go"`, `"context_manifest":[`, `"path":"go.mod"`, `"content_sha256":"gomodhash"`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("body %q must contain %q", gotBody, want)
		}
	}

	// Append-once second commit: 200 committed:false — success-shaped, not an error.
	respBody = `{"artifact_sha256":"abc","committed":false,"output_id":"oid-1"}`
	if committed, err := c.CommitArtifact(context.Background(), "oid-1", "main.go", manifest); err != nil || committed {
		t.Errorf("200 committed:false must return (false, nil); got (%v, %v)", committed, err)
	}

	// 409 → the no-content-binding sentinel (logged by the caller, never fatal).
	status, respBody = 409, `{"error":"output has no content binding; artifact commitment impossible"}`
	if _, err := c.CommitArtifact(context.Background(), "oid-2", "main.go", manifest); !errors.Is(err, ErrArtifactNoContentBinding) {
		t.Errorf("409 must return ErrArtifactNoContentBinding; got %v", err)
	}

	// Other errors are errors.
	status, respBody = 500, `{}`
	if _, err := c.CommitArtifact(context.Background(), "oid-3", "main.go", manifest); err == nil {
		t.Error("500 must be an error")
	}

	// No-ops.
	if committed, err := c.CommitArtifact(context.Background(), "", "main.go", manifest); err != nil || committed {
		t.Errorf("empty output_id must be a no-op (false, nil); got (%v, %v)", committed, err)
	}
	if committed, err := New("", "").CommitArtifact(context.Background(), "oid", "main.go", manifest); err != nil || committed {
		t.Errorf("unconfigured client must be a no-op (false, nil); got (%v, %v)", committed, err)
	}
}
