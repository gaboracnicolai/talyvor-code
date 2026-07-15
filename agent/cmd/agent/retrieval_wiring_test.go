package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/talyvor/code/internal/codebase"
	"github.com/talyvor/code/internal/config"
	"github.com/talyvor/code/internal/lens"
)

// constEmbedder returns a fixed unit vector for any text — lets a seeded on-disk
// index and a mocked query-embed land in the same space so retrieval hits.
type constEmbedder struct{}

func (constEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 0, 0, 0}
	}
	return out, nil
}

// TestAsk_InjectsRetrievedContext — end-to-end proof that `ask` is retrieval-wired
// (recon consumer (a) chat context): with a persisted semantic index present, the
// prompt `ask` sends to Lens includes the retrieved codebase chunk.
func TestAsk_InjectsRetrievedContext(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	idx, err := codebase.BuildIndex(context.Background(), constEmbedder{}, []codebase.Chunk{
		{File: "seed.go", Language: "Go", StartLine: 1, EndLine: 2, Content: "func Seed(){ /*SEEDED_CHUNK_XYZ*/ }"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Save(codebase.IndexPath(root)); err != nil {
		t.Fatal(err)
	}

	var completionBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "embeddings") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"index":0,"embedding":[1,0,0,0]}]}`)
			return
		}
		b, _ := io.ReadAll(r.Body)
		completionBody = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		f, _ := w.(http.Flusher)
		for _, ev := range []string{
			`{"type":"content_block_delta","delta":{"type":"text_delta","text":"answer"}}`,
			`{"type":"message_stop"}`, `[DONE]`,
		} {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", ev)
			if f != nil {
				f.Flush()
			}
		}
	}))
	defer srv.Close()

	cfg := config.Config{LensURL: srv.URL, LensAPIKey: "k", WorkspaceID: "ws", Model: "claude-haiku-4-6"}
	if err := runAsk(io.Discard, cfg, []string{"how", "does", "seeding", "work"}); err != nil {
		t.Fatalf("runAsk: %v", err)
	}
	if !strings.Contains(completionBody, "SEEDED_CHUNK_XYZ") {
		t.Errorf("ask prompt must include the retrieved codebase context; got:\n%s", completionBody)
	}
}

// fakeRetriever returns canned chunks regardless of query — lets us prove the
// retrieved context reaches the generation prompt without a real index/embedder.
type fakeRetriever struct{ out []codebase.RetrievedChunk }

func (f fakeRetriever) Retrieve(context.Context, string, int) ([]codebase.RetrievedChunk, error) {
	return f.out, nil
}

// captureLens returns a Lens client pointed at an httptest server that records the
// request body (the outgoing prompt) and replies with a canned completion.
func captureLens(t *testing.T, captured *string) *lens.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*captured = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"package x\nfunc F(){}\n"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	t.Cleanup(srv.Close)
	return lens.New(srv.URL, "k")
}

// TestGenerateChange_FeedsRetrievedSiblingContext — the recon's gap #2 fix: the
// agent's per-file generation now receives RELEVANT SIBLING context from retrieval.
// Previously it saw only the one file; now the retrieved sibling chunk reaches the
// prompt. A nil retriever preserves the old blind behavior (contrast).
func TestGenerateChange_FeedsRetrievedSiblingContext(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.go"), []byte("package x\nfunc F(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{WorkspaceID: "ws", ActiveIssue: "ENG-1"}
	pf := PlannedFile{Path: "target.go", Operation: "modify", Description: "add logging"}
	ret := fakeRetriever{out: []codebase.RetrievedChunk{
		{Chunk: codebase.Chunk{File: "logger.go", StartLine: 1, EndLine: 3, Content: "func Log(m string){ /*SIBLING_MARKER*/ }"}, Score: 0.9},
	}}

	// WITH a retriever → the sibling chunk is in the prompt.
	var captured string
	lc := captureLens(t, &captured)
	if _, err := generateChange(context.Background(), lc, cfg, "add logging", pf, root, "model", ret); err != nil {
		t.Fatalf("generateChange: %v", err)
	}
	if !strings.Contains(captured, "SIBLING_MARKER") {
		t.Error("generation prompt must include the RETRIEVED sibling context (gap #2)")
	}
	if !strings.Contains(captured, "func F()") {
		t.Error("generation prompt must still include the target file's own current content")
	}
	if !strings.Contains(captured, "logger.go:1-3") {
		t.Error("retrieved sibling context must be cited by file:span")
	}

	// WITHOUT a retriever → no sibling context (the prior blind behavior).
	var blind string
	lc2 := captureLens(t, &blind)
	if _, err := generateChange(context.Background(), lc2, cfg, "add logging", pf, root, "model", nil); err != nil {
		t.Fatalf("generateChange nil-ret: %v", err)
	}
	if strings.Contains(blind, "SIBLING_MARKER") {
		t.Error("a nil retriever must not inject sibling context")
	}
}
