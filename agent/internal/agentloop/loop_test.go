package agentloop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scriptModel replays a fixed sequence of replies and RECORDS the messages it saw
// each turn — so a test can prove the loop fed a tool observation back into the next
// turn's context. After the script is exhausted it emits `done`.
type scriptModel struct {
	replies []string
	i       int
	seen    [][]Message
}

func (m *scriptModel) Complete(_ context.Context, msgs []Message) (string, error) {
	cp := make([]Message, len(msgs))
	copy(cp, msgs)
	m.seen = append(m.seen, cp)
	if m.i >= len(m.replies) {
		return `{"thought":"finished","tool":"done","args":{"summary":"done"}}`, nil
	}
	r := m.replies[m.i]
	m.i++
	return r, nil
}

// TestLoop_DispatchesObservesAdvances — the core mechanic: the model's tool call is
// dispatched, its result becomes an OBSERVATION in the next turn's context, and the
// loop advances. Proven by the model SEEING the read_file output on turn 2.
func TestLoop_DispatchesObservesAdvances(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.go"), []byte("package x\n// OBSERVED_CONTENT\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	model := &scriptModel{replies: []string{
		`{"thought":"read it","tool":"read_file","args":{"path":"x.go"}}`,
		`{"thought":"done","tool":"done","args":{"summary":"read x.go"}}`,
	}}
	ag := New(model, DefaultTools(root, nil), Config{MaxSteps: 10})
	res, err := ag.Run(context.Background(), "look at x.go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Done || res.Stop != StopDone {
		t.Errorf("expected clean done; got Done=%v Stop=%v", res.Done, res.Stop)
	}
	if res.Steps != 2 {
		t.Errorf("expected 2 steps (read → done); got %d", res.Steps)
	}
	// The model's SECOND turn must have seen the read_file observation — proof the
	// loop observed and fed it back (not a blind single pass).
	if len(model.seen) < 2 {
		t.Fatalf("model was called %d times, want ≥2", len(model.seen))
	}
	turn2 := model.seen[1]
	found := false
	for _, msg := range turn2 {
		if strings.Contains(msg.Content, "OBSERVED_CONTENT") {
			found = true
		}
	}
	if !found {
		t.Error("turn-2 context must contain the read_file observation (the loop must feed results back)")
	}
}

// TestLoop_StopsOnBudget — the loop is bounded: a model that never finishes stops at
// MaxSteps (with DISTINCT tool args each turn so the no-progress detector doesn't
// pre-empt the budget path).
func TestLoop_StopsOnBudget(t *testing.T) {
	root := t.TempDir()
	n := 0
	model := ModelFunc(func(_ context.Context, _ []Message) (string, error) {
		n++
		return fmt.Sprintf(`{"tool":"read_file","args":{"path":"missing_%d.go"}}`, n), nil
	})
	ag := New(model, DefaultTools(root, nil), Config{MaxSteps: 4})
	res, err := ag.Run(context.Background(), "loop forever")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Stop != StopBudget || res.Done {
		t.Errorf("expected budget stop; got Stop=%v Done=%v", res.Stop, res.Done)
	}
	if res.Steps != 4 {
		t.Errorf("expected exactly MaxSteps=4 steps; got %d", res.Steps)
	}
}

// TestLoop_RecoversFromBadToolFormat — a malformed model reply is fed back as an
// error observation so the model can correct itself; the loop does not crash.
func TestLoop_RecoversFromBadToolFormat(t *testing.T) {
	root := t.TempDir()
	model := &scriptModel{replies: []string{
		`I will now read the file.`, // not JSON
		`{"tool":"done","args":{"summary":"ok"}}`,
	}}
	ag := New(model, DefaultTools(root, nil), Config{MaxSteps: 10})
	res, err := ag.Run(context.Background(), "task")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Done {
		t.Errorf("loop must recover from a bad-format turn and still finish; got %+v", res)
	}
	// The turn after the bad reply must contain a format-correction hint.
	if len(model.seen) < 2 || !containsAny(model.seen[1], "JSON") {
		t.Error("a malformed reply must be fed back with a format hint")
	}
}

func containsAny(msgs []Message, sub string) bool {
	for _, m := range msgs {
		if strings.Contains(m.Content, sub) {
			return true
		}
	}
	return false
}
