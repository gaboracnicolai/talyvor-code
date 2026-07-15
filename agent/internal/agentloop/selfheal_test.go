package agentloop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/talyvor/code/internal/codebase"
)

// TestLoop_SelfHeal_UsesSearchToFindFix — self-heal is NATIVE loop behavior AND
// richer than the old bolted-on healer. The old healer (≤3) only regenerated the
// changed file from the raw error text; it could not search the codebase. Here a
// failing test drives the agent to SEARCH for the right helper, learn it, and edit —
// a heal that uses the full tool set. Proves run→observe→SEARCH→edit→run→done.
func TestLoop_SelfHeal_UsesSearchToFindFix(t *testing.T) {
	root := t.TempDir()
	w := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("go.mod", "module m\n\ngo 1.25\n")
	// Add mistakenly calls sub (compiles — sub exists — but the test fails).
	w("math.go", "package m\n\nfunc Add(a, b int) int { return sub(a, b) }\n")
	w("helper.go", "package m\n\n// add returns the sum of a and b.\nfunc add(a, b int) int { return a + b }\n\nfunc sub(a, b int) int { return a - b }\n")
	w("math_test.go", "package m\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 2) != 4 {\n\t\tt.Fatalf(\"Add(2,2)=%d want 4\", Add(2, 2))\n\t}\n}\n")

	// The retriever surfaces the add() helper when the agent searches for it.
	ret := fakeRetriever{out: []codebase.RetrievedChunk{
		{Chunk: codebase.Chunk{File: "helper.go", StartLine: 3, EndLine: 4, Content: "// add returns the sum of a and b.\nfunc add(a, b int) int { return a + b }"}, Score: 0.95},
	}}

	searchedBeforeFix := false
	model := ModelFunc(func(_ context.Context, msgs []Message) (string, error) {
		last := msgs[len(msgs)-1].Content
		switch {
		case strings.HasPrefix(last, "[run]") && strings.Contains(last, "exit 0"):
			return `{"tool":"done","args":{"summary":"fixed Add to use add()"}}`, nil
		case strings.HasPrefix(last, "[run]"): // failing test → search for the right helper
			return `{"tool":"search_codebase","args":{"query":"addition sum helper function"}}`, nil
		case strings.HasPrefix(last, "[search_codebase]") && strings.Contains(last, "func add"):
			searchedBeforeFix = true
			return `{"tool":"edit_file","args":{"path":"math.go","content":"package m\n\nfunc Add(a, b int) int { return add(a, b) }\n"}}`, nil
		case strings.HasPrefix(last, "[edit_file]"):
			return `{"tool":"run","args":{"cmd":"go test ./..."}}`, nil
		default:
			return `{"tool":"run","args":{"cmd":"go test ./..."}}`, nil
		}
	})

	ag := New(model, DefaultTools(root, ret), Config{MaxSteps: 15})
	res, err := ag.Run(context.Background(), "make the tests pass")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Done || res.Stop != StopDone {
		t.Fatalf("self-heal-via-search should finish; got %+v", res)
	}
	if !searchedBeforeFix {
		t.Error("the heal must have SEARCHED the codebase to find the fix (richer than the old regenerate-only healer)")
	}
	got, _ := os.ReadFile(filepath.Join(root, "math.go"))
	if !strings.Contains(string(got), "add(a, b)") {
		t.Errorf("Add must be healed to call add(); got:\n%s", got)
	}
}
