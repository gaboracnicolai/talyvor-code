package agentloop

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// TestLoop_StopsOnNoProgress_EditFailCycle — THE overnight-safety property: an agent
// stuck in edit→fail→IDENTICAL-edit must abort via the no-progress detector, NOT burn
// its whole step budget. The model here loops forever: same edit, failing run, same
// edit… The loop must stop well before MaxSteps.
func TestLoop_StopsOnNoProgress_EditFailCycle(t *testing.T) {
	root := t.TempDir()
	n := 0
	model := ModelFunc(func(_ context.Context, _ []Message) (string, error) {
		n++
		if n%2 == 1 {
			return `{"tool":"edit_file","args":{"path":"x.go","content":"package x\nfunc Broken(){}\n"}}`, nil
		}
		return `{"tool":"run","args":{"cmd":"exit 1"}}`, nil
	})
	ag := New(model, DefaultTools(root, nil), Config{MaxSteps: 50, MaxRepeat: 2})
	res, err := ag.Run(context.Background(), "fix the build")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Stop != StopNoProgress {
		t.Errorf("a repeating edit→fail→identical-edit cycle must stop as no-progress; got %v", res.Stop)
	}
	if res.Steps >= 50 {
		t.Errorf("no-progress must abort BEFORE the budget; burned %d/50 steps", res.Steps)
	}
	// With MaxRepeat=2, the 3rd identical edit (turns 1,3,5) trips at step 5.
	if res.Steps != 5 {
		t.Errorf("expected abort at step 5; got %d", res.Steps)
	}
}

// TestLoop_StopsOnNoProgress_IdenticalEdit — the tightest case: the same edit every
// turn. With MaxRepeat=2 the 3rd identical call aborts.
func TestLoop_StopsOnNoProgress_IdenticalEdit(t *testing.T) {
	root := t.TempDir()
	model := ModelFunc(func(_ context.Context, _ []Message) (string, error) {
		return `{"tool":"edit_file","args":{"path":"a.go","content":"package a\n"}}`, nil
	})
	ag := New(model, DefaultTools(root, nil), Config{MaxSteps: 30, MaxRepeat: 2})
	res, _ := ag.Run(context.Background(), "loop")
	if res.Stop != StopNoProgress || res.Steps != 3 {
		t.Errorf("identical edit must abort at step 3 as no-progress; got Stop=%v Steps=%d", res.Stop, res.Steps)
	}
}

// TestLoop_StopsOnNoProgress_GarbageReplies — a model that never emits valid JSON
// can't loop forever either; bounded malformed replies trip no-progress.
func TestLoop_StopsOnNoProgress_GarbageReplies(t *testing.T) {
	root := t.TempDir()
	model := ModelFunc(func(_ context.Context, _ []Message) (string, error) {
		return "sorry, I cannot do that", nil // never valid JSON
	})
	ag := New(model, DefaultTools(root, nil), Config{MaxSteps: 30, MaxRepeat: 2})
	res, _ := ag.Run(context.Background(), "loop")
	if res.Stop != StopNoProgress {
		t.Errorf("endless garbage must stop as no-progress; got %v", res.Stop)
	}
	if res.Steps >= 30 {
		t.Errorf("garbage must abort before the budget; got %d", res.Steps)
	}
}

// TestLoop_CleanDoneStops — an agent that finishes returns done with its summary.
func TestLoop_CleanDoneStops(t *testing.T) {
	root := t.TempDir()
	model := &scriptModel{replies: []string{
		`{"tool":"run","args":{"cmd":"echo ok"}}`,
		`{"tool":"done","args":{"summary":"verified: build passes"}}`,
	}}
	ag := New(model, DefaultTools(root, nil), Config{MaxSteps: 10})
	res, _ := ag.Run(context.Background(), "task")
	if !res.Done || res.Stop != StopDone {
		t.Errorf("expected clean done; got %+v", res)
	}
	if res.Summary != "verified: build passes" {
		t.Errorf("done summary not captured; got %q", res.Summary)
	}
}

// TestLoop_ModelErrorPropagates — a hard model-call error stops the loop as
// StopError and surfaces the error (not a silent hang).
func TestLoop_ModelErrorPropagates(t *testing.T) {
	root := t.TempDir()
	boom := errors.New("lens down")
	model := ModelFunc(func(_ context.Context, _ []Message) (string, error) {
		return "", fmt.Errorf("call failed: %w", boom)
	})
	ag := New(model, DefaultTools(root, nil), Config{MaxSteps: 5})
	res, err := ag.Run(context.Background(), "task")
	if err == nil || res.Stop != StopError {
		t.Errorf("model error must propagate as StopError; got Stop=%v err=%v", res.Stop, err)
	}
}
