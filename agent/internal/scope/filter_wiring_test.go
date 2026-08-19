package scope_test

// WHAT THIS FILE PINS: that what the product TELLS A USER a scope does to their context is what a
// scope MEASURABLY does to their context.
//
// ⚠ THE MEASURED FACT, AND IT IS NOT WHAT THE PRODUCT SAID. `.talyvor-scopes` is described in this
// package's own doc comment as "applied as a file filter so the agent's context-discovery doesn't
// drift into unrelated code", and both front ends print "All files in context" when a scope is
// CLEARED — a sentence whose only meaning is that not all files were in context while it was set.
// Measured end to end below through the exact call `runAgent` makes at cmd/agent/main.go:791: with
// a scope active whose single include pattern is `internal/auth/**`, the indexed population the
// planner is handed is byte-for-byte the population it is handed with no scope at all. The excluded
// file is in the prompt. It always was.
//
// ⚠ WHY THE PROSE IS THE FIX AND THE WIRING IS NOT. FilterFiles and GetScopedFiles are correct
// functions; nothing calls them. Calling them is not a one-line repair — there are five production
// IndexDirectory sites (cmd/agent/main.go:791, internal/projectctx/loader.go:242,
// internal/mcp/server.go:164 and :1094, internal/codebase/semindex_build.go:116) and they are not
// one decision. Narrowing the semantic-index BUILD would write a scope-shaped hole into a cache
// that outlives the scope and is reused by every other one; narrowing the MCP server would change
// what an external client sees based on a CLI state it never set. Which of those five a scope
// should narrow is a product decision, so this session measured it and did not guess it. What is
// not a decision is a false sentence in a user's terminal.
//
// ⚠ THE PAIR BELOW IS THE POINT, NOT EITHER TEST ALONE. TestActiveScopeDoesNotNarrow pins the
// measured behaviour; TestScopeFilterClaimMatchesItsWiring pins the prose against a CENSUS of real
// callers taken from the filesystem. Wire a caller and the second test demands the prose come back;
// re-add the claim without wiring and it fails today. Neither direction is green by default.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/talyvor/code/internal/codebase"
	"github.com/talyvor/code/internal/scope"
)

// workspace lays down a two-area tree plus a catalogue whose only scope includes exactly one of
// them, and marks that scope active — the state a user is in after `talyvor-code scope use auth`.
func workspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range []string{
		"internal/auth/token.go",
		"internal/billing/invoice.go",
	} {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cat := `{"auth":{"name":"Authentication","includes":["internal/auth/**"],"focus":"tokens"}}` + "\n"
	if err := os.WriteFile(filepath.Join(root, scope.ScopesFileName), []byte(cat), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, scope.ActiveScopeFileName), []byte("auth\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func indexedPaths(t *testing.T, root string) []string {
	t.Helper()
	// The exact call cmd/agent/main.go:791 makes to build the planner's view of the codebase.
	idx, err := codebase.IndexDirectory(root, codebase.DefaultMaxFiles)
	if err != nil {
		t.Fatalf("IndexDirectory: %v", err)
	}
	out := make([]string, 0, len(idx.Files))
	for _, f := range idx.Files {
		out = append(out, filepath.ToSlash(f.Path))
	}
	return out
}

// TestActiveScopeDoesNotNarrow is the measurement, stated as the fact it is rather than as an
// aspiration. It fails the day a scope starts narrowing the planner's population — which is the
// day the sentences this file also pins have to come back.
func TestActiveScopeDoesNotNarrow(t *testing.T) {
	root := workspace(t)

	sm := scope.NewManager(root)
	if err := sm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := sm.LoadActive(); err != nil {
		t.Fatalf("LoadActive: %v", err)
	}
	// The instrument reads: the scope really is active, and its filter really would drop the
	// billing file if anything called it. Without these two the assertion below could pass because
	// the catalogue failed to load, which looks identical from the outside.
	if sm.ActiveName() != "auth" {
		t.Fatalf("fixture is not in the state under test: active scope = %q, want %q", sm.ActiveName(), "auth")
	}
	if got := sm.FilterFiles([]codebase.FileInfo{{Path: "internal/billing/invoice.go"}}); len(got) != 0 {
		t.Fatalf("fixture is not in the state under test: FilterFiles kept %v, so this scope does not exclude billing", got)
	}

	paths := indexedPaths(t, root)
	if len(paths) == 0 {
		t.Fatal("indexed nothing — the walk did not reach the fixture, so nothing below is measured")
	}
	var sawAuth, sawBilling bool
	for _, p := range paths {
		switch {
		case strings.HasPrefix(p, "internal/auth/"):
			sawAuth = true
		case strings.HasPrefix(p, "internal/billing/"):
			sawBilling = true
		}
	}
	if !sawAuth {
		t.Fatalf("indexed %v — the INCLUDED area is missing, so this fixture measures nothing", paths)
	}
	if !sawBilling {
		t.Fatalf("the active scope excluded internal/billing from the planner's population (%v).\n"+
			"That is the behaviour the product describes, and it is NEW. The user-facing sentences "+
			"pinned by TestScopeFilterClaimMatchesItsWiring were corrected on the measurement that "+
			"this did NOT happen — go put them back.", paths)
	}
}

// ─── the claim/wiring agreement guard ────────────────────────────────────────
//
// The population is taken from the filesystem, never from a list of names in this file. A guard
// that decides its own population by naming things is a guard that goes green when the thing it
// was watching moves — the failure this repo has already paid for twice (W4.12, W4.15).

// repoRoot walks up from this package to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("go.mod not found above the scope package — the census below would scan nothing")
	return ""
}

// productionFilterCallers censuses every non-test .go file in the module for a call to the scope
// package's two filtering entry points, excluding the package's own definitions.
func productionFilterCallers(t *testing.T, root string) (callers []string, filesScanned int) {
	t.Helper()
	selfDir := filepath.Join(root, "internal", "scope")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		filesScanned++
		if filepath.Dir(path) == selfDir {
			return nil // the definitions themselves are not callers
		}
		rel, _ := filepath.Rel(root, path)
		for _, sym := range []string{".FilterFiles(", ".GetScopedFiles("} {
			if strings.Contains(string(body), sym) {
				callers = append(callers, filepath.ToSlash(rel)+" → "+strings.Trim(sym, ".("))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("census walk: %v", err)
	}
	return callers, filesScanned
}

// narrowingClaims are the user-facing and doc sentences that only mean something if a scope
// narrows the file population. Each is located by (file, substring); a substring that no longer
// appears in its file is a CENSUS FAILURE, not a pass — otherwise correcting a file's wording by
// accident would silently retire the check that watches it.
var narrowingClaims = []struct {
	file   string // relative to the repo root's PARENT (the git root), so both runtimes are reachable
	claim  string
	anchor string // a sentence that must still be present, proving the file was read
}{
	{
		file:   "agent/cmd/agent/main.go",
		claim:  "All files in context",
		anchor: "Scope cleared.",
	},
	{
		file:   "extension/src/commands/scope-command.ts",
		claim:  "All files in context",
		anchor: "Scope cleared.",
	},
	{
		file:   "agent/internal/scope/scope.go",
		claim:  "applied as a file filter",
		anchor: "Package scope",
	},
}

func TestScopeFilterClaimMatchesItsWiring(t *testing.T) {
	modRoot := repoRoot(t)
	gitRoot := filepath.Dir(modRoot)

	callers, scanned := productionFilterCallers(t, modRoot)
	if scanned < 20 {
		t.Fatalf("census scanned only %d non-test .go files — it is not reading the module, so a "+
			"caller count of %d means nothing", scanned, len(callers))
	}
	t.Logf("census: %d non-test .go files scanned, %d production caller(s) of the scope filter: %v",
		scanned, len(callers), callers)

	for _, c := range narrowingClaims {
		p := filepath.Join(gitRoot, filepath.FromSlash(c.file))
		body, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("%s: cannot read the file this check exists to watch: %v", c.file, err)
			continue
		}
		// Vacuity control. Without it, a rename or a rewrite turns this check into a green that
		// asserts nothing — the exact shape three sessions in this queue have shipped and had to
		// have caught for them by a positive control.
		if !strings.Contains(string(body), c.anchor) {
			t.Errorf("%s: anchor %q is gone, so this check is no longer reading the code it names. "+
				"Re-point it before trusting its verdict.", c.file, c.anchor)
			continue
		}
		claimed := strings.Contains(string(body), c.claim)
		switch {
		case claimed && len(callers) == 0:
			t.Errorf("%s states %q, and NOTHING in this module calls the scope filter "+
				"(census: %d non-test .go files, 0 callers). The sentence tells a user their "+
				"context was narrowed while it was not. Either wire FilterFiles/GetScopedFiles "+
				"into the population it describes, or say what actually happens.",
				c.file, c.claim, scanned)
		case !claimed && len(callers) > 0:
			t.Errorf("%s no longer states %q, but the scope filter now HAS production callers (%v). "+
				"The behaviour came back and the sentence describing it did not.",
				c.file, c.claim, callers)
		}
	}
}
