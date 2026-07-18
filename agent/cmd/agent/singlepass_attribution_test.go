package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/talyvor/code/internal/config"
	"github.com/talyvor/code/internal/lens"
)

// (e) 409 ⇒ logged, PR still succeeds. A 409 means the output was already attributed to a
// DIFFERENT target (a possible mis-attribution) — it must be LOGGED (not silent), stay
// non-fatal, and not be counted as THIS PR's attribution.
func TestAttributePR_409_LoggedNotFatalNotCounted(t *testing.T) {
	editAttr := map[string]string{"foo.go": "oid-A", "bar.go": "oid-B"}
	committed := []string{"foo.go", "bar.go"}
	rep := &fakeAttrReporter{errOn: map[string]error{"oid-A": lens.ErrAttributionConflict}}

	var logbuf bytes.Buffer
	n := attributePR(context.Background(), config.Config{ReportAttribution: true}, &logbuf, rep, editAttr, committed, "REF")

	if len(rep.calls) != 2 {
		t.Errorf("both ids attempted (non-fatal); got %d", len(rep.calls))
	}
	if !strings.Contains(logbuf.String(), "already attributed") {
		t.Errorf("a 409 must be LOGGED as a mis-attribution signal; log=%q", logbuf.String())
	}
	if n != 1 {
		t.Errorf("a 409 (different ref) is not this PR's attribution → want n=1 (oid-B only); got %d", n)
	}
}

// (c) heal-supersedes + (b) skipped-excluded: the single-pass last-writer map records
// each APPLIED Phase-2 generation, then heal-loop repairs OVERWRITE the files they
// rewrote (they are the later, surviving writer). A file skipped in Phase 2 (absent from
// `applied`) is never in the map.
func TestSinglePassLastWriters_HealSupersedesAndSkips(t *testing.T) {
	applied := []FileChange{
		{Path: "foo.go", OutputID: "oid-gen-foo"},
		{Path: "bar.go", OutputID: "oid-gen-bar"},
		// baz.go was SKIPPED in Phase 2 → not in `applied` → must not be attributed.
	}
	heal := map[string]string{"foo.go": "oid-heal-foo"} // a repair rewrote foo.go

	m := singlePassLastWriters(applied, heal)
	if m["foo.go"] != "oid-heal-foo" {
		t.Errorf("(c) heal repair must be the LAST writer of foo.go; got %q", m["foo.go"])
	}
	if m["bar.go"] != "oid-gen-bar" {
		t.Errorf("bar.go = %q, want its Phase-2 generation", m["bar.go"])
	}
	if _, ok := m["baz.go"]; ok {
		t.Error("(b) a skipped file (not applied) must NOT be attributed")
	}
}

// (a) reverted-not-attributed: a file edited (applied) then reverted is present in the
// last-writer map but absent from the committed diff, so the EXISTING survivingAttributions
// gate drops it — never attribute-everything.
func TestSinglePassLastWriters_RevertedDroppedByCommittedDiff(t *testing.T) {
	m := singlePassLastWriters([]FileChange{{Path: "foo.go", OutputID: "oid-A"}}, nil)
	if surviving := survivingAttributions(m, []string{}); len(surviving) != 0 {
		t.Errorf("(a) a reverted file (absent from committed diff) must not be attributed; got %v", surviving)
	}
	if surviving := survivingAttributions(m, []string{"foo.go"}); len(surviving) != 1 || surviving[0] != "oid-A" {
		t.Errorf("a surviving file must be attributed; got %v", surviving)
	}
}

// (d) flag OFF ⇒ zero calls, byte-identical — on the single-pass map too.
func TestSinglePassAttribution_FlagOff_ZeroCalls(t *testing.T) {
	m := singlePassLastWriters([]FileChange{{Path: "foo.go", OutputID: "oid-A"}}, nil)
	rep := &fakeAttrReporter{}
	if n := attributePR(context.Background(), config.Config{ReportAttribution: false}, nil, rep, m, []string{"foo.go"}, "REF"); n != 0 || len(rep.calls) != 0 {
		t.Fatalf("flag OFF must make zero calls; n=%d calls=%v", n, rep.calls)
	}
}

// generateChange must now CAPTURE the gateway output_id (X-Talyvor-Output-Id) into the
// FileChange — the fix for the single-pass path that previously dropped it via lc.Complete.
func TestGenerateChange_CapturesOutputId(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Talyvor-Output-Id", "oid-generated")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"package x\nfunc X() {}\n"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	cfg := config.Config{LensURL: srv.URL, LensAPIKey: "k", WorkspaceID: "ws"}
	lc := lens.New(srv.URL, "k")
	pf := PlannedFile{Path: "x.go", Operation: "create", Description: "add X"}

	change, err := generateChange(context.Background(), lc, cfg, "task", pf, t.TempDir(), "model", nil)
	if err != nil {
		t.Fatal(err)
	}
	if change.OutputID != "oid-generated" {
		t.Errorf("generateChange must capture the output_id; got %q", change.OutputID)
	}
}
