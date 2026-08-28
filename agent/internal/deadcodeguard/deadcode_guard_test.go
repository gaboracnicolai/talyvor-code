package deadcodeguard

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// deadcodeguard — no unexported top-level function in this module is declared and never called.
//
// ⚠ WHY THIS EXISTS, AND IT IS A SHARPER STORY THAN "SOMEBODY LEFT DEAD CODE".
// `internal/mcp/server.go` carried this, verbatim:
//
//	// runGit is a thin helper used by tests + tools that need a raw
//	// git output line rather than the wrappers in internal/git.
//	func runGit(args ...string) (string, error) { ... }
//	// Compile-time guard: the unused symbols above are still expected
//	// to compile cleanly under go vet.
//	var _ = runGit
//
// The file said the helper HAS callers and, four lines later, installed the idiom you only need
// when it has NONE. Measured: `runGit` and its `var _` keep-alive arrived in the SAME commit
// (d276e39, "Phase 9 — MCP server with 10 tools"). It was born dead. No caller was ever lost.
//
// ⚠⚠ AND THE DOCSTRING WAS DESCRIBING A DIFFERENT FUNCTION. `cmd/agent/main_test.go:1577` declares
// `func runGit(t *testing.T, dir string, args ...string)` — a genuine test helper with 28 callers,
// in a different package, with a different signature. "used by tests" is TRUE of that one. The MCP
// helper inherited a description of its namesake, which is why the comment reads as a statement of
// fact rather than an aspiration.
//
// ⚠⚠⚠ THE CENSUS THAT FOUND THIS LIED FIRST, THROUGH THAT SAME COLLISION, AND IN THE SAFE-LOOKING
// DIRECTION. Its first version counted references REPO-WIDE by identifier name and reported "no
// dead unexported declarations" — because `main_test.go`'s 28 calls to the OTHER `runGit` were
// counted against `internal/mcp`'s. A name-based join across a package boundary, with no package
// qualifier: the same shape as a captured field with no join key. Only a ground truth established
// by hand beforehand caught it. Hence the rule below counts references ONLY inside the declaring
// package, which is the join key the first version lacked.
//
// SUBJECTS ARE FUNCTIONS, NOT METHODS, AND THAT IS THE POINT OF THE RESTRICTION. A method can
// satisfy an interface and be called with no textual reference to its name anywhere, so a
// name-based rule over methods has a real false-positive class. A package-level unexported
// FUNCTION has no such door: if nothing in its own package names it, nothing can reach it.

var (
	funcDecl   = regexp.MustCompile(`^func ([a-z]\w*)\(`)
	anyDecl    = regexp.MustCompile(`^func (?:\([^)]*\) )?([a-z]\w*)\(`)
	keepAliveP = `^\s*var _ = %s\s*$`
)

// moduleRoot walks up for go.mod and FAILS LOUDLY rather than silently scanning nothing.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("go.mod not found above the working directory — this guard would have scanned nothing")
	return ""
}

// goPackages returns every directory under root holding non-test .go files, with those files.
func goPackages(t *testing.T, root string) map[string][]string {
	t.Helper()
	pkgs := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// testdata is fixtures, not code; vendor and dot-dirs are not ours.
			if n := d.Name(); n == "testdata" || n == "vendor" || (strings.HasPrefix(n, ".") && n != ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			dir := filepath.Dir(path)
			pkgs[dir] = append(pkgs[dir], path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return pkgs
}

func TestNoUnexportedFunctionIsDeclaredAndNeverCalled(t *testing.T) {
	root := moduleRoot(t)
	pkgs := goPackages(t, root)

	type subject struct{ pkg, file, name string }
	var subjects []subject
	// contents is read once per package: the reference scan must see the package's TEST files too,
	// since a helper called only from a test in the same package is reachable and not dead.
	contents := map[string]map[string][]string{}

	for dir, files := range pkgs {
		contents[dir] = map[string][]string{}
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			contents[dir][f] = strings.Split(string(b), "\n")
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue // a dead helper inside a test file is a test-hygiene matter, not shipped code
			}
			for _, line := range contents[dir][f] {
				m := funcDecl.FindStringSubmatch(line)
				if m == nil {
					continue
				}
				// ⚠ THE TWO FUNCTIONS THE GO RUNTIME CALLS AND NO SOURCE LINE DOES.
				// `init` has no call site anywhere by construction, so a name-based rule reports it
				// dead — a guard that reds the moment somebody adds one. MEASURED: this module has
				// no `init` today, so the guard was green here BY LUCK, not by correctness.
				// `main` was worse: it passed only because the token also appears in `package main`,
				// i.e. for a reason that has nothing to do with being called. Excluded by name and
				// stated, rather than left to an accident of tokenisation.
				if m[1] == "init" || m[1] == "main" {
					continue
				}
				subjects = append(subjects, subject{dir, f, m[1]})
			}
		}
	}

	// ⚠ NON-VACUITY FLOORS — AND THEY ARE COLLAPSE FLOORS, NOT RATCHETS. A walk that found no
	// packages, or a regexp that stopped matching declarations, would make the loop below pass by
	// having nothing to check, and would look exactly like a clean module. That is what these catch.
	//
	// ⚠⚠ THE FIRST VERSION PINNED THEM AT THE EXACT MEASURED COUNTS (178 subjects) AND WENT RED ON
	// THE VERY DELETION THIS GUARD EXISTS TO DEMAND. Removing `runGit` took the count to 177 and the
	// floor failed the build — a guard that reds when you comply with it. The floors are therefore
	// set well below the measurement, which is what "the population did not collapse" actually
	// needs; a tight floor here would silently convert this guard into a no-deletions ratchet.
	//
	// Measured by this test when it was written: 23 packages, 177 unexported top-level functions.
	if len(pkgs) < 15 || len(subjects) < 150 {
		t.Fatalf("population collapsed: %d packages, %d unexported top-level functions; "+
			"collapse floors are 15/150 (measured at authoring: 23/177)", len(pkgs), len(subjects))
	}

	var dead []string
	for _, s := range subjects {
		pat := regexp.MustCompile(`\b` + regexp.QuoteMeta(s.name) + `\b`)
		keep := regexp.MustCompile(fmt.Sprintf(keepAliveP, regexp.QuoteMeta(s.name)))
		found := false
		for _, lines := range contents[s.pkg] {
			for _, line := range lines {
				if !pat.MatchString(line) {
					continue
				}
				if m := anyDecl.FindStringSubmatch(line); m != nil && m[1] == s.name {
					continue // the declaration itself is not a call
				}
				if keep.MatchString(line) {
					continue // ⚠ A `var _ = name` KEEP-ALIVE IS NOT A CALLER. It is the marker of one.
				}
				if strings.HasPrefix(strings.TrimSpace(line), "//") {
					continue // nor is a comment — that is the whole defect this guard names
				}
				if strings.HasPrefix(line, "package ") {
					continue // nor is the package clause: `package main` is not a call to main()
				}
				found = true
				break
			}
			if found {
				break
			}
		}
		if !found {
			rel, _ := filepath.Rel(root, s.file)
			dead = append(dead, fmt.Sprintf("%s  (%s)", s.name, rel))
		}
	}

	if len(dead) > 0 {
		sort.Strings(dead)
		t.Fatalf("%d unexported top-level function(s) are declared and never called in their own package.\n"+
			"Delete it, or wire it — but a `var _ = name` keep-alive and a docstring claiming callers is neither:\n  %s",
			len(dead), strings.Join(dead, "\n  "))
	}
}
