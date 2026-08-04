package codebase

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// .talyvor/codebase-index.json IS A FULL COPY OF THE USER'S SOURCE.
//
// Chunk.Content holds raw file text, not just vectors, and the index is written to the REPO ROOT.
// Nothing stopped it being committed: a routine `git add -A` sweeps the user's entire codebase into
// whatever remote that repo pushes to — which for a private repo mirrored to a public fork, or a
// repo shared with a contractor, is a disclosure the user never chose.
//
// ⚠ THE OLD COMMENT MADE IT WORSE: "The index never leaves the repo" reads as a reassurance and is
// precisely backwards — the danger is not that it leaves, it is that it STAYS, inside a directory
// the user routinely commits.
//
// The fix is a self-ignoring directory: .talyvor/.gitignore containing "*". git honours a .gitignore
// in ANY directory, so the whole index directory becomes uncommittable without touching the user's
// own root .gitignore — writing to a file they authored is a surprising side effect, and one that
// merge-conflicts. Writing outside the repo was the alternative; see EnsureIndexDirIgnored for why
// this was chosen instead.
func TestIndexDirIsSelfIgnoring(t *testing.T) {
	root := t.TempDir()
	if err := EnsureIndexDirIgnored(root); err != nil {
		t.Fatalf("EnsureIndexDirIgnored: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, indexDir, ".gitignore"))
	if err != nil {
		t.Fatalf("no .gitignore inside %s — the index directory is committable: %v", indexDir, err)
	}
	if !strings.Contains(string(b), "*") {
		t.Fatalf(".talyvor/.gitignore = %q, want it to ignore everything in the directory", b)
	}
}

// The user's own .gitignore must NOT be edited — it is a file they authored, it merge-conflicts,
// and a tool that rewrites it is a tool people stop trusting.
func TestEnsureIndexDirIgnored_DoesNotTouchTheUsersGitignore(t *testing.T) {
	root := t.TempDir()
	users := filepath.Join(root, ".gitignore")
	const original = "node_modules/\ndist/\n"
	if err := os.WriteFile(users, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := EnsureIndexDirIgnored(root); err != nil {
		t.Fatalf("EnsureIndexDirIgnored: %v", err)
	}
	got, err := os.ReadFile(users)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != original {
		t.Fatalf("the user's .gitignore was modified:\n got %q\nwant %q", got, original)
	}
}

// Idempotent: indexing runs repeatedly, and a second run must not append or duplicate.
func TestEnsureIndexDirIgnored_IsIdempotent(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := EnsureIndexDirIgnored(root); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(root, indexDir, ".gitignore"))
	if n := strings.Count(string(b), "*"); n != 1 {
		t.Fatalf("wrote the pattern %d times, want 1: %q", n, b)
	}
}

// ⚠ POSITIVE CONTROL ON THE GUARD ITSELF: prove the test can fail. A directory with no .gitignore
// must be detected as committable — otherwise the first test would pass on a system that never
// writes one.
func TestIndexDirWithoutTheIgnoreIsDetectedAsCommittable(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, indexDir), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := os.ReadFile(filepath.Join(root, indexDir, ".gitignore")); err == nil {
		t.Fatal("a bare index dir reported an ignore file — the check cannot distinguish the states")
	}
}
