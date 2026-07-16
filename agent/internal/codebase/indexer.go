// Package codebase indexes a workspace tree for the CLI agent.
// The index gives the planner a coarse map of the repo (languages,
// counts, branch, repo name) plus a path-matching helper the
// agent uses for smart file discovery. Heavy AST work stays out
// of scope — we keep this lightweight so it's cheap to run on
// every `run`/`review` invocation.
package codebase

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	gitpkg "github.com/talyvor/code/internal/git"
)

// FileInfo carries the minimal per-file metadata the planner sees.
// We deliberately don't include the file content — that loads
// later only for files the plan actually targets.
type FileInfo struct {
	Path     string
	Language string
	Size     int64
	Lines    int
}

// CodebaseIndex is the snapshot returned by IndexDirectory.
type CodebaseIndex struct {
	Root       string
	Files      []FileInfo
	Languages  map[string]int // language → file count
	TotalSize  int64
	TotalLines int
	GitBranch  string
	GitRepo    string
}

// DefaultMaxFiles caps a single index — beyond it the planner
// loses signal anyway. Callers pass 0 to take the default.
const DefaultMaxFiles = 500

// skipDirs are walked-into but ignored at the top level. We
// match exact basenames here; nested copies (e.g. workspaces/x/
// node_modules) are caught the same way.
var skipDirs = map[string]bool{
	".git":         true,
	".talyvor":     true, // the semantic index's OWN cache dir — never index the index artifact
	"node_modules": true,
	"vendor":       true,
	".next":        true,
	"dist":         true,
	"__pycache__":  true,
	"build":        false, // common but not always generated — keep
}

// skipExts are file suffixes we never index — minified bundles
// and lockfiles add weight without informing the planner.
var skipSuffixes = []string{
	".min.js",
	".min.css",
	".lock",
	"-lock.json",
}

// IndexDirectory walks root and returns a CodebaseIndex. Returns
// after maxFiles entries are collected so a huge monorepo doesn't
// OOM the planner. Walks the whole tree past that — we just stop
// adding to the slice.
func IndexDirectory(root string, maxFiles int) (*CodebaseIndex, error) {
	if maxFiles <= 0 {
		maxFiles = DefaultMaxFiles
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	idx := &CodebaseIndex{
		Root:      abs,
		Files:     make([]FileInfo, 0, 128),
		Languages: map[string]int{},
	}
	walkErr := filepath.WalkDir(abs, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Permission failures on a single file shouldn't
			// abort the whole walk.
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if len(idx.Files) >= maxFiles {
			return filepath.SkipAll
		}
		base := d.Name()
		for _, suf := range skipSuffixes {
			if strings.HasSuffix(base, suf) {
				return nil
			}
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(abs, path)
		if rel == "" {
			rel = path
		}
		lines := countLines(path, info.Size())
		lang := DetectLanguage(path)
		idx.Files = append(idx.Files, FileInfo{
			Path:     rel,
			Language: lang,
			Size:     info.Size(),
			Lines:    lines,
		})
		idx.Languages[lang]++
		idx.TotalSize += info.Size()
		idx.TotalLines += lines
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	// Best-effort git context. Missing git or no remote is fine
	// — we just leave the fields empty.
	if branch, err := gitpkg.GetCurrentBranch(); err == nil {
		idx.GitBranch = branch
	}
	if repo, err := gitpkg.GetRepoName(); err == nil {
		idx.GitRepo = repo
	}
	return idx, nil
}

// countLines streams the file with a single read of up to 256KB.
// We don't need exact line counts past that — a planner doesn't
// care whether a generated file has 50k or 500k lines.
func countLines(path string, size int64) int {
	const cap = 256 * 1024
	read := size
	if read > cap {
		read = cap
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	buf := make([]byte, read)
	n, _ := f.Read(buf)
	if n == 0 {
		return 0
	}
	count := bytes.Count(buf[:n], []byte("\n"))
	// Treat the last line without a trailing \n as a line too.
	if n > 0 && buf[n-1] != '\n' {
		count++
	}
	return count
}

// Summary renders the index as a short paragraph suitable for
// dropping into a planning prompt. We surface the most common
// languages first so the model knows what stack to target.
func (idx *CodebaseIndex) Summary() string {
	var b strings.Builder
	repo := idx.GitRepo
	if repo == "" {
		repo = filepath.Base(idx.Root)
	}
	fmt.Fprintf(&b, "Repository: %s\n", repo)
	if idx.GitBranch != "" {
		fmt.Fprintf(&b, "Branch: %s\n", idx.GitBranch)
	}
	// Top languages by file count, ties broken alphabetically.
	type lc struct {
		lang  string
		count int
	}
	rows := make([]lc, 0, len(idx.Languages))
	for k, v := range idx.Languages {
		rows = append(rows, lc{k, v})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].count != rows[j].count {
			return rows[i].count > rows[j].count
		}
		return rows[i].lang < rows[j].lang
	})
	if len(rows) > 0 {
		parts := make([]string, 0, len(rows))
		for i, r := range rows {
			if i >= 6 {
				break
			}
			parts = append(parts, fmt.Sprintf("%s (%d files)", r.lang, r.count))
		}
		fmt.Fprintf(&b, "Languages: %s\n", strings.Join(parts, ", "))
	}
	fmt.Fprintf(&b, "Total: %d files, %d lines\n", len(idx.Files), idx.TotalLines)
	return b.String()
}

// FindRelevantFiles ranks files by a coarse path-substring score
// against the query terms. We split the query on whitespace and
// give each term equal weight — the goal is "did the planner
// mention this filename?" not a full IR engine.
func (idx *CodebaseIndex) FindRelevantFiles(query string, limit int) []FileInfo {
	if limit <= 0 {
		limit = 10
	}
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return nil
	}
	type scored struct {
		file  FileInfo
		score int
	}
	all := make([]scored, 0, len(idx.Files))
	for _, f := range idx.Files {
		lp := strings.ToLower(f.Path)
		s := 0
		for _, t := range terms {
			if t == "" {
				continue
			}
			if strings.Contains(lp, t) {
				s += 2
			}
			// Filename basename hits weighted higher than directory hits.
			if strings.Contains(strings.ToLower(filepath.Base(lp)), t) {
				s++
			}
		}
		if s > 0 {
			all = append(all, scored{file: f, score: s})
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].file.Path < all[j].file.Path
	})
	if len(all) > limit {
		all = all[:limit]
	}
	out := make([]FileInfo, len(all))
	for i, s := range all {
		out[i] = s.file
	}
	return out
}

// DetectLanguage maps a path's extension to a friendly language
// name. Unknown extensions return "Other" so callers can
// distinguish them from "no extension".
func DetectLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "Go"
	case ".ts":
		return "TypeScript"
	case ".tsx":
		return "TypeScript"
	case ".js":
		return "JavaScript"
	case ".jsx":
		return "JavaScript"
	case ".py":
		return "Python"
	case ".rs":
		return "Rust"
	case ".java":
		return "Java"
	case ".kt":
		return "Kotlin"
	case ".rb":
		return "Ruby"
	case ".swift":
		return "Swift"
	case ".cs":
		return "C#"
	case ".cpp", ".cxx", ".cc":
		return "C++"
	case ".c", ".h":
		return "C"
	case ".php":
		return "PHP"
	case ".md", ".markdown":
		return "Markdown"
	case ".json":
		return "JSON"
	case ".yaml", ".yml":
		return "YAML"
	case ".toml":
		return "TOML"
	case ".html", ".htm":
		return "HTML"
	case ".css":
		return "CSS"
	case ".scss", ".sass":
		return "Sass"
	case ".sh", ".bash":
		return "Shell"
	case ".sql":
		return "SQL"
	}
	return "Other"
}
