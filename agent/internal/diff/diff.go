// Package diff produces + applies unified diffs for the agent's
// multi-file flow. We don't need a full myers-perfect output —
// the CLI shows diffs for human approval and applies them by
// replacing the whole file. The unified format is what users
// expect from `diff -u`, so we emit that shape verbatim.
package diff

import (
	"errors"
	"fmt"
	"strings"
)

// GenerateUnifiedDiff renders a unified diff between original and
// modified. `filename` is used for the `---`/`+++` header lines;
// `contextLines` controls how many unchanged lines flank each
// hunk (3 is the standard `diff -u` default).
//
// Identical inputs produce the empty string — callers should
// treat that as "no change" rather than emitting an empty hunk.
func GenerateUnifiedDiff(original, modified, filename string, contextLines int) string {
	if original == modified {
		return ""
	}
	if contextLines < 0 {
		contextLines = 0
	}
	aLines := splitLines(original)
	bLines := splitLines(modified)
	ops := lcsDiff(aLines, bLines)
	hunks := segmentHunks(ops, contextLines)
	if len(hunks) == 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n", filename)
	fmt.Fprintf(&b, "+++ %s\n", filename)
	for _, h := range hunks {
		// @@ headers use 1-based starts. Counts of 0 are
		// permissible (rare — happens when a hunk is purely an
		// insertion with no preceding context to anchor it).
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n",
			h.origStart+1, h.origCount, h.modStart+1, h.modCount)
		for _, line := range h.lines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Apply applies a unified diff to original and returns the
// modified text. The implementation is permissive: we anchor
// hunks by matching their leading context lines in the source
// rather than trusting the @@ header offsets, so diffs still
// apply when the surrounding file shifted slightly.
//
// Returns an error when a hunk can't be located — callers should
// surface that as "manual review required" rather than retrying.
func Apply(original, unified string) (string, error) {
	if strings.TrimSpace(unified) == "" {
		return original, nil
	}
	src := splitLines(original)
	hunks, err := parseHunks(unified)
	if err != nil {
		return "", err
	}
	out := make([]string, 0, len(src))
	cursor := 0
	for _, h := range hunks {
		anchor := h.findIn(src, cursor)
		if anchor < 0 {
			return "", fmt.Errorf("diff: hunk near line %d failed to apply", h.origStart+1)
		}
		out = append(out, src[cursor:anchor]...)
		i := anchor
		for _, op := range h.ops {
			switch op.kind {
			case ' ':
				if i >= len(src) {
					return "", fmt.Errorf("diff: hunk overruns file")
				}
				out = append(out, src[i])
				i++
			case '-':
				if i >= len(src) {
					return "", fmt.Errorf("diff: hunk overruns file")
				}
				i++
			case '+':
				out = append(out, op.text)
			}
		}
		cursor = i
	}
	out = append(out, src[cursor:]...)
	return joinLines(out, strings.HasSuffix(original, "\n")), nil
}

// ─── LCS-driven diff ─────────────────────────────────

type opKind byte

const (
	opEqual  opKind = ' '
	opDelete opKind = '-'
	opInsert opKind = '+'
)

type op struct {
	kind opKind
	text string
}

// lcsDiff returns the operations needed to turn a into b using an
// O(n*m) LCS table. Lines are compared by exact equality —
// trailing-whitespace differences show up as edits, which is the
// honest answer (a diff hides nothing).
func lcsDiff(a, b []string) []op {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	var ops []op
	i, j := n, m
	for i > 0 && j > 0 {
		if a[i-1] == b[j-1] {
			ops = append(ops, op{kind: opEqual, text: a[i-1]})
			i--
			j--
		} else if dp[i-1][j] >= dp[i][j-1] {
			ops = append(ops, op{kind: opDelete, text: a[i-1]})
			i--
		} else {
			ops = append(ops, op{kind: opInsert, text: b[j-1]})
			j--
		}
	}
	for i > 0 {
		ops = append(ops, op{kind: opDelete, text: a[i-1]})
		i--
	}
	for j > 0 {
		ops = append(ops, op{kind: opInsert, text: b[j-1]})
		j--
	}
	for l, r := 0, len(ops)-1; l < r; l, r = l+1, r-1 {
		ops[l], ops[r] = ops[r], ops[l]
	}
	return ops
}

// ─── Hunk segmentation ───────────────────────────────

type hunk struct {
	origStart, origCount int
	modStart, modCount   int
	lines                []string // " ctx", "-del", "+add"
}

// segmentHunks groups the op stream into unified hunks. The
// algorithm scans once tracking cumulative orig/mod line indexes;
// each hunk holds at most `contextLines` of leading + trailing
// equal lines, and two consecutive change blocks separated by
// more than 2*contextLines equals become two hunks.
func segmentHunks(ops []op, contextLines int) []hunk {
	var hunks []hunk

	// Annotate each op with its position so trimming context
	// later doesn't lose track of starting line numbers.
	type annotated struct {
		op
		origPos, modPos int // 0-based position before this op
	}
	ann := make([]annotated, 0, len(ops))
	origIdx, modIdx := 0, 0
	for _, o := range ops {
		a := annotated{op: o, origPos: origIdx, modPos: modIdx}
		ann = append(ann, a)
		switch o.kind {
		case opEqual:
			origIdx++
			modIdx++
		case opDelete:
			origIdx++
		case opInsert:
			modIdx++
		}
	}

	// Walk and split into change groups separated by equal runs
	// longer than 2*contextLines.
	i := 0
	for i < len(ann) {
		// Skip leading equals until we find a change.
		j := i
		for j < len(ann) && ann[j].kind == opEqual {
			j++
		}
		if j == len(ann) {
			break
		}
		// Hunk start: rewind contextLines equals from j (or i,
		// whichever bound applies).
		startEquals := j - i
		if startEquals > contextLines {
			startEquals = contextLines
		}
		hunkStart := j - startEquals

		// Find end of changes: walk forward; close when we hit
		// > 2*contextLines consecutive equals OR end of stream.
		k := j
		for k < len(ann) {
			if ann[k].kind != opEqual {
				k++
				continue
			}
			// Look ahead — how many equals in a row?
			run := 0
			for k+run < len(ann) && ann[k+run].kind == opEqual {
				run++
			}
			if run > 2*contextLines && k+run < len(ann) {
				// Gap is large enough to split — close current
				// hunk after contextLines of trailing context.
				break
			}
			k += run
		}
		// Trim trailing equals beyond contextLines.
		trail := 0
		for k > j && ann[k-1].kind == opEqual {
			trail++
			k--
			if trail >= contextLines {
				break
			}
		}
		// Re-walk trailing equals up to contextLines (we may
		// have over-trimmed in the loop above; recompute).
		end := k + contextLines
		if end > len(ann) {
			end = len(ann)
		}
		for end > k && ann[end-1].kind != opEqual {
			end--
		}
		if end < k {
			end = k
		}

		// Build the hunk from ann[hunkStart:end].
		var lines []string
		var origCount, modCount int
		for _, a := range ann[hunkStart:end] {
			switch a.kind {
			case opEqual:
				lines = append(lines, " "+a.text)
				origCount++
				modCount++
			case opDelete:
				lines = append(lines, "-"+a.text)
				origCount++
			case opInsert:
				lines = append(lines, "+"+a.text)
				modCount++
			}
		}
		first := ann[hunkStart]
		hunks = append(hunks, hunk{
			origStart: first.origPos,
			origCount: origCount,
			modStart:  first.modPos,
			modCount:  modCount,
			lines:     lines,
		})

		// Advance i past this hunk's trailing context.
		i = end
	}
	return hunks
}

// ─── Parsing unified diffs ───────────────────────────

type parsedHunk struct {
	origStart int // 0-based
	ops       []parsedOp
}

type parsedOp struct {
	kind byte
	text string
}

// findIn locates this hunk in src by matching the leading context
// lines. We prefer the stated origStart if it works; otherwise
// scan forward from cursor so a slightly-shifted file still
// patches cleanly.
func (h parsedHunk) findIn(src []string, cursor int) int {
	context := []string{}
	for _, op := range h.ops {
		if op.kind == ' ' || op.kind == '-' {
			context = append(context, op.text)
			if len(context) >= 3 {
				break
			}
		}
		if op.kind == '+' && len(context) > 0 {
			break
		}
	}
	tryAt := func(start int) bool {
		if start < 0 || start+len(context) > len(src) {
			return false
		}
		for i, line := range context {
			if src[start+i] != line {
				return false
			}
		}
		return true
	}
	if h.origStart >= cursor && tryAt(h.origStart) {
		return h.origStart
	}
	if len(context) == 0 {
		// Pure insertion at top — apply at cursor.
		return cursor
	}
	for i := cursor; i+len(context) <= len(src); i++ {
		if tryAt(i) {
			return i
		}
	}
	return -1
}

func parseHunks(unified string) ([]parsedHunk, error) {
	lines := strings.Split(unified, "\n")
	var hunks []parsedHunk
	var cur *parsedHunk
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ "):
			continue
		case strings.HasPrefix(line, "@@"):
			if cur != nil {
				hunks = append(hunks, *cur)
			}
			origStart, err := parseHeaderStart(line)
			if err != nil {
				return nil, err
			}
			cur = &parsedHunk{origStart: origStart}
		case cur == nil:
			continue
		case strings.HasPrefix(line, " ") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, "+"):
			cur.ops = append(cur.ops, parsedOp{kind: line[0], text: line[1:]})
		case line == "":
			// Trailing blank — ignore.
		}
	}
	if cur != nil {
		hunks = append(hunks, *cur)
	}
	if len(hunks) == 0 {
		return nil, errors.New("diff: no hunks found")
	}
	return hunks, nil
}

func parseHeaderStart(header string) (int, error) {
	// "@@ -3,4 +3,5 @@"
	open := strings.Index(header, "-")
	if open < 0 {
		return 0, errors.New("diff: bad @@ header")
	}
	rest := header[open+1:]
	end := strings.IndexAny(rest, ", ")
	if end < 0 {
		return 0, errors.New("diff: bad @@ header")
	}
	var n int
	if _, err := fmt.Sscanf(rest[:end], "%d", &n); err != nil {
		return 0, err
	}
	return n - 1, nil
}

// ─── line helpers ────────────────────────────────────

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	out := strings.Split(s, "\n")
	// strings.Split on "a\n" yields ["a", ""] — drop the trailing
	// empty when present so callers reason in terms of real lines.
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func joinLines(lines []string, trailingNewline bool) string {
	out := strings.Join(lines, "\n")
	if trailingNewline && out != "" {
		out += "\n"
	}
	return out
}
