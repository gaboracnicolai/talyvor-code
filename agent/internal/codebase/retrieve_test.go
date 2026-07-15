package codebase

import (
	"context"
	"strings"
	"testing"
)

// TestRelevantContextSection_IncludesSiblingsExcludesEdited — the prompt section fed
// to generation: it cites each retrieved chunk by file:span and includes its
// content, but EXCLUDES the file being edited (already shown in full) so the model
// gets cross-file context, not a duplicate of itself.
func TestRelevantContextSection_IncludesSiblingsExcludesEdited(t *testing.T) {
	chunks := []RetrievedChunk{
		{Chunk: Chunk{File: "internal/auth/session.go", StartLine: 1, EndLine: 3, Content: "SIBLING_AAA"}, Score: 0.9},
		{Chunk: Chunk{File: "target.go", StartLine: 5, EndLine: 9, Content: "SELF_CONTENT"}, Score: 0.85},
		{Chunk: Chunk{File: "internal/db/user.go", StartLine: 2, EndLine: 4, Content: "SIBLING_BBB"}, Score: 0.7},
	}
	s := RelevantContextSection(chunks, "target.go", 5)
	if !strings.Contains(s, "SIBLING_AAA") || !strings.Contains(s, "SIBLING_BBB") {
		t.Error("must include the sibling chunk contents")
	}
	if strings.Contains(s, "SELF_CONTENT") {
		t.Error("must EXCLUDE the file being edited (target.go)")
	}
	if !strings.Contains(s, "internal/auth/session.go:1-3") {
		t.Error("must cite each chunk by file:span")
	}
	if RelevantContextSection(nil, "x", 5) != "" {
		t.Error("no chunks → empty section")
	}
	// Respects the cap (max siblings shown).
	many := make([]RetrievedChunk, 8)
	for i := range many {
		many[i] = RetrievedChunk{Chunk: Chunk{File: "f" + string(rune('a'+i)) + ".go", StartLine: 1, EndLine: 1, Content: "C"}, Score: 1}
	}
	if got := strings.Count(RelevantContextSection(many, "none", 3), ".go:"); got != 3 {
		t.Errorf("max=3 must show 3 chunks, showed %d", got)
	}
}

// TestRetrieval_ReplacesPathSubstring — the "genuinely replaced, not added beside"
// proof: retrieval is now the relevance source and it SUPERSEDES the old
// path-substring FindRelevantFiles. For a query whose terms live only in a file's
// CONTENT (never its path), the old path-substring scorer returns NOTHING, while
// semantic retrieval ranks that file top — same fixture, opposite outcomes.
func TestRetrieval_ReplacesPathSubstring(t *testing.T) {
	ctx := context.Background()
	query := "postgres pgx connection pool"

	// OLD relevance source: path-substring over the filename index. "postgres"/"pgx"
	// appear in NO path, so it scores zero and returns nothing.
	fi := &CodebaseIndex{Files: []FileInfo{
		{Path: "internal/auth/login.go"}, {Path: "internal/db/query.go"}, {Path: "frontend/button.tsx"},
	}}
	if old := fi.FindRelevantFiles(query, 3); len(old) != 0 {
		t.Errorf("path-substring must MISS a content-only query (no path shares the terms); got %+v", old)
	}

	// NEW relevance source: semantic retrieval over the same files' CONTENT ranks
	// db/query.go top by content — the capability path-substring never had.
	emb := bagEmbedder{dim: 256}
	idx, _ := BuildIndex(ctx, emb, fixtureChunks())
	got, _ := idx.Retrieve(ctx, emb, query, 1)
	if len(got) != 1 || got[0].File != "internal/db/query.go" {
		t.Errorf("retrieval must find db/query.go by CONTENT where path-substring missed; got %+v", got)
	}
}

// TestBoundIndex_RetrieverSeam — BoundIndex is the consumer-facing Retriever: it
// binds a SemanticIndex + Embedder so chat/agent hold one relevance API. A nil index
// retrieves nothing (graceful no-op), never panics.
func TestBoundIndex_RetrieverSeam(t *testing.T) {
	ctx := context.Background()
	emb := bagEmbedder{dim: 128}
	idx, _ := BuildIndex(ctx, emb, fixtureChunks())
	var r Retriever = BoundIndex{Index: idx, Emb: emb}
	got, err := r.Retrieve(ctx, "authentication login credentials", 1)
	if err != nil || len(got) != 1 || got[0].File != "internal/auth/login.go" {
		t.Errorf("BoundIndex.Retrieve wrong: %+v err=%v", got, err)
	}
	// nil index → nil, no panic.
	var nilR Retriever = BoundIndex{Index: nil, Emb: emb}
	if got, err := nilR.Retrieve(ctx, "x", 3); err != nil || got != nil {
		t.Errorf("nil index must retrieve nothing, got %v err=%v", got, err)
	}
}
