package codebase

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFile_TruncatesAtMaxBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	body := strings.Repeat("a", 1024)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadFile(path, 100)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.HasSuffix(strings.TrimRight(got, "\n"), "(truncated)") {
		t.Fatalf("expected truncation marker, got tail: %q", got[len(got)-40:])
	}
	if len(got) > 200 {
		t.Errorf("output too large (%d bytes) — marker missing or cap broken", len(got))
	}
}

func TestReadFile_FullWhenUnderLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadFile(path, 1000)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(got, "hello") {
		t.Fatalf("expected hello in output, got %q", got)
	}
	if strings.Contains(got, "(truncated)") {
		t.Fatalf("unexpected truncation marker: %q", got)
	}
}

func TestReadFile_MissingFileErrors(t *testing.T) {
	_, err := ReadFile("/no/such/path/x.txt", 100)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadFilesForContext_FormatsMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.ts")
	b := filepath.Join(dir, "b.ts")
	_ = os.WriteFile(a, []byte("const A=1;\n"), 0o644)
	_ = os.WriteFile(b, []byte("const B=2;\n"), 0o644)

	got, err := ReadFilesForContext([]string{a, b}, 10_000)
	if err != nil {
		t.Fatalf("ReadFilesForContext: %v", err)
	}
	if !strings.Contains(got, "=== "+a+" ===") {
		t.Errorf("missing header for a: %q", got)
	}
	if !strings.Contains(got, "=== "+b+" ===") {
		t.Errorf("missing header for b: %q", got)
	}
	if !strings.Contains(got, "const A=1;") || !strings.Contains(got, "const B=2;") {
		t.Errorf("body missing: %q", got)
	}
}

func TestReadFilesForContext_StopsAtMaxTotal(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	_ = os.WriteFile(a, []byte(strings.Repeat("a", 800)), 0o644)
	_ = os.WriteFile(b, []byte(strings.Repeat("b", 800)), 0o644)

	got, err := ReadFilesForContext([]string{a, b}, 500)
	if err != nil {
		t.Fatalf("ReadFilesForContext: %v", err)
	}
	if !strings.Contains(got, a) {
		t.Errorf("expected first file included: %q", got)
	}
	if strings.Contains(got, b) {
		t.Errorf("expected second file dropped past cap, got: %q", got)
	}
	if !strings.Contains(got, "truncated") && !strings.Contains(got, "skipped") {
		t.Errorf("expected truncation/skip marker, got: %q", got)
	}
}

func TestReadFilesForContext_HandlesMissingFile(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	_ = os.WriteFile(a, []byte("ok\n"), 0o644)
	got, err := ReadFilesForContext([]string{a, "/no/such/file"}, 10_000)
	if err != nil {
		t.Fatalf("ReadFilesForContext: %v", err)
	}
	if !strings.Contains(got, "ok") {
		t.Errorf("first file body missing: %q", got)
	}
	// Missing file should be noted but not abort the whole batch.
	if !strings.Contains(got, "/no/such/file") {
		t.Errorf("expected missing file mentioned: %q", got)
	}
}
