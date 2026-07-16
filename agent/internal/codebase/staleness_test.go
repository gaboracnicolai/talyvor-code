package codebase

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func inList(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// TestStaleness_StaleAfterEdit_FreshAfterReindex — the core staleness contract: a
// just-built index is fresh; after editing a file it reports stale + names the file;
// after an incremental re-index it is fresh again.
func TestStaleness_StaleAfterEdit_FreshAfterReindex(t *testing.T) {
	root := t.TempDir()
	writeF(t, root, "a.go", "package p\n\nfunc Alpha() int { return 1 }\n")
	writeF(t, root, "b.go", "package p\n\nfunc Bravo() int { return 2 }\n")
	emb := &countingEmbedder{}
	idx, err := BuildFromRoot(context.Background(), emb, root, 100)
	if err != nil {
		t.Fatal(err)
	}

	if r, _ := Staleness(root, idx, 100); r.Stale {
		t.Errorf("a just-built index must be fresh; got %+v", r)
	}

	writeF(t, root, "b.go", "package p\n\nfunc Bravo() int { return 999 }\n")
	r, err := Staleness(root, idx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Stale {
		t.Error("index must be STALE after an edit")
	}
	if !inList(r.Changed, "b.go") {
		t.Errorf("Changed must name b.go; got %v", r.Changed)
	}
	if inList(r.Changed, "a.go") {
		t.Error("a.go is unchanged; must not appear in Changed")
	}

	idx2, err := BuildIncremental(context.Background(), emb, root, 100, idx)
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := Staleness(root, idx2, 100); r.Stale {
		t.Errorf("index must be FRESH after re-index; got %+v", r)
	}
}

// TestStaleness_DetectsDeleted — a deleted file makes the index stale and appears in
// Deleted.
func TestStaleness_DetectsDeleted(t *testing.T) {
	root := t.TempDir()
	writeF(t, root, "a.go", "package p\n\nfunc Alpha() int { return 1 }\n")
	writeF(t, root, "b.go", "package p\n\nfunc Bravo() int { return 2 }\n")
	idx, err := BuildFromRoot(context.Background(), &countingEmbedder{}, root, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "b.go")); err != nil {
		t.Fatal(err)
	}
	r, err := Staleness(root, idx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Stale || !inList(r.Deleted, "b.go") {
		t.Errorf("a deleted file must make the index stale + appear in Deleted; got %+v", r)
	}
}

// TestStaleness_NilIndex — no index → not "stale" (nothing to compare); no panic.
func TestStaleness_NilIndex(t *testing.T) {
	if r, err := Staleness(t.TempDir(), nil, 100); err != nil || r.Stale {
		t.Errorf("nil index must be a clean not-stale no-op; got %+v err=%v", r, err)
	}
}
