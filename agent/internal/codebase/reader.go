package codebase

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// DefaultMaxFileBytes is the per-file read cap. 100KB matches
// the spec — beyond that the planner gets diluted and we waste
// tokens.
const DefaultMaxFileBytes int64 = 100 * 1024

// DefaultMaxTotalBytes is the cap for ReadFilesForContext when
// the caller passes 0. Matches the spec's 500KB ceiling.
const DefaultMaxTotalBytes int64 = 500 * 1024

// ReadFile returns the file's content, truncating to maxBytes
// and appending a marker so downstream prompts know the model is
// not seeing the whole file.
func ReadFile(path string, maxBytes int64) (string, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxFileBytes
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	// Read maxBytes + 1 so we can detect when truncation
	// actually happened (full file fit ≤ maxBytes).
	buf, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(buf)) <= maxBytes {
		return string(buf), nil
	}
	return string(buf[:maxBytes]) + "\n... (truncated)\n", nil
}

// ReadFilesForContext concatenates multiple files for a single
// prompt with === path === headers. Stops adding files once the
// running total tops maxTotalBytes; missing files surface as
// "[error: …]" notes rather than aborting the batch.
func ReadFilesForContext(files []string, maxTotalBytes int64) (string, error) {
	if maxTotalBytes <= 0 {
		maxTotalBytes = DefaultMaxTotalBytes
	}
	var b strings.Builder
	var total int64
	for i, p := range files {
		header := fmt.Sprintf("=== %s ===\n", p)
		if total+int64(len(header)) >= maxTotalBytes {
			fmt.Fprintf(&b, "\n... (%d more files skipped — context cap reached)\n", len(files)-i)
			break
		}
		b.WriteString(header)
		total += int64(len(header))
		content, err := ReadFile(p, DefaultMaxFileBytes)
		if err != nil {
			fmt.Fprintf(&b, "[error reading %s: %v]\n\n", p, err)
			total += 80
			continue
		}
		remaining := maxTotalBytes - total
		if remaining <= 0 {
			b.WriteString("... (truncated — context cap)\n")
			break
		}
		if int64(len(content)) > remaining {
			b.WriteString(content[:remaining])
			b.WriteString("\n... (truncated — context cap)\n")
			total += remaining
			break
		}
		b.WriteString(content)
		if !strings.HasSuffix(content, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
		total += int64(len(content)) + 1
	}
	return b.String(), nil
}
