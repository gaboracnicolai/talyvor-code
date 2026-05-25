package github

// MaxDiffChars caps PR diffs at roughly 8000 tokens for the
// model. Larger diffs still get reviewed — we just show the
// model both ends of the change rather than letting the middle
// swamp the context window.
const MaxDiffChars = 32000

// TruncateDiff returns diff verbatim when it fits within
// maxChars; otherwise it splits in half and inserts a marker so
// the model sees both the start and end of the change.
//
// The middle (often boilerplate test fixture or generated code)
// is the part most safely dropped — bugs and architectural
// changes tend to cluster at the top or bottom of a diff.
func TruncateDiff(diff string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = MaxDiffChars
	}
	if len(diff) <= maxChars {
		return diff
	}
	half := maxChars / 2
	head := diff[:half]
	tail := diff[len(diff)-half:]
	return head + "\n\n... [diff truncated — showing representative sample] ...\n\n" + tail
}
