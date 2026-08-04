package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/talyvor/code/internal/codebase"
)

// AN AGENT ANSWERING WITHOUT THE CONTEXT IT CLAIMS TO USE MUST SAY SO.
//
// retrievedContext used to map ANY retrieval error to "", so `ask` answered from the model's own
// knowledge with no codebase grounding and no indication — the answer was indistinguishable from a
// grounded one. A confidently wrong answer about someone's own repository is worse than a refusal,
// because nothing about it invites checking.

type failingRetriever struct{ err error }

func (f failingRetriever) Retrieve(context.Context, string, int) ([]codebase.RetrievedChunk, error) {
	return nil, f.err
}

// ⚠ THE FAILURE IS RETURNED, NOT SWALLOWED.
func TestRetrievedContext_ReturnsTheRetrievalFailure(t *testing.T) {
	boom := errors.New("index corrupt: unexpected EOF")
	sec, err := retrievedContext(context.Background(), failingRetriever{boom}, "why is this nil", "")
	if err == nil {
		t.Fatal("a failed retrieval returned no error — `ask` would answer ungrounded and silent")
	}
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the underlying retrieval error", err)
	}
	if sec != "" {
		t.Errorf("section = %q, want empty on failure", sec)
	}
}

// ⚠ A NIL RETRIEVER IS NOT A FAILURE. "No index has been built" is already reported separately;
// treating it as an error would cry wolf on every run before the first index.
func TestRetrievedContext_NilRetrieverIsNotAnError(t *testing.T) {
	sec, err := retrievedContext(context.Background(), nil, "q", "")
	if err != nil {
		t.Errorf("nil retriever produced an error (%v) — that is the no-index case, not a failure", err)
	}
	if sec != "" {
		t.Errorf("section = %q, want empty", sec)
	}
}

// ⚠ AND THE USER HAS TO BE ABLE TO READ IT. The warning must name the failure AND say the answer is
// not grounded — a message that only says "retrieval failed" leaves the reader to infer what that
// means for the answer they are about to trust.
func TestWarnUngrounded_SaysTheAnswerIsNotGrounded(t *testing.T) {
	var buf bytes.Buffer
	warnUngrounded(&buf, errors.New("index corrupt: unexpected EOF"))
	out := buf.String()

	for _, want := range []string{"index corrupt", "NOT grounded", "guess"} {
		if !strings.Contains(out, want) {
			t.Errorf("warning does not contain %q:\n%s", want, out)
		}
	}
}
