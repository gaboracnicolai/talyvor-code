package agentloop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeGoModule writes a tiny buildable module whose Add uses the given body.
func writeGoModule(t *testing.T, root, addBody string) {
	t.Helper()
	w := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("go.mod", "module m\n\ngo 1.25\n")
	w("math.go", "package m\n\nfunc Add(a, b int) int { "+addBody+" }\n")
	w("math_test.go", "package m\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 2) != 4 {\n\t\tt.Fatalf(\"Add(2,2)=%d want 4\", Add(2, 2))\n\t}\n}\n")
}

func concatMsgs(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

// TestLoop_ObservesFailure_ReplansToFix — THE headline proof. A real buggy module
// (Add subtracts) fails its test. The agent must RUN the test, OBSERVE the failure,
// and re-plan to a fix — the exact capability the single-pass pipeline lacks (it
// generates once, blind to any test result). The model here is a pure function of
// the transcript: it emits the fix ONLY when the last observation is a FAILING run,
// so a correct final file proves the failure was observed and drove the re-plan.
func TestLoop_ObservesFailure_ReplansToFix(t *testing.T) {
	root := t.TempDir()
	writeGoModule(t, root, "return a - b") // BUG: subtracts

	var sawWhenFixing string
	model := ModelFunc(func(_ context.Context, msgs []Message) (string, error) {
		last := msgs[len(msgs)-1].Content
		switch {
		case strings.HasPrefix(last, "[run]") && strings.Contains(last, "exit 0"):
			return `{"tool":"done","args":{"summary":"tests pass"}}`, nil
		case strings.HasPrefix(last, "[run]"): // a FAILING run — re-plan to a fix
			sawWhenFixing = concatMsgs(msgs)
			return `{"tool":"edit_file","args":{"path":"math.go","content":"package m\n\nfunc Add(a, b int) int { return a + b }\n"}}`, nil
		case strings.HasPrefix(last, "[edit_file]"): // after editing, verify
			return `{"tool":"run","args":{"cmd":"go test ./..."}}`, nil
		default: // initial: observe the current state
			return `{"tool":"run","args":{"cmd":"go test ./..."}}`, nil
		}
	})

	ag := New(model, DefaultTools(root, nil), Config{MaxSteps: 12})
	res, err := ag.Run(context.Background(), "make the tests pass")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Done || res.Stop != StopDone {
		t.Fatalf("agent should finish by fixing + verifying; got %+v", res)
	}
	// The file is actually corrected on disk.
	got, _ := os.ReadFile(filepath.Join(root, "math.go"))
	if !strings.Contains(string(got), "return a + b") {
		t.Errorf("math.go must be fixed; got:\n%s", got)
	}
	// The fix was decided AFTER observing the failing test — proof of observe→re-plan,
	// not a blind retry.
	if !strings.Contains(sawWhenFixing, "exit 1") && !strings.Contains(sawWhenFixing, "FAIL") {
		t.Errorf("the fix must have been driven by an OBSERVED test failure; transcript at fix time did not contain the failure")
	}
	if len(res.EditedFiles) != 1 || res.EditedFiles[0] != "math.go" {
		t.Errorf("edited files = %v, want [math.go]", res.EditedFiles)
	}
}

// TestLoop_CrossFile_EditBDependsOnReadA — cross-file context the blind pipeline
// can't do: server.go must use a constant that lives ONLY in config.go. The agent
// READS config.go, learns the value, and EDITS server.go with it. The constant is an
// unusual number (47213) present nowhere else, so a correct edit proves the value
// flowed A→B through a read the loop enabled. (The old single-pass generateChange
// gets only the target file + task — it never reads config.go, so it cannot know
// 47213.)
func TestLoop_CrossFile_EditBDependsOnReadA(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "config.go"), []byte("package m\n\nconst Port = 47213\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "server.go"), []byte("package m\n\nfunc Addr() string { return \"\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	readConfigBeforeEdit := false
	model := ModelFunc(func(_ context.Context, msgs []Message) (string, error) {
		last := msgs[len(msgs)-1].Content
		switch {
		case strings.Contains(last, "Port = 47213"): // just READ config.go — learned the value
			readConfigBeforeEdit = true
			port := extractAfter(last, "Port = ")
			return `{"tool":"edit_file","args":{"path":"server.go","content":"package m\n\nimport \"strconv\"\n\nfunc Addr() string { return \":\" + strconv.Itoa(` + port + `) }\n"}}`, nil
		case strings.HasPrefix(last, "[edit_file]"):
			return `{"tool":"done","args":{"summary":"server.go now uses config.go's port"}}`, nil
		default: // initial: read the file that holds the port
			return `{"tool":"read_file","args":{"path":"config.go"}}`, nil
		}
	})

	ag := New(model, DefaultTools(root, nil), Config{MaxSteps: 10})
	res, err := ag.Run(context.Background(), "make Addr() return config.go's port")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Done {
		t.Fatalf("expected done; got %+v", res)
	}
	if !readConfigBeforeEdit {
		t.Error("the agent must READ config.go before editing server.go")
	}
	got, _ := os.ReadFile(filepath.Join(root, "server.go"))
	if !strings.Contains(string(got), "47213") {
		t.Errorf("server.go must use the port learned from config.go (47213); got:\n%s", got)
	}
}

// extractAfter returns the leading run of digits following the first occurrence of
// sep (for pulling "47213" out of "const Port = 47213").
func extractAfter(s, sep string) string {
	i := strings.Index(s, sep)
	if i < 0 {
		return ""
	}
	rest := s[i+len(sep):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	return rest[:end]
}
