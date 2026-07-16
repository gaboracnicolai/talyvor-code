package agentloop

import (
	"context"
	"testing"
)

// oidModel implements Model + the optional OutputIdentified: scripted replies + a
// scripted output_id per turn (mirrors how the real *lensModel exposes the
// X-Talyvor-Output-Id of its most recent completion).
type oidModel struct {
	replies []string
	oids    []string
	i       int
	last    string
}

func (m *oidModel) Complete(_ context.Context, _ []Message) (string, error) {
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
func (m *oidModel) LastOutputID() string { return m.last }

// captureObserver records every StepInfo the loop emits.
type captureObserver struct{ steps []StepInfo }

func (o *captureObserver) ObserveStep(s StepInfo) { o.steps = append(o.steps, s) }

// TestLoop_EmitsOutputIdAndRunExitToObserver — the loop must feed each step's generation
// output_id (via OutputIdentified) to the Observer, and for a `run` step it must carry
// the command's real exit code + Ran=true (so an external verdict reporter can pair
// generations to build/test outcomes without parsing the observation string).
func TestLoop_EmitsOutputIdAndRunExitToObserver(t *testing.T) {
	root := t.TempDir()
	model := &oidModel{
		replies: []string{
			`{"tool":"edit_file","args":{"path":"a.go","content":"package a\n"}}`,
			`{"tool":"run","args":{"cmd":"false"}}`, // exit 1
			`{"tool":"done","args":{"summary":"ok"}}`,
		},
		oids: []string{"oid-edit", "oid-run", "oid-done"},
	}
	obs := &captureObserver{}
	ag := New(model, DefaultTools(root, nil), Config{MaxSteps: 10, Observer: obs})
	if _, err := ag.Run(context.Background(), "task"); err != nil {
		t.Fatal(err)
	}

	var editStep, runStep *StepInfo
	for i := range obs.steps {
		switch obs.steps[i].Tool {
		case "edit_file":
			editStep = &obs.steps[i]
		case "run":
			runStep = &obs.steps[i]
		}
	}
	if editStep == nil || runStep == nil {
		t.Fatalf("observer must see the edit + run steps; got %d steps", len(obs.steps))
	}
	if editStep.OutputID != "oid-edit" {
		t.Errorf("edit step output_id = %q, want oid-edit", editStep.OutputID)
	}
	if editStep.ToolErr {
		t.Error("a successful edit must not be flagged ToolErr")
	}
	if runStep.OutputID != "oid-run" {
		t.Errorf("run step output_id = %q, want oid-run", runStep.OutputID)
	}
	if !runStep.Ran {
		t.Error("a run step must be flagged Ran")
	}
	if runStep.RunExit != 1 {
		t.Errorf("run step exit = %d, want 1 (the `false` command)", runStep.RunExit)
	}
}

// TestLoop_NilObserver_NoOp — with no Observer the loop behaves exactly as before
// (behavior-preserving). It must run to a clean done and emit nothing.
func TestLoop_NilObserver_NoOp(t *testing.T) {
	root := t.TempDir()
	model := &oidModel{
		replies: []string{`{"tool":"done","args":{"summary":"x"}}`},
		oids:    []string{"oid-1"},
	}
	res, err := New(model, DefaultTools(root, nil), Config{MaxSteps: 5}).Run(context.Background(), "t")
	if err != nil || !res.Done {
		t.Fatalf("nil-observer loop must still finish cleanly; done=%v err=%v", res.Done, err)
	}
}
