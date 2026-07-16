package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/talyvor/code/internal/codebase"
	"github.com/talyvor/code/internal/config"
	"github.com/talyvor/code/internal/lens"
)

// TestLoadRetriever_WarnsWhenStale — the consumer-side staleness contract: after the
// working tree drifts from the persisted index, loadRetriever emits a STALE warning to
// its warn writer (so chat/ask/agent surface that retrieval is grounded in out-of-date
// chunks); a fresh index stays silent. Warn-only — it never re-embeds on the load path.
func TestLoadRetriever_WarnsWhenStale(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n\nfunc Alpha() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := codebase.BuildFromRoot(context.Background(), constEmbedder{}, root, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Save(codebase.IndexPath(root)); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{WorkspaceID: "ws"}
	lc := lens.New("http://127.0.0.1:1", "k") // never dialed on the load/staleness path

	// Fresh: a retriever is returned and NO staleness warning is written.
	var fresh strings.Builder
	if r := loadRetriever(lc, cfg, root, nil, &fresh); r == nil {
		t.Fatal("a present index must yield a retriever")
	}
	if strings.Contains(fresh.String(), "STALE") {
		t.Errorf("a fresh index must not warn; got %q", fresh.String())
	}

	// Drift the working tree → the index is now stale.
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package p\n\nfunc Alpha() int { return 999 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var warn strings.Builder
	loadRetriever(lc, cfg, root, nil, &warn)
	if !strings.Contains(warn.String(), "STALE") {
		t.Errorf("loadRetriever must WARN when the index is stale; got %q", warn.String())
	}
}
