package cmdguard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ⚠ THE TYPESCRIPT EXTENSION HAS THE SAME TOOL AND NEEDS THE SAME BOUND.
//
// extension/src/agent/loop-tools.ts hands a model-authored string to `sh -c` exactly as this
// package's caller does. This parser cannot be called from there — separate runtimes, and
// compiling it to WASM to gate a VS Code extension's shell calls would put a build toolchain on a
// security path for no proportionate gain. So the design is ported, and the two are held together
// by testdata/cmdguard-corpus.json: both suites read it and must agree on every command.
//
// A guard that only one side enforces is worth what the weaker side enforces, so this test exists
// on THIS side too. If someone widens the Go allowlist without widening the port, this fails.

type corpusCase struct {
	Command  string `json:"command"`
	Decision string `json:"decision"`
	Why      string `json:"why"`
}

type corpus struct {
	Cases          []corpusCase `json:"cases"`
	HonestUserCost struct {
		MinimumUnattended int      `json:"minimumUnattended"`
		Commands          []string `json:"commands"`
	} `json:"honestUserCost"`
}

// loadCorpus walks up for the shared file. ⚠ IT FAILS LOUDLY WHEN THE FILE IS MISSING rather than
// verifying an empty set — a corpus that has moved would otherwise make this pass unconditionally,
// which is exactly how a guard becomes decoration.
func loadCorpus(t *testing.T) corpus {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		p := filepath.Join(dir, "testdata", "cmdguard-corpus.json")
		if _, err := os.Stat(p); err == nil {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("reading %s: %v", p, err)
			}
			var c corpus
			if err := json.Unmarshal(b, &c); err != nil {
				t.Fatalf("parsing %s: %v", p, err)
			}
			if len(c.Cases) < 30 {
				t.Fatalf("only %d cases in %s — too thin to prove parity", len(c.Cases), p)
			}
			return c
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("cmdguard corpus not found above the working directory — this test would have verified nothing")
	return corpus{}
}

func TestSharedCorpus_GoAndTypeScriptAgree(t *testing.T) {
	c := loadCorpus(t)
	seen := map[string]bool{}
	for _, tc := range c.Cases {
		if got := Check(tc.Command).Decision.String(); got != tc.Decision {
			t.Errorf("Check(%q) = %s, want %s — %s\n(the TypeScript port asserts the same case; they have drifted)",
				tc.Command, got, tc.Decision, tc.Why)
		}
		seen[tc.Decision] = true
	}
	for _, d := range []string{"allow", "confirm", "refuse"} {
		if !seen[d] {
			t.Errorf("the corpus contains no %s case", d)
		}
	}
}

// The same measurement the TypeScript side makes, from the same list, so "16/20" means the same
// thing on both sides rather than being two numbers that happen to match.
func TestSharedCorpus_HonestUserCostMatches(t *testing.T) {
	c := loadCorpus(t)
	allowed := 0
	for _, cmd := range c.HonestUserCost.Commands {
		if Check(cmd).Decision == Allow {
			allowed++
		}
	}
	if allowed < c.HonestUserCost.MinimumUnattended {
		t.Errorf("only %d/%d ordinary commands run unattended, below the shared minimum of %d",
			allowed, len(c.HonestUserCost.Commands), c.HonestUserCost.MinimumUnattended)
	}
	t.Logf("HONEST-USER COST (Go): %d/%d ordinary commands unattended; %d need one confirmation",
		allowed, len(c.HonestUserCost.Commands), len(c.HonestUserCost.Commands)-allowed)
}
