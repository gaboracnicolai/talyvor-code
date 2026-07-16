package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/talyvor/code/internal/agentloop"
	"github.com/talyvor/code/internal/config"
	"github.com/talyvor/code/internal/lens"
)

// TestVerdictObserverFor_GatedOnFlag — the behavior-preserving gate: ReportVerdicts OFF
// installs NO observer (the loop's Observer stays nil ⇒ byte-identical to before); ON
// installs one.
func TestVerdictObserverFor_GatedOnFlag(t *testing.T) {
	lc := lens.New("http://127.0.0.1:1", "k")
	if o := verdictObserverFor(context.Background(), config.Config{ReportVerdicts: false}, lc, nil); o != nil {
		t.Error("ReportVerdicts=false must install NO observer (byte-identical loop)")
	}
	if o := verdictObserverFor(context.Background(), config.Config{ReportVerdicts: true}, lc, nil); o == nil {
		t.Error("ReportVerdicts=true must install a verdict observer")
	}
}

// oidScriptModel implements agentloop.Model + OutputIdentified for the end-to-end test.
type oidScriptModel struct {
	replies []string
	oids    []string
	i       int
	last    string
}

func (m *oidScriptModel) Complete(_ context.Context, _ []agentloop.Message) (string, error) {
	r := `{"tool":"done","args":{"summary":"end"}}`
	if m.i < len(m.replies) {
		r = m.replies[m.i]
	}
	if m.i < len(m.oids) {
		m.last = m.oids[m.i]
	} else {
		m.last = ""
	}
	m.i++
	return r, nil
}
func (m *oidScriptModel) LastOutputID() string { return m.last }

// TestEndToEnd_LoopReportsCorrectVerdict — the whole chain: the real loop drives an
// edit→run over the real tools, the verdictObserver receives the StepInfos, and a clean
// 1:1 build/test run reports the correct verdict for the edit's output_id.
func TestEndToEnd_LoopReportsCorrectVerdict(t *testing.T) {
	root := t.TempDir()
	model := &oidScriptModel{
		replies: []string{
			`{"tool":"edit_file","args":{"path":"a.go","content":"package a\n"}}`,
			`{"tool":"run","args":{"cmd":"echo running test"}}`, // build/test-recognized, exit 0
			`{"tool":"done","args":{"summary":"ok"}}`,
		},
		oids: []string{"oid-edit", "oid-run", "oid-done"},
	}
	rep := &fakeReporter{}
	obs := newVerdictObserver(context.Background(), rep, nil)
	ag := agentloop.New(model, agentloop.DefaultTools(root, nil), agentloop.Config{MaxSteps: 10, Observer: obs})
	if _, err := ag.Run(context.Background(), "task"); err != nil {
		t.Fatal(err)
	}
	if len(rep.calls) != 1 {
		t.Fatalf("expected exactly one verdict end-to-end; got %+v", rep.calls)
	}
	c := rep.calls[0]
	if c.outputID != "oid-edit" || c.verdict != "tests_passed" || c.exit != 0 {
		t.Errorf("end-to-end verdict = %+v, want oid-edit/tests_passed/0", c)
	}
}

// TestLensModel_CapturesOutputIdFromHeader — the real adapter threads output_id: after a
// Complete, LastOutputID returns the X-Talyvor-Output-Id the gateway set.
func TestLensModel_CapturesOutputIdFromHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Talyvor-Output-Id", "oid-from-header")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	m := newLensModel(lens.New(srv.URL, "k"), "model", "ws", "ENG-1")
	reply, err := m.Complete(context.Background(), []agentloop.Message{{Role: "user", Content: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if reply != "hi" {
		t.Errorf("reply = %q, want hi", reply)
	}
	if got := m.LastOutputID(); got != "oid-from-header" {
		t.Errorf("LastOutputID = %q, want oid-from-header (the gateway header)", got)
	}
}
