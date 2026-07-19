package lens

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// canonical_test.go — the agent-side mirror of Lens's canonical-content byte definition
// (talyvor-lens internal/outputverify/content.go @ 5b0b3d1). Lens's vectors are NOT importable across
// repos, so these MIRROR the same vector classes byte-for-byte: fences, trailing newline, CRLF,
// surrounding whitespace, and the two flagship quirks Lens deliberately preserves. If any vector here
// drifts from Lens's, every artifact commit becomes unsatisfiable — treat the expected bytes as frozen.
func TestCanonicalContent_MirrorsLensSpec(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bare code, no trailing newline → exactly one appended",
			in:   "package main\nfunc main(){}",
			want: "package main\nfunc main(){}\n",
		},
		{
			name: "bare code, exactly one trailing newline → identity",
			in:   "package main\nfunc main(){}\n",
			want: "package main\nfunc main(){}\n",
		},
		{
			name: "fenced code block → fences dropped",
			in:   "```go\npackage main\nfunc main(){}\n```",
			want: "package main\nfunc main(){}\n",
		},
		{
			name: "fenced with surrounding whitespace → trimmed then dropped",
			in:   "\n```go\ncode()\n```\n",
			want: "code()\n",
		},
		{
			name: "prose after closing fence survives (flagship quirk)",
			in:   "```go\ncode()\n```\nThanks!",
			want: "code()\n```\nThanks!\n",
		},
		{
			name: "blank line after opening fence preserved (flagship quirk)",
			in:   "```go\n\ncode()\n```",
			want: "\ncode()\n",
		},
		{
			name: "interior CRLF preserved byte-for-byte",
			in:   "a\r\nb",
			want: "a\r\nb\n",
		},
		{
			name: "fence-only degenerates to a single newline",
			in:   "```",
			want: "\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CanonicalContent(c.in); got != c.want {
				t.Errorf("CanonicalContent(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// CanonicalContentSHA256 is definitionally hex(sha256(CanonicalContent(text))) — the value Lens captured
// as output_content_sha256 and the ONLY hash a committed artifact's output slot can carry.
func TestCanonicalContentSHA256_IsHashOfCanonicalBytes(t *testing.T) {
	const text = "package main\nfunc main(){}"
	sum := sha256.Sum256([]byte(CanonicalContent(text)))
	if got, want := CanonicalContentSHA256(text), hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("sha = %s, want %s", got, want)
	}
}
