// Package execguard censuses every place in this repository that hands a
// command STRING to a shell interpreter, and pins that set.
//
// ⚠ WHY A CENSUS AND NOT ANOTHER CHECK. cmdguard decides what a model-authored
// command may do; W4.34 measured that its Go and TypeScript ports return the
// same verdict on 6,335 inputs, decisions and reasons alike. **Two guards that
// agree perfectly are worth exactly what the ungated call path allows.** Both
// cmdguard files describe their purpose in terms of one caller each — Go's says
// "this package's caller", TypeScript's names loop-tools.ts — and NEITHER
// states that it is the only way to a shell. Nothing checked whether it was.
//
// MEASURED 2026-08-28 (tab-n2f7, W4.35) by walking every caller of every
// shell-reaching site in both trees: every model-authored path IS gated today.
//
//	agent/internal/runner/executor.go   sh -c  <- agentloop/tools.go gates it with
//	                                             cmdguard.Check; the self-heal loop
//	                                             passes runner.DetectBuildCommand's
//	                                             literal or the --heal-cmd flag
//	agent/internal/shell/generator.go   sh -c  <- `shell --run` confirms interactively,
//	                                             fail-closed (default N, EOF aborts);
//	                                             shell.IsCommandSafe beside it is an
//	                                             ADVISORY denylist and says so
//	extension/.../loop-tools.ts         sh -c  <- cmdguard-pure check(), and it fails
//	                                             CLOSED with no interactive surface:
//	                                             `confirm === undefined` refuses
//	extension/src/agent/heal.ts         sh -c  <- detectBuildCommand's on-disk marker
//
// This file does not re-check those gates. It pins the POPULATION, so that the
// NEXT shell-reaching site cannot be added without someone deciding which of
// the four it resembles. A gate is only worth the paths it is on, and a new
// path is exactly what nothing here could previously see.
//
// ⚠ ARGV-FORM EXECUTION IS DELIBERATELY OUT OF POPULATION, and that is a real
// boundary rather than a convenience: exec.Command("git", args...) and Node's
// execFile pass an argument vector, so no shell parses the string and none of
// cmdguard's reasoning applies. internal/git, internal/sidecar, artifact_commit
// and the extension's git callers are all argv-form. If one of them ever grows
// a shell interpreter it enters this census by construction, because the census
// keys on the INTERPRETER and not on the file.
package execguard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// shellInterpreters are the programs that take a command STRING and parse it.
// A site passing one of these is in the census whatever else it does.
var shellInterpreters = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true,
	"powershell": true, "pwsh": true, "cmd": true, "cmd.exe": true,
}

// expected pins how many shell-reaching sites each file contains.
//
// ⚠ IT IS A COUNT PER FILE, NOT A file:line SET, AND THE CHOICE IS LOAD-BEARING
// IN BOTH DIRECTIONS. Pinning line numbers would red on every unrelated edit
// above a site and get relaxed away. Pinning file NAMES alone would miss a
// SECOND site added to a file already on the list — which is the cheapest way
// to add an ungated path. The count catches both a new file and a new site in
// an old one, and ignores movement.
var expected = map[string]int{
	// Go: POSIX + PowerShell branches of the same call.
	"agent/internal/runner/executor.go": 2,
	"agent/internal/shell/generator.go": 2,
	// TypeScript: same two-branch shape.
	"extension/src/agent/loop-tools.ts": 2,
	"extension/src/agent/heal.ts":       2,
}

// repoRoot walks up for the directory holding both trees. ⚠ IT FAILS LOUDLY
// rather than returning an empty root: a census that scanned nothing would pass
// unconditionally, which is precisely the defect this file exists to prevent.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if fi, err := os.Stat(filepath.Join(dir, "agent", "go.mod")); err == nil && !fi.IsDir() {
			if _, err := os.Stat(filepath.Join(dir, "extension", "src")); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("repository root (holding agent/go.mod and extension/src) not found above the test's cwd — this census would have scanned nothing")
	return ""
}

// goShellSites parses each Go file and counts calls to exec.Command /
// exec.CommandContext whose program argument is a shell interpreter.
//
// ⚠ AST, NOT grep: a census by grep is a census of SPELLINGS. A commented-out
// call, a shell name inside a string, and a real call all match the same
// pattern, and the failure direction is a site that reads as covered.
func goShellSites(t *testing.T, root string) map[string]int {
	t.Helper()
	found := map[string]int{}
	fset := token.NewFileSet()
	err := filepath.Walk(filepath.Join(root, "agent"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "vendor" || info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("parsing %s: %v", path, perr)
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "exec" {
				return true
			}
			if sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext" {
				return true
			}
			for _, arg := range call.Args {
				lit, ok := arg.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				v, uerr := strconv.Unquote(lit.Value)
				if uerr != nil {
					continue
				}
				if shellInterpreters[filepath.Base(v)] {
					found[rel]++
					return true
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking agent/: %v", err)
	}
	return found
}

// tsSpawnShell matches a Node child_process call whose program is a shell.
// Comment lines are stripped first for the same reason the Go side uses an AST.
var tsSpawnShell = regexp.MustCompile(`\b(?:spawn|spawnSync|execFile|execFileSync)\s*\(\s*"([^"]+)"`)

// tsShellString matches child_process.exec/execSync, which run their argument
// through a shell BY DEFINITION — there is no program argument to inspect.
var tsShellString = regexp.MustCompile(`(?:^|[^.\w])(?:exec|execSync)\s*\(`)

func tsShellSites(t *testing.T, root string) map[string]int {
	t.Helper()
	found := map[string]int{}
	err := filepath.Walk(filepath.Join(root, "extension", "src"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".test.ts") {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Fatalf("reading %s: %v", path, rerr)
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		// Only files that import child_process can reach a shell through it;
		// this keeps a regexp named `exec` in an unrelated file (pr-pure.ts
		// runs RegExp.exec) out of the census.
		src := string(b)
		if !strings.Contains(src, "child_process") {
			return nil
		}
		for _, line := range strings.Split(src, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") {
				continue
			}
			for _, m := range tsSpawnShell.FindAllStringSubmatch(line, -1) {
				if shellInterpreters[filepath.Base(m[1])] {
					found[rel]++
				}
			}
			if tsShellString.MatchString(line) && !strings.Contains(line, "execFile") &&
				!strings.Contains(line, "RegExp") && !strings.Contains(line, ".exec(") {
				found[rel]++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking extension/src: %v", err)
	}
	return found
}

// TestEveryShellReachingSiteIsAccountedFor is the load-bearing assertion.
func TestEveryShellReachingSiteIsAccountedFor(t *testing.T) {
	root := repoRoot(t)
	got := map[string]int{}
	for f, n := range goShellSites(t, root) {
		got[f] += n
	}
	for f, n := range tsShellSites(t, root) {
		got[f] += n
	}

	// ⚠ ABSENCE FAILS. A scanner that matched nothing — a moved tree, a broken
	// pattern, a walk that never ran — would otherwise agree with an empty
	// expectation and report the safest possible product.
	if len(got) == 0 {
		t.Fatal("the census found ZERO shell-reaching sites. This repository hands command strings " +
			"to `sh -c` in at least four places, so the scanner is broken, not the tree.")
	}

	var files []string
	for f := range got {
		files = append(files, f)
	}
	for f := range expected {
		if _, ok := got[f]; !ok {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	seen := map[string]bool{}
	for _, f := range files {
		if seen[f] {
			continue
		}
		seen[f] = true
		want, pinned := expected[f]
		switch {
		case !pinned:
			t.Errorf("NEW shell-reaching site(s) in %s (%d found, not pinned).\n"+
				"Something new hands a command STRING to a shell. Decide which of the four "+
				"accounted-for paths it resembles — cmdguard-gated, interactively confirmed, "+
				"or a detected build command — and record the answer in execguard's expected "+
				"table. Do not simply add the count.", f, got[f])
		case got[f] == 0:
			t.Errorf("%s no longer contains a shell-reaching site (pinned at %d).\n"+
				"If it was removed or converted to argv form, drop it from the expected "+
				"table in the same commit — a pin for a site that is gone is a pin that "+
				"cannot fail.", f, want)
		case got[f] != want:
			t.Errorf("%s has %d shell-reaching site(s), pinned at %d.\n"+
				"A second site in an already-listed file is the cheapest way to add an "+
				"ungated path, which is exactly why this census counts per file rather "+
				"than listing file names.", f, got[f], want)
		}
	}
}

// TestTheCensusCanSeeAShellSiteAtAll is the armed-probe control, kept in the
// tree rather than left in a one-off harness.
//
// ⚠ IT EXISTS BECAUSE THE ASSERTION ABOVE IS A COMPARISON AGAINST A TABLE, AND
// A SCANNER THAT RETURNS NOTHING AGREES WITH AN EMPTY TABLE. The population
// check catches a total zero; this catches the subtler case where the Go
// scanner works and the TypeScript one silently does not, or the reverse —
// each side must independently find something.
func TestTheCensusCanSeeAShellSiteAtAll(t *testing.T) {
	root := repoRoot(t)
	if n := len(goShellSites(t, root)); n == 0 {
		t.Error("the Go scanner found no shell-reaching site; agent/internal/runner and " +
			"agent/internal/shell both call exec.CommandContext(\"sh\", \"-c\", …)")
	}
	if n := len(tsShellSites(t, root)); n == 0 {
		t.Error("the TypeScript scanner found no shell-reaching site; loop-tools.ts and " +
			"heal.ts both call spawn(\"sh\", [\"-c\", …])")
	}
}
