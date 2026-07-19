package lens

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// canonical.go — the agent-side mirror of Lens's CANONICAL CONTENT byte definition (talyvor-lens
// internal/outputverify/content.go, merged @ 5b0b3d1). At capture time Lens stores
// output_content_sha256 = hex(sha256(canonical(assistant text))) and the artifact commit endpoint folds
// THAT hash into the output slot — so an artifact commitment is satisfiable only by a tree whose slot file
// byte-equals the canonical text. This function must therefore stay byte-for-byte identical to Lens's
// canonicalize (and to the historical stripFences it was pinned to):
//
//  1. s = strings.TrimSpace(text)                       — outer whitespace only; interior bytes
//     (including CRLF) are untouched
//  2. if s starts with "```": drop through the first "\n" (the opening fence line)
//  3. if the LAST "```" is s's suffix (nothing but that fence after it): drop it and any
//     newlines immediately before it
//  4. append "\n" unless s already ends with one         — exactly one trailing newline
//
// The vectors in canonical_test.go mirror Lens's (cross-repo, so not importable) — including the two
// deliberate quirks (prose after a closing fence survives; a blank line straight after the opening fence
// is preserved). Drift here makes every artifact commit unsatisfiable: treat as frozen.
func CanonicalContent(text string) string {
	out := strings.TrimSpace(text)
	if strings.HasPrefix(out, "```") {
		if i := strings.Index(out, "\n"); i >= 0 {
			out = out[i+1:]
		}
	}
	if j := strings.LastIndex(out, "```"); j >= 0 && strings.TrimSpace(out[j:]) == "```" {
		out = strings.TrimRight(out[:j], "\n")
	}
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}

// CanonicalContentSHA256 is hex(sha256(CanonicalContent(text))) — the exact value Lens captured as the
// generation's output_content_sha256. The commit rule compares THIS against the hash of the bytes on
// disk: commit iff they are equal (anything else can never be attested and must not be committed).
func CanonicalContentSHA256(text string) string {
	sum := sha256.Sum256([]byte(CanonicalContent(text)))
	return hex.EncodeToString(sum[:])
}
