package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/talyvor/code/internal/config"
)

// TestRunIterative_CLI_AppliesEditAndFinishes — end-to-end wiring: `run --iterative`
// drives the loop through the real Lens client (mocked). The scripted model edits a
// file then finishes; the CLI must apply the confined edit and report a clean done.
func TestRunIterative_CLI_AppliesEditAndFinishes(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.WriteFile("x.go", []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	replies := []string{
		`{"thought":"add a marker","tool":"edit_file","args":{"path":"x.go","content":"package x\n// ITER_EDIT_MARKER\n"}}`,
		`{"thought":"done","tool":"done","args":{"summary":"added the marker"}}`,
	}
	i := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		text := `{"tool":"done","args":{"summary":"end"}}`
		if i < len(replies) {
			text = replies[i]
			i++
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"usage":   map[string]int{},
		})
	}))
	defer srv.Close()

	cfg := config.Config{LensURL: srv.URL, LensAPIKey: "k", WorkspaceID: "ws", ActiveIssue: "ENG-1", Model: "claude-sonnet-4-6"}
	var out bytes.Buffer
	if err := runAgent(strings.NewReader(""), &out, io.Discard, cfg, []string{"--iterative", "--max-steps", "8", "add a marker to x.go"}); err != nil {
		t.Fatalf("runAgent --iterative: %v", err)
	}

	got, _ := os.ReadFile("x.go")
	if !strings.Contains(string(got), "ITER_EDIT_MARKER") {
		t.Errorf("the iterative loop must apply the confined edit; x.go = %q", got)
	}
	if !strings.Contains(out.String(), "done") {
		t.Errorf("output must report a clean done; got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "x.go") {
		t.Errorf("output must list the edited file; got:\n%s", out.String())
	}
}
