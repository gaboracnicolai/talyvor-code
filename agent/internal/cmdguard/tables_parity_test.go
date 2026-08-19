package cmdguard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// THE CORPUS IS A SAMPLE; THESE TABLES ARE THE POPULATION.
//
// corpus_test.go says "If someone widens the Go allowlist without widening the port, this fails".
// Measured by mutation rather than read: it fails only for entries the corpus happens to have a case
// for. Adding "awk" to pipeFilters on the Go side alone — a straight widening of the allowlist that
// decides what a model-authored shell command may run unattended — left BOTH suites green. 25 of the
// 39 table entries had no corpus case at all: 9 of 18 allowedHeads keys, 7 of 9 pipeFilters, and 9 of
// 12 flagsTakingValue flags.
//
// So the sample is kept (it is the only thing checking that the two parsers AGREE on real commands)
// and the population is pinned beside it. This file asserts the Go tables equal
// testdata/cmdguard-tables.json; extension/src/agent/cmdguard-tables.test.ts asserts the same of the
// TypeScript copies. A one-sided widening now fails on the side that widened, and an edit to the
// manifest alone fails on both.
//
// ⚠ THIS GUARD ADDS NO PERMISSION AND REMOVES NONE. The manifest was generated from the tables as
// they stood, so it changes no verdict; what it changes is whether the next edit can be one-sided.

type tablesManifest struct {
	AllowedHeads     map[string][]string `json:"allowedHeads"`
	PipeFilters      []string            `json:"pipeFilters"`
	FlagsTakingValue map[string][]string `json:"flagsTakingValue"`
}

// loadTables walks up for the shared file and FAILS LOUDLY when it is missing — a manifest that has
// moved would otherwise leave this asserting nothing, which is the failure mode the thing it guards
// already had.
func loadTables(t *testing.T) tablesManifest {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		p := filepath.Join(dir, "testdata", "cmdguard-tables.json")
		if _, err := os.Stat(p); err == nil {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("reading %s: %v", p, err)
			}
			var m tablesManifest
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatalf("parsing %s: %v", p, err)
			}
			if len(m.AllowedHeads) == 0 || len(m.PipeFilters) == 0 || len(m.FlagsTakingValue) == 0 {
				t.Fatalf("%s has an empty table — comparing against it would prove nothing", p)
			}
			return m
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("cmdguard tables manifest not found above the working directory — this test would have verified nothing")
	return tablesManifest{}
}

func keysOf(m map[string]map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func setOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

// equalSets reports the differences BY NAME in both directions. "the tables differ" would tell a
// reader that something moved; naming the entry tells them what a shell may now do.
func equalSets(t *testing.T, what string, got, want []string) {
	t.Helper()
	g, w := map[string]bool{}, map[string]bool{}
	for _, s := range got {
		g[s] = true
	}
	for _, s := range want {
		w[s] = true
	}
	for _, s := range sortedCopy(got) {
		if !w[s] {
			t.Errorf("%s: Go has %q and the shared manifest does not — widen testdata/cmdguard-tables.json AND the TypeScript port, or drop it", what, s)
		}
	}
	for _, s := range sortedCopy(want) {
		if !g[s] {
			t.Errorf("%s: the shared manifest has %q and Go does not — the two guards have drifted", what, s)
		}
	}
}

func TestTables_MatchSharedManifest(t *testing.T) {
	m := loadTables(t)

	equalSets(t, "allowedHeads keys", keysOf(allowedHeads), func() []string {
		out := make([]string, 0, len(m.AllowedHeads))
		for k := range m.AllowedHeads {
			out = append(out, k)
		}
		return out
	}())
	for head, subs := range allowedHeads {
		if want, ok := m.AllowedHeads[head]; ok {
			equalSets(t, "allowedHeads["+head+"]", setOf(subs), want)
		}
	}

	equalSets(t, "pipeFilters", setOf(pipeFilters), m.PipeFilters)

	equalSets(t, "flagsTakingValue keys", keysOf(flagsTakingValue), func() []string {
		out := make([]string, 0, len(m.FlagsTakingValue))
		for k := range m.FlagsTakingValue {
			out = append(out, k)
		}
		return out
	}())
	for cmd, flags := range flagsTakingValue {
		if want, ok := m.FlagsTakingValue[cmd]; ok {
			equalSets(t, "flagsTakingValue["+cmd+"]", setOf(flags), want)
		}
	}
}

// TestTables_ManifestIsNotVacuous — the comparison above is only worth the manifest's contents.
// An empty or truncated manifest that still parsed would let every set match trivially in one
// direction, so pin the size the guard is actually covering. The numbers are the measured census,
// not a target: 18 heads, 9 pipe filters, 6 flag-bearing commands.
func TestTables_ManifestIsNotVacuous(t *testing.T) {
	m := loadTables(t)
	if len(m.AllowedHeads) != 18 || len(m.PipeFilters) != 9 || len(m.FlagsTakingValue) != 6 {
		t.Errorf("manifest census changed: %d heads / %d pipe filters / %d flag-bearing commands (was 18/9/6). "+
			"If that is deliberate, update this count IN THE SAME COMMIT as the TypeScript port — that is the whole point of the file.",
			len(m.AllowedHeads), len(m.PipeFilters), len(m.FlagsTakingValue))
	}
	entries := len(m.AllowedHeads) + len(m.PipeFilters)
	for _, f := range m.FlagsTakingValue {
		entries += len(f)
	}
	if entries < 30 {
		t.Fatalf("only %d entries in the manifest — too thin to pin the population", entries)
	}
}
