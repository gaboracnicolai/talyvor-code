package agentloop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/talyvor/code/internal/codebase"
)

// fakeRetriever returns canned chunks for the search tool (no index/embedder).
type fakeRetriever struct{ out []codebase.RetrievedChunk }

func (f fakeRetriever) Retrieve(context.Context, string, int) ([]codebase.RetrievedChunk, error) {
	return f.out, nil
}

func jsonArgs(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestReadTool_ConfinedRead — read_file returns an in-root file's content and
// REFUSES a path that escapes the repo root (S11).
func TestReadTool_ConfinedRead(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.go"), []byte("package main\n// HELLO_MARKER\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewReadTool(root)
	obs, err := tool.Run(context.Background(), jsonArgs(t, map[string]any{"path": "hello.go"}))
	if err != nil || !strings.Contains(obs, "HELLO_MARKER") {
		t.Errorf("read must return content; obs=%q err=%v", obs, err)
	}
	if _, err := tool.Run(context.Background(), jsonArgs(t, map[string]any{"path": "../escape.txt"})); err == nil {
		t.Error("read_file MUST refuse a path outside the root (S11)")
	}
}

// TestEditTool_ConfinedWrite_Diffs — edit_file writes an in-root file (confined),
// returns a diff, and the change lands on disk; a path escape is refused.
func TestEditTool_ConfinedWrite_Diffs(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.go"), []byte("package x\nfunc A(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewEditTool(root)
	obs, err := tool.Run(context.Background(), jsonArgs(t, map[string]any{"path": "x.go", "content": "package x\nfunc A(){}\nfunc B(){}\n"}))
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if !strings.Contains(obs, "x.go") || !strings.Contains(obs, "func B") {
		t.Errorf("edit observation must name the file + show the diff; got %q", obs)
	}
	got, _ := os.ReadFile(filepath.Join(root, "x.go"))
	if !strings.Contains(string(got), "func B(){}") {
		t.Error("edit must apply to disk")
	}
	if _, err := tool.Run(context.Background(), jsonArgs(t, map[string]any{"path": "../evil.go", "content": "x"})); err == nil {
		t.Error("edit_file MUST refuse a path outside the root (S11)")
	}
	// The escaping write must NOT have created a file outside the root.
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "evil.go")); err == nil {
		t.Error("edit_file wrote outside the root — S11 breached")
	}
}

// TestRunTool_CapturesExitAndOutput — run executes in the root and returns exit code
// + captured output; a non-zero exit is captured, not an error (the model re-plans).
func TestRunTool_CapturesExitAndOutput(t *testing.T) {
	root := t.TempDir()
	tool := NewRunTool(root)
	obs, err := tool.Run(context.Background(), jsonArgs(t, map[string]any{"cmd": "echo RUN_MARKER"}))
	if err != nil || !strings.Contains(obs, "RUN_MARKER") || !strings.Contains(obs, "exit 0") {
		t.Errorf("run must capture stdout + exit 0; obs=%q err=%v", obs, err)
	}
	obs2, err := tool.Run(context.Background(), jsonArgs(t, map[string]any{"cmd": "exit 3"}))
	if err != nil {
		t.Errorf("a non-zero exit must be an OBSERVATION, not a tool error; err=%v", err)
	}
	if !strings.Contains(obs2, "exit 3") {
		t.Errorf("run must capture a non-zero exit; obs=%q", obs2)
	}
}

// TestSearchTool_FormatsRetrieval — search_codebase runs semantic retrieval and
// formats each hit with its file:span + content (the REAL index, not path-substring).
func TestSearchTool_FormatsRetrieval(t *testing.T) {
	ret := fakeRetriever{out: []codebase.RetrievedChunk{
		{Chunk: codebase.Chunk{File: "internal/auth/login.go", StartLine: 10, EndLine: 20, Content: "func Login() SEARCH_HIT"}, Score: 0.9},
	}}
	tool := NewSearchTool(ret, 5)
	obs, err := tool.Run(context.Background(), jsonArgs(t, map[string]any{"query": "authentication"}))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(obs, "internal/auth/login.go") || !strings.Contains(obs, "10-20") || !strings.Contains(obs, "SEARCH_HIT") {
		t.Errorf("search must format file:span + content; got %q", obs)
	}
}

// TestRegistry_Dispatch — the registry dispatches by tool name; an unknown tool is a
// clean error the loop can surface as an observation.
func TestRegistry_Dispatch(t *testing.T) {
	root := t.TempDir()
	reg := Registry{}
	reg.Register(NewRunTool(root))
	if _, err := reg.Dispatch(context.Background(), "run", jsonArgs(t, map[string]any{"cmd": "echo ok"})); err != nil {
		t.Errorf("dispatch known tool: %v", err)
	}
	if _, err := reg.Dispatch(context.Background(), "nonexistent", nil); err == nil {
		t.Error("dispatch of an unknown tool must error")
	}
	if names := reg.Names(); len(names) != 1 || names[0] != "run" {
		t.Errorf("Names() = %v, want [run]", names)
	}
}
