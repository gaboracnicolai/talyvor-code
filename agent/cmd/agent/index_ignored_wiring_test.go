package main

import (
	"os"
	"strings"
	"testing"
)

// ⚠ THE WIRING, NOT THE FUNCTION. EnsureIndexDirIgnored existing proves nothing; a guard with no
// caller is the shape this project has been closing all week. This asserts the index command calls
// it, and calls it BEFORE the write — so an interrupted build never leaves a committable copy of
// the user's source behind.
func TestIndexCommandIgnoresTheDirBeforeWriting(t *testing.T) {
	src, err := os.ReadFile("index_cmd.go")
	if err != nil {
		t.Fatalf("read index_cmd.go: %v", err)
	}
	s := string(src)
	call := strings.Index(s, "codebase.EnsureIndexDirIgnored(root)")
	if call < 0 {
		t.Fatal("index_cmd.go never calls codebase.EnsureIndexDirIgnored — the index directory " +
			"stays committable, and it holds raw source text")
	}
	// It must precede the point where an index is persisted.
	if save := strings.Index(s, "SaveIndex"); save >= 0 && call > save {
		t.Fatal("EnsureIndexDirIgnored runs AFTER the index is saved — there is a window in which " +
			"a full copy of the user's source is committable")
	}
}
