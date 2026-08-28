package main

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// EVERY SIDE-EFFECTING BOOLEAN FLAG MUST DEFAULT TO false. Six of them were
// defended by nothing.
//
// MEASURED 2026-08-28 (tab-k2w8, W4.42) by mutation, not by reading
// (~/talyvor-queue/w442-code-defaults-census-k2w8.py). Population, derived by
// parsing: every flag default registered in this file (55) plus every exported
// Default* scalar in internal/ (5). Each was changed to a clearly different
// value of the same type, compiled, and `go test -race -count=1 ./...` re-run.
// Result: 60 sites, 28 CAUGHT, 32 UNPINNED, 0 INVALID.
//
// ⚠ THE HARNESS CARRIED ITS OWN CONTROL AND IT PASSED BEFORE ANY ZERO WAS
// BELIEVED: W4.32 (891473f) pinned `serve`'s -host and -port, so those two had
// to come back CAUGHT. They did. A census whose known-positive rows do not fire
// is reporting nothing.
//
// OF THE TWELVE BOOLEAN FLAGS, ALL TWELVE ALREADY DEFAULT false — the posture is
// correct — and SIX of them were held there by nothing: -yes, -heal, -pr (the
// one on `code`), -pr-draft, -github, -draft. The other six reded some test.
//
// ⚠⚠ THE SHARPEST ROW IS THAT THERE ARE TWO `-pr` FLAGS AND THE DANGEROUS ONE IS
// THE UNDEFENDED ONE. `code`'s -pr *creates a GitHub pull request*; `review`'s
// -pr merely selects the PR diff as the thing to review. The second is defended
// and the first is not. They are indistinguishable by name, which is exactly why
// the census anchored every mutation by LINE NUMBER and why this table is keyed
// on the usage string rather than the flag name.
//
// ⚠ THIS FILE CHANGES NO DEFAULT. Every one is already false. What it adds is
// that flipping one — a single token, on flags that auto-approve edits, open
// pull requests, or post public comments — can no longer pass unnoticed, and
// that a NEW boolean flag has to be classified before it can be added.
//
// WHY A PROPERTY AND NOT A PIN: an equality table over twelve literals would say
// "these are false today". The rule below says something stronger and more
// durable — a flag whose true value takes an outward-facing or hard-to-reverse
// action must be OPT-IN — and it keeps applying to flags nobody has written yet.

// boolFlag is one `fs.BoolVar` registration, keyed by name + the first words of
// its usage string, because `-pr` is registered twice with different meanings.
type boolFlag struct {
	name        string
	usagePrefix string
	sideEffect  bool
	why         string
}

var classifiedBoolFlags = []boolFlag{
	{"iterative", "Use the ITERATIVE", false, "selects a loop strategy; writes nothing on its own"},
	{"dry-run", "Show plan + diffs", false, "strictly reduces what happens"},
	{"yes", "Auto-approve", true, "removes the human approval gate on every edit the agent makes"},
	{"heal", "Run build/test", true, "runs the project's build and test commands, then edits again to fix them"},
	{"pr", "Create a GitHub", true, "opens a pull request on a remote — outward-facing and public"},
	{"pr-draft", "Open the PR as a draft", true, "only meaningful with --pr, and still a remote object"},
	{"pr", "Review the current PR diff", false, "chooses WHICH diff to review locally; creates nothing"},
	{"github", "Post review as a GitHub", true, "posts a public comment on someone's pull request"},
	{"push", "Push after a successful commit", true, "publishes commits to a remote"},
	{"draft", "Open as a draft PR", true, "creates a remote pull request"},
	{"explain", "Explain the command", false, "prints an explanation; runs nothing"},
	{"run", "Execute the command", true, "executes a shell command on the caller's machine"},
}

var boolVarRe = regexp.MustCompile(`fs\.BoolVar\(&\w+,\s*"([^"]+)",\s*(\w+),\s*"([^"]*)"`)

func parseBoolFlags(t *testing.T) [][3]string {
	t.Helper()
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}
	m := boolVarRe.FindAllStringSubmatch(string(src), -1)
	if len(m) == 0 {
		t.Fatal("parsed ZERO fs.BoolVar registrations out of main.go — the parser is broken, " +
			"and a guard that finds nothing to check is the defect it exists to catch")
	}
	out := make([][3]string, 0, len(m))
	for _, g := range m {
		out = append(out, [3]string{g[1], g[2], g[3]})
	}
	return out
}

func classify(name, usage string) *boolFlag {
	for i := range classifiedBoolFlags {
		c := &classifiedBoolFlags[i]
		if c.name == name && strings.HasPrefix(usage, c.usagePrefix) {
			return c
		}
	}
	return nil
}

// TestSideEffectingFlagsAreOptIn is the load-bearing assertion.
func TestSideEffectingFlagsAreOptIn(t *testing.T) {
	for _, f := range parseBoolFlags(t) {
		name, def, usage := f[0], f[1], f[2]
		c := classify(name, usage)
		if c == nil {
			continue // reported by the completeness floor below
		}
		if c.sideEffect && def != "false" {
			t.Errorf("-%s defaults to %s, but it is classified side-effecting: %s.\n"+
				"    A flag whose true value takes an outward-facing or hard-to-reverse action "+
				"must be OPT-IN. If this flag is no longer side-effecting, change its "+
				"classification here and say why — that is the edit this test exists to force.",
				name, def, c.why)
		}
	}
}

// TestEveryBoolFlagIsClassified is the completeness floor. Without it a new
// side-effecting flag could be added and simply not be covered, which is the
// shape the census above was measuring in the first place.
func TestEveryBoolFlagIsClassified(t *testing.T) {
	parsed := parseBoolFlags(t)
	var unclassified []string
	seen := map[string]bool{}
	for _, f := range parsed {
		name, usage := f[0], f[2]
		if c := classify(name, usage); c == nil {
			unclassified = append(unclassified, "-"+name+" ("+truncateUsage(usage)+")")
		} else {
			seen[c.name+"|"+c.usagePrefix] = true
		}
	}
	var stale []string
	for _, c := range classifiedBoolFlags {
		if !seen[c.name+"|"+c.usagePrefix] {
			stale = append(stale, "-"+c.name+" ("+c.usagePrefix+")")
		}
	}
	sort.Strings(unclassified)
	sort.Strings(stale)
	if len(unclassified) > 0 {
		t.Errorf("boolean flags registered in main.go but NOT classified: %v\n"+
			"    Add each to classifiedBoolFlags saying whether its true value takes an "+
			"outward-facing or hard-to-reverse action. An unclassified flag is one nothing "+
			"holds to the opt-in rule.", unclassified)
	}
	if len(stale) > 0 {
		t.Errorf("classified entries with no matching registration in main.go: %v\n"+
			"    Remove them — a classification for a flag that no longer exists is inert.", stale)
	}
	if len(parsed) != len(classifiedBoolFlags) {
		t.Errorf("parsed %d bool flag registrations but the table holds %d — the two must be "+
			"the same population", len(parsed), len(classifiedBoolFlags))
	}
}

func truncateUsage(s string) string {
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}
