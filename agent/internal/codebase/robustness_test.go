package codebase

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// bigIndex builds a persisted-index payload large enough that a non-atomic (truncate-
// then-write) Save is caught mid-write by a concurrent reader → a torn/partial JSON.
func bigIndex(n int) *SemanticIndex {
	idx := &SemanticIndex{
		Version:    IndexVersion,
		EmbedModel: DefaultEmbedModel,
		FileHashes: make(map[string]string, n),
		Chunks:     make([]Chunk, n),
		Vectors:    make([][]float32, n),
	}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("pkg/file_%04d.go", i)
		idx.Chunks[i] = Chunk{File: name, Language: "Go", StartLine: 1, EndLine: 40, Content: strings.Repeat("x", 160)}
		idx.FileHashes[name] = strings.Repeat("a", 64)
		v := make([]float32, 64)
		for j := range v {
			v[j] = float32(i + j)
		}
		idx.Vectors[i] = v
	}
	return idx
}

// TestSave_AtomicUnderConcurrentLoad — a serving command that Loads the index while
// `index` is rewriting it must never observe a torn file. Writers rewrite a large index
// repeatedly while readers Load concurrently; with a non-atomic Save a reader catches a
// truncated write and LoadIndex returns a parse error. Atomic (temp-then-rename) Save
// makes every read see either the old or new whole file.
func TestSave_AtomicUnderConcurrentLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codebase-index.json")
	big := bigIndex(2000)
	if err := big.Save(path); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var mu sync.Mutex
	var firstErr error
	record := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}

	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-done:
					return
				default:
					if _, err := LoadIndex(path); err != nil {
						record(err)
						return
					}
				}
			}
		}()
	}
	for i := 0; i < 120; i++ {
		if err := big.Save(path); err != nil {
			record(err)
			break
		}
	}
	close(done)
	readers.Wait()

	if firstErr != nil {
		t.Fatalf("a concurrent Load saw a TORN write — Save is not atomic: %v", firstErr)
	}
}

// TestSave_LeavesNoTempFile — atomic Save must clean up after itself: no leftover temp
// siblings in the index dir (which would otherwise get picked up or clutter .talyvor).
func TestSave_LeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codebase-index.json")
	if err := bigIndex(10).Save(path); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "codebase-index.json" {
			t.Errorf("Save left an unexpected sibling file %q (temp not cleaned up)", e.Name())
		}
	}
}

// TestLoadIndex_RejectsVersionMismatch — a future/unknown schema version must fail LOUD
// (an error naming the version), never silently parse into a mis-interpreted index.
func TestLoadIndex_RejectsVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codebase-index.json")

	// Current version loads cleanly.
	cur := &SemanticIndex{Version: IndexVersion, Chunks: []Chunk{{File: "a.go"}}, Vectors: [][]float32{{1}}}
	if err := cur.Save(path); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadIndex(path); err != nil || got == nil {
		t.Fatalf("a current-version index must load; got=%v err=%v", got, err)
	}

	// A newer version must be rejected by this loader.
	future := &SemanticIndex{Version: IndexVersion + 1, Chunks: []Chunk{{File: "a.go"}}, Vectors: [][]float32{{1}}}
	if err := future.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadIndex(path)
	if err == nil {
		t.Fatalf("a version-%d index must be REJECTED by a version-%d loader, not parsed; got %+v", IndexVersion+1, IndexVersion, got)
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("the rejection error must name the version mismatch; got %v", err)
	}
}

// TestLoadIndex_RejectsLegacyUnversioned — a pre-versioning artifact (no "version"
// field → parses to 0) must also fail loud, forcing a clean rebuild rather than being
// read as if it were the current schema.
func TestLoadIndex_RejectsLegacyUnversioned(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codebase-index.json")
	if err := os.WriteFile(path, []byte(`{"chunks":[{"file":"a.go"}],"vectors":[[1]]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIndex(path); err == nil {
		t.Error("a legacy unversioned (version 0) index must fail loud, forcing a rebuild")
	}
}
