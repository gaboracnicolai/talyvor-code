package codebase

import "strings"

// Chunk is a retrievable span of one file — the unit the semantic index embeds and
// retrieval returns. StartLine/EndLine are 1-based inclusive so callers can cite a
// precise span ("internal/auth/auth.go:5-12").
type Chunk struct {
	File      string `json:"file"`
	Language  string `json:"language"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Content   string `json:"content"`
}

const (
	chunkWindowLines  = 50  // fallback line-window size
	chunkOverlapLines = 10  // overlap so a match spanning a boundary is still whole in one window
	chunkMaxLines     = 160 // a single declaration larger than this is sub-windowed
)

// ChunkFile splits one file's content into retrievable chunks. It is
// declaration-aware for Go where that is cheap (each top-level func/type/const/var
// becomes a chunk carrying its doc comment, so a retrieved chunk is a coherent
// unit), and falls back to overlapping line windows for everything else — or for a
// Go file with no top-level declarations. Pure: no IO, no network. Whitespace-only
// input yields no chunks.
func ChunkFile(relPath, content string) []Chunk {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	lang := DetectLanguage(relPath)
	lines := splitLines(content)
	if lang == "Go" {
		if cs := chunkGo(relPath, lang, lines); len(cs) > 0 {
			return cs
		}
	}
	return windowRange(relPath, lang, lines, 1, len(lines))
}

// splitLines splits on "\n" and drops the trailing empty element a final newline
// produces, so line counts match the human view of the file.
func splitLines(content string) []string {
	lines := strings.Split(content, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// chunkGo splits at top-level declaration boundaries. A boundary is a col-0
// func/type/const/var line (nested decls are indented, so col-0 is a cheap, robust
// "top-level" test); each boundary absorbs its immediately-preceding // comment
// block. Everything before the first declaration (package + imports) is the header
// chunk. Returns nil when no declaration is found, so ChunkFile falls back to
// windows.
func chunkGo(relPath, lang string, lines []string) []Chunk {
	total := len(lines)
	isDecl := func(s string) bool {
		return strings.HasPrefix(s, "func ") || strings.HasPrefix(s, "func(") ||
			strings.HasPrefix(s, "type ") || strings.HasPrefix(s, "const ") || strings.HasPrefix(s, "var ")
	}
	var starts []int // 1-based chunk start lines, strictly increasing
	for i := 0; i < total; i++ {
		if !isDecl(lines[i]) {
			continue
		}
		b := i // 0-based; extend up over a contiguous // comment block
		for b-1 >= 0 && strings.HasPrefix(lines[b-1], "//") {
			b--
		}
		s := b + 1
		if len(starts) == 0 || s > starts[len(starts)-1] {
			starts = append(starts, s)
		}
	}
	if len(starts) == 0 {
		return nil
	}
	var out []Chunk
	if starts[0] > 1 {
		out = append(out, mkChunk(relPath, lang, lines, 1, starts[0]-1))
	}
	for k := range starts {
		s := starts[k]
		e := total
		if k+1 < len(starts) {
			e = starts[k+1] - 1
		}
		if e-s+1 > chunkMaxLines {
			out = append(out, windowRange(relPath, lang, lines, s, e)...)
		} else {
			out = append(out, mkChunk(relPath, lang, lines, s, e))
		}
	}
	return out
}

// windowRange emits overlapping line windows covering [from,to] (1-based). Every
// window after the first starts `chunkOverlapLines` before the previous end, and
// the final window reaches `to`.
func windowRange(relPath, lang string, lines []string, from, to int) []Chunk {
	if from < 1 {
		from = 1
	}
	if to > len(lines) {
		to = len(lines)
	}
	if to < from {
		return nil
	}
	var out []Chunk
	start := from
	for {
		end := start + chunkWindowLines - 1
		if end > to {
			end = to
		}
		out = append(out, mkChunk(relPath, lang, lines, start, end))
		if end >= to {
			break
		}
		start = end - chunkOverlapLines + 1
		if start < from {
			start = from
		}
	}
	return out
}

// mkChunk materialises the [start,end] (1-based inclusive) span.
func mkChunk(relPath, lang string, lines []string, start, end int) Chunk {
	return Chunk{
		File:      relPath,
		Language:  lang,
		StartLine: start,
		EndLine:   end,
		Content:   strings.Join(lines[start-1:end], "\n"),
	}
}
