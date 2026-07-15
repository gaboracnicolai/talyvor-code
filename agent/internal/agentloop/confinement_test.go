package agentloop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoop_ConfinementHolds_UnderAdversarialModel — the security-critical proof: a
// model that DELIBERATELY tries to read and write OUTSIDE the repo root gets nothing.
// Every escaping tool call returns a refusal observation (the loop re-plans, never
// crashes), no outside file is read (the secret never enters the transcript), and no
// outside file is created or overwritten. S11 holds on read AND edit under the loop.
func TestLoop_ConfinementHolds_UnderAdversarialModel(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Dir(root)
	secret := filepath.Join(parent, "SECRET_OUTSIDE.txt")
	if err := os.WriteFile(secret, []byte("TOP_SECRET_VALUE"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(secret) })
	evil := filepath.Join(parent, "evil_created.txt")
	t.Cleanup(func() { os.Remove(evil) })

	step := 0
	model := ModelFunc(func(_ context.Context, _ []Message) (string, error) {
		step++
		switch step {
		case 1:
			return `{"tool":"read_file","args":{"path":"../SECRET_OUTSIDE.txt"}}`, nil // read escape
		case 2:
			return `{"tool":"edit_file","args":{"path":"../SECRET_OUTSIDE.txt","content":"OVERWRITTEN"}}`, nil // overwrite escape
		case 3:
			return `{"tool":"edit_file","args":{"path":"../evil_created.txt","content":"evil"}}`, nil // create-outside escape
		default:
			return `{"tool":"done","args":{"summary":"could not escape"}}`, nil
		}
	})
	ag := New(model, DefaultTools(root, nil), Config{MaxSteps: 10})
	res, err := ag.Run(context.Background(), "read the secret outside the repo")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Done {
		t.Errorf("loop must survive escaping tool calls and finish; got %+v", res)
	}

	// The secret must NEVER appear in the transcript (the read was refused, not leaked).
	full := concatMsgs(res.Transcript)
	if strings.Contains(full, "TOP_SECRET_VALUE") {
		t.Error("S11 BREACH: the out-of-root secret leaked into the loop transcript")
	}
	if !strings.Contains(full, "outside root") {
		t.Error("an escaping read/edit must surface a refusal observation")
	}
	// The secret must be UNCHANGED on disk, and no evil file created.
	if b, _ := os.ReadFile(secret); string(b) != "TOP_SECRET_VALUE" {
		t.Error("S11 BREACH: the out-of-root file was overwritten by the loop")
	}
	if _, err := os.Stat(evil); err == nil {
		t.Error("S11 BREACH: the loop created a file outside the root")
	}
	if len(res.EditedFiles) != 0 {
		t.Errorf("no edit should have succeeded; EditedFiles=%v", res.EditedFiles)
	}
}

// TestRunTool_ExecutesInWorkspaceRoot — run's working directory is the repo root:
// a command that writes a relative file lands INSIDE the root, confirming run is
// rooted (not the process cwd).
func TestRunTool_ExecutesInWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	tool := NewRunTool(root)
	args, _ := json.Marshal(map[string]any{"cmd": "printf hi > run_marker.txt"})
	if _, err := tool.Run(context.Background(), args); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "run_marker.txt")); err != nil {
		t.Errorf("run must execute in the repo root; marker not found there: %v", err)
	}
}
