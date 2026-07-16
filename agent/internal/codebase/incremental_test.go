package codebase

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// countingEmbedder records every text it embeds so a test can prove exactly which
// chunks were (re-)embedded on an incremental pass.
type countingEmbedder struct {
	calls    int
	embedded []string
}

func (e *countingEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	e.calls++
	e.embedded = append(e.embedded, texts...)
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 0, 0, 0}
	}
	return out, nil
}
func (e *countingEmbedder) reset()                    { e.calls = 0; e.embedded = nil }
func (e *countingEmbedder) embeddedAny(s string) bool { return containsAnyOf(e.embedded, s) }

func containsAnyOf(xs []string, sub string) bool {
	for _, x := range xs {
		if strings.Contains(x, sub) {
			return true
		}
	}
	return false
}

func writeF(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasFileChunk(idx *SemanticIndex, file string) bool {
	for _, c := range idx.Chunks {
		if c.File == file {
			return true
		}
	}
	return false
}

// TestBuildFromRoot_SetsVersionAndHashes — a full build records the schema version
// and a per-file content hash for every indexed file (the substrate incremental
// re-index compares against).
func TestBuildFromRoot_SetsVersionAndHashes(t *testing.T) {
	root := t.TempDir()
	writeF(t, root, "a.go", "package p\n\nfunc A() int { return 1 }\n")
	idx, err := BuildFromRoot(context.Background(), &countingEmbedder{}, root, 100)
	if err != nil {
		t.Fatal(err)
	}
	if idx.Version != IndexVersion {
		t.Errorf("index Version = %d, want %d", idx.Version, IndexVersion)
	}
	if idx.FileHashes["a.go"] == "" {
		t.Error("full build must record a per-file content hash")
	}
}

// TestBuildIncremental_ReEmbedsOnlyChangedFile — the core proof: after changing ONE
// file, a re-index embeds ONLY that file's chunks and reuses the rest.
func TestBuildIncremental_ReEmbedsOnlyChangedFile(t *testing.T) {
	root := t.TempDir()
	writeF(t, root, "a.go", "package p\n\nfunc Alpha() int { return 1 }\n")
	writeF(t, root, "b.go", "package p\n\nfunc Bravo() int { return 2 }\n")
	writeF(t, root, "c.go", "package p\n\nfunc Charlie() int { return 3 }\n")

	emb := &countingEmbedder{}
	full, err := BuildFromRoot(context.Background(), emb, root, 100)
	if err != nil {
		t.Fatal(err)
	}
	total := len(emb.embedded)
	if total == 0 {
		t.Fatal("full build embedded nothing")
	}

	// Change ONLY b.go.
	writeF(t, root, "b.go", "package p\n\nfunc Bravo() int { return 999 }\n")
	emb.reset()
	inc, err := BuildIncremental(context.Background(), emb, root, 100, full)
	if err != nil {
		t.Fatal(err)
	}
	if emb.embeddedAny("Alpha") || emb.embeddedAny("Charlie") {
		t.Error("incremental MUST NOT re-embed unchanged files (a.go / c.go)")
	}
	if !emb.embeddedAny("999") {
		t.Error("incremental MUST embed the changed file (b.go)")
	}
	if len(emb.embedded) >= total {
		t.Errorf("incremental embedded %d chunks; must be FEWER than the full %d", len(emb.embedded), total)
	}
	// All three files remain in the index.
	for _, f := range []string{"a.go", "b.go", "c.go"} {
		if !hasFileChunk(inc, f) {
			t.Errorf("incremental index dropped %s", f)
		}
	}
	// Hashes: b.go updated, a.go/c.go unchanged.
	if inc.FileHashes["b.go"] == full.FileHashes["b.go"] {
		t.Error("b.go's stored hash must change after the edit")
	}
	if inc.FileHashes["a.go"] != full.FileHashes["a.go"] {
		t.Error("a.go's hash must be unchanged")
	}
}

// TestBuildIncremental_DropsDeletedFile — a deleted file's chunks and hash leave the
// index, and nothing is embedded (the surviving file is unchanged).
func TestBuildIncremental_DropsDeletedFile(t *testing.T) {
	root := t.TempDir()
	writeF(t, root, "a.go", "package p\n\nfunc Alpha() int { return 1 }\n")
	writeF(t, root, "b.go", "package p\n\nfunc Bravo() int { return 2 }\n")
	emb := &countingEmbedder{}
	full, err := BuildFromRoot(context.Background(), emb, root, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "b.go")); err != nil {
		t.Fatal(err)
	}
	emb.reset()
	inc, err := BuildIncremental(context.Background(), emb, root, 100, full)
	if err != nil {
		t.Fatal(err)
	}
	if hasFileChunk(inc, "b.go") {
		t.Error("a deleted file's chunks must leave the index")
	}
	if _, ok := inc.FileHashes["b.go"]; ok {
		t.Error("a deleted file's hash must be removed")
	}
	if !hasFileChunk(inc, "a.go") {
		t.Error("the surviving file must remain")
	}
	if len(emb.embedded) != 0 {
		t.Errorf("a pure deletion re-embeds nothing; embedded %d", len(emb.embedded))
	}
}

// TestBuildIncremental_AddsNewFile — a new file is embedded and added; unchanged
// files are not re-embedded.
func TestBuildIncremental_AddsNewFile(t *testing.T) {
	root := t.TempDir()
	writeF(t, root, "a.go", "package p\n\nfunc Alpha() int { return 1 }\n")
	emb := &countingEmbedder{}
	full, err := BuildFromRoot(context.Background(), emb, root, 100)
	if err != nil {
		t.Fatal(err)
	}
	writeF(t, root, "new.go", "package p\n\nfunc NewlyAdded() int { return 7 }\n")
	emb.reset()
	inc, err := BuildIncremental(context.Background(), emb, root, 100, full)
	if err != nil {
		t.Fatal(err)
	}
	if !emb.embeddedAny("NewlyAdded") {
		t.Error("a new file must be embedded")
	}
	if emb.embeddedAny("Alpha") {
		t.Error("the unchanged file must NOT be re-embedded")
	}
	if !hasFileChunk(inc, "new.go") {
		t.Error("the new file must be in the index")
	}
}

// TestBuildFromRoot_SkipsIndexCacheDir — the index must not embed its OWN cache: the
// .talyvor dir is skipped by the walk, so a re-index never ingests codebase-index.json.
func TestBuildFromRoot_SkipsIndexCacheDir(t *testing.T) {
	root := t.TempDir()
	writeF(t, root, "a.go", "package p\n\nfunc A() int { return 1 }\n")
	// Simulate an existing index cache.
	if err := os.MkdirAll(filepath.Join(root, ".talyvor"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeF(t, root, ".talyvor/codebase-index.json", `{"chunks":[{"file":"CACHE_SENTINEL"}]}`)
	emb := &countingEmbedder{}
	idx, err := BuildFromRoot(context.Background(), emb, root, 100)
	if err != nil {
		t.Fatal(err)
	}
	if emb.embeddedAny("CACHE_SENTINEL") || hasFileChunk(idx, ".talyvor/codebase-index.json") {
		t.Error("the index walk must SKIP the .talyvor cache dir (never index its own artifact)")
	}
}
