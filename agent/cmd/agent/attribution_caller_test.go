package main

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/talyvor/code/internal/config"
)

type attrCall struct{ outputID, kind, ref string }
type fakeAttrReporter struct {
	calls []attrCall
	errOn map[string]error
}

func (f *fakeAttrReporter) ReportAttribution(_ context.Context, outputID, kind, ref string) error {
	f.calls = append(f.calls, attrCall{outputID, kind, ref})
	if f.errOn != nil {
		return f.errOn[outputID]
	}
	return nil
}

// TestAttributePR_FlagGatedAndSurviving — flag OFF makes ZERO calls (byte-identical); flag
// ON reports each SURVIVING generation (last-writer ∩ committed diff) as target_kind "pr"
// against the PR ref; a reverted file (absent from the committed diff) is not attributed.
func TestAttributePR_FlagGatedAndSurviving(t *testing.T) {
	editAttr := map[string]string{"foo.go": "oid-A", "bar.go": "oid-B", "gone.go": "oid-C"}
	committed := []string{"foo.go", "bar.go"} // gone.go was reverted → absent

	repOff := &fakeAttrReporter{}
	if n := attributePR(context.Background(), config.Config{ReportAttribution: false}, nil, repOff, editAttr, committed, "PRREF"); n != 0 || len(repOff.calls) != 0 {
		t.Fatalf("flag OFF must make zero calls; n=%d calls=%v", n, repOff.calls)
	}

	repOn := &fakeAttrReporter{}
	n := attributePR(context.Background(), config.Config{ReportAttribution: true}, nil, repOn, editAttr, committed, "PRREF")
	if n != 2 || len(repOn.calls) != 2 {
		t.Fatalf("flag ON must report the 2 surviving generations; n=%d calls=%v", n, repOn.calls)
	}
	for _, c := range repOn.calls {
		if c.kind != "pr" || c.ref != "PRREF" {
			t.Errorf("each call must carry kind=pr + the PR ref; got %+v", c)
		}
		if c.outputID == "oid-C" {
			t.Error("the reverted file's generation (oid-C) must NOT be attributed")
		}
	}
}

// TestAttributePR_BestEffort_ErrorNeverFails — an error on one id is logged + skipped; the
// others are still reported; attribution never fails the caller (no panic, returns a count).
func TestAttributePR_BestEffort_ErrorNeverFails(t *testing.T) {
	editAttr := map[string]string{"foo.go": "oid-A", "bar.go": "oid-B"}
	committed := []string{"foo.go", "bar.go"}
	rep := &fakeAttrReporter{errOn: map[string]error{"oid-A": errors.New("network down")}}

	n := attributePR(context.Background(), config.Config{ReportAttribution: true}, io.Discard, rep, editAttr, committed, "REF")
	if n != 1 {
		t.Errorf("one id errored (skipped), the other reported → n=1; got %d", n)
	}
	if len(rep.calls) != 2 {
		t.Errorf("both ids must be attempted; attempted=%d", len(rep.calls))
	}
}
