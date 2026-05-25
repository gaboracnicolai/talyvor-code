package codebase

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFiles materialises the supplied path → content map under
// root, creating any intermediate directories. Returns the root
// so tests can chain calls without juggling paths.
func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, body := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
}

func TestIndexDirectory_CountsFilesAndLanguages(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"a.go":          "package a\n\nfunc A() {}\n",
		"b.go":          "package b\nvar X = 1\n",
		"src/foo.ts":    "export const x = 1;\nexport const y = 2;\n",
		"src/util.ts":   "export {};\n",
		"README.md":     "# project\n",
	})
	idx, err := IndexDirectory(dir, 500)
	if err != nil {
		t.Fatalf("IndexDirectory: %v", err)
	}
	if len(idx.Files) != 5 {
		t.Fatalf("file count = %d, want 5\nfiles=%+v", len(idx.Files), idx.Files)
	}
	if idx.Languages["Go"] != 2 {
		t.Errorf("Go count = %d, want 2", idx.Languages["Go"])
	}
	if idx.Languages["TypeScript"] != 2 {
		t.Errorf("TypeScript count = %d, want 2", idx.Languages["TypeScript"])
	}
	if idx.TotalLines == 0 {
		t.Error("TotalLines is zero")
	}
}

func TestIndexDirectory_SkipsBlockedPaths(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"src/app.ts":              "x\n",
		"node_modules/lib/x.js":   "x\n",
		".git/HEAD":               "ref: refs/heads/main\n",
		"vendor/pkg/util.go":      "package util\n",
		".next/build/chunk.js":    "x\n",
		"dist/bundle.min.js":      "x\n",
		"package-lock.json":       "{}\n",
		"py/__pycache__/x.pyc":    "binary",
	})
	idx, err := IndexDirectory(dir, 500)
	if err != nil {
		t.Fatalf("IndexDirectory: %v", err)
	}
	for _, f := range idx.Files {
		for _, blocked := range []string{
			"node_modules", ".git", "vendor", ".next", "dist", "__pycache__",
		} {
			if strings.Contains(f.Path, blocked) {
				t.Errorf("expected to skip %s but it was indexed", f.Path)
			}
		}
		if strings.HasSuffix(f.Path, ".min.js") {
			t.Errorf(".min.js should be skipped: %s", f.Path)
		}
		if strings.HasSuffix(f.Path, ".lock") {
			t.Errorf(".lock should be skipped: %s", f.Path)
		}
	}
	if len(idx.Files) != 1 || !strings.HasSuffix(idx.Files[0].Path, "src/app.ts") {
		t.Fatalf("expected only src/app.ts to remain, got %+v", idx.Files)
	}
}

func TestIndexDirectory_RespectsMaxFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{}
	for i := 0; i < 30; i++ {
		files[fmt.Sprintf("file%02d.go", i)] = "package p\n"
	}
	writeFiles(t, dir, files)
	idx, err := IndexDirectory(dir, 10)
	if err != nil {
		t.Fatalf("IndexDirectory: %v", err)
	}
	if len(idx.Files) != 10 {
		t.Fatalf("expected 10 files (maxFiles cap), got %d", len(idx.Files))
	}
}

func TestDetectLanguage(t *testing.T) {
	cases := map[string]string{
		"main.go":     "Go",
		"app.ts":      "TypeScript",
		"App.tsx":     "TypeScript",
		"index.js":    "JavaScript",
		"App.jsx":     "JavaScript",
		"util.py":     "Python",
		"lib.rs":      "Rust",
		"Main.java":   "Java",
		"index.rb":    "Ruby",
		"a.swift":     "Swift",
		"README.md":   "Markdown",
		"config.yaml": "YAML",
		"data.json":   "JSON",
		"style.css":   "CSS",
		"weird.xyz":   "Other",
	}
	for path, want := range cases {
		got := DetectLanguage(path)
		if got != want {
			t.Errorf("DetectLanguage(%s) = %s, want %s", path, got, want)
		}
	}
}

func TestSummary_FormatsCounts(t *testing.T) {
	idx := &CodebaseIndex{
		Root:       "/tmp/r",
		Files:      []FileInfo{{Path: "a.go"}, {Path: "b.ts"}},
		Languages:  map[string]int{"Go": 1, "TypeScript": 1},
		TotalSize:  1234,
		TotalLines: 87,
		GitBranch:  "main",
		GitRepo:    "myapp",
	}
	s := idx.Summary()
	for _, want := range []string{"myapp", "main", "Go", "TypeScript", "87"} {
		if !strings.Contains(s, want) {
			t.Errorf("Summary missing %q: %s", want, s)
		}
	}
}

func TestFindRelevantFiles_MatchesByPath(t *testing.T) {
	idx := &CodebaseIndex{
		Files: []FileInfo{
			{Path: "src/auth/jwt.ts", Language: "TypeScript"},
			{Path: "src/auth/session.ts", Language: "TypeScript"},
			{Path: "src/utils/format.ts", Language: "TypeScript"},
			{Path: "src/server.ts", Language: "TypeScript"},
		},
	}
	out := idx.FindRelevantFiles("auth", 10)
	if len(out) < 2 {
		t.Fatalf("expected ≥2 matches for 'auth', got %d (%+v)", len(out), out)
	}
	// First two results should be the auth files (jwt or session).
	for _, f := range out[:2] {
		if !strings.Contains(f.Path, "auth") {
			t.Errorf("expected auth match in top results, got %s", f.Path)
		}
	}
}

func TestFindRelevantFiles_RespectsLimit(t *testing.T) {
	idx := &CodebaseIndex{
		Files: []FileInfo{
			{Path: "a/x.go"}, {Path: "b/x.go"}, {Path: "c/x.go"}, {Path: "d/x.go"},
		},
	}
	out := idx.FindRelevantFiles("x.go", 2)
	if len(out) != 2 {
		t.Fatalf("expected 2 results, got %d", len(out))
	}
}
