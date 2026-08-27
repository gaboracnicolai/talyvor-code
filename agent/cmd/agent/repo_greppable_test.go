package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repo_greppable_test.go — NO SOURCE FILE IN THIS REPOSITORY MAY CONTAIN A NUL BYTE, BECAUSE ONE
// BYTE MAKES A WHOLE FILE OPAQUE TO `grep` WITH NO ERROR AND NO NON-ZERO EXIT.
//
// ── THE DEFECT, MEASURED RATHER THAN REVIEWED ────────────────────────────────
//
// At main `3f9bffed`, extension/src/docs/docs-pure.ts carried FOUR raw 0x00 bytes (offsets
// 5248/5272/5713/5719): the markdown renderer lifts code spans out of the text, replaces each with
// a NUL-delimited placeholder, HTML-escapes the rest, then puts them back. The delimiter was typed
// as the character rather than as an escape.
//
// The string VALUES are unremarkable. The bytes on disk are not:
//
//	grep -c 'export' extension/src/docs/docs-pure.ts   → printed NOTHING, exit 1
//	git grep -c 'export' -- extension/src/docs/docs-pure.ts → 10
//
// A file with eleven exports, invisible to plain grep, and no signal that it happened.
//
// ⚠⚠ AND IT IS WORSE THAN grep: `git diff` CALLED THE FILE BINARY TOO, SO IT WAS NEVER
// REVIEWABLE. Measured over its whole history — the file exists in exactly TWO commits and BOTH
// render as `Binary files a/... and b/... differ`: `539fa3c` ("Phase 7 — Docs integration"), which
// INTRODUCED it, and the commit that removes the bytes. A 7KB TypeScript module — a markdown
// renderer containing `escapeHTML`, which is the function standing between docs content and the
// webview — landed with no readable diff, and every diff of it since would have been the same.
// After this change the bytes are text and `git diff` shows lines again.
//
// ⚠ AND `git grep` IS WHY IT SURVIVED. It reads the file fine. The commands in this project's
// notes are `git grep`, so the one tool everybody here uses is the one tool that is not fooled.
//
// ── HOW IT WAS FOUND, WHICH IS THE PART WORTH COPYING ────────────────────────
//
// It was not found here. The identical defect was fixed in talyvor-suite the same day
// (`36fc1700`), in that repo's request-body CENSUS MODULE, and the sweep that followed was the
// point: every repo checked with one detector, control-checked in both directions against the
// known-bad historical blob (found) and four clean repos (talyvor-lens 1371 text files,
// talyvor-docs 590, talyvor-track 656, talyvor-queue 468 — none). This repository was the only
// other hit. "The fix applied where the defect was reported and the identical shape one element
// over never swept for" is the failure this project keeps paying for; sweeping was cheaper than
// waiting.
//
// ── THE REPAIR CHANGES NOTHING, AND THAT IS MEASURED, NOT ARGUED ─────────────
//
// The escape is the same character — U+0000 either way — so only the bytes on disk moved. Proven
// rather than asserted: renderMarkdown and escapeHTML were run over 11 inputs under the OLD source
// and the NEW one and the sha256 of the combined output is identical
// (5f10fdcaab7e68990c0801b5fe2e2c816ef8747c4a82c06a32a15e8f988f9846). Two of those inputs are
// adversarial on purpose — a caller-supplied NUL in the input text, and a forged placeholder
// (NUL "0" NUL) — because those are the only inputs where the placeholder scheme could behave
// differently at all. It does not.
//
// ── WHAT THIS TEST ASSERTS, AND WHY IT LIVES IN THE GO HALF ─────────────────
//
// The rule is repo-wide, so it must run from a place that can see the whole repo. `go test ./...`
// runs in CI on every push and this package is already part of it; the extension's unit tests run
// out of `out/`, one compile step removed from the source they would be checking.
//
// Build output is skipped BY NAME (`out/`, `rel/`, `dist`, `bin`, `.gradle`): those are derived,
// they are untracked, and they carried the same bytes only because the source did. Skipping them
// is what keeps this a statement about the repository rather than about one machine's stale
// artefacts.

// ── THE CONTROLS ────────────────────────────────────────────────────────────
//
// 6 arms, `~/talyvor-queue/w423-greppable-controls-m4x7.py`, each applied ALONE against the whole
// `agent` Go suite, predicted catcher named FIRST, sha256-verified restores, verdicts from test
// names.
//
//   V1   the delimiters go back to raw bytes (THE DEFECT)  → 1 red  (the census)
//   V1P  V1 with THIS FILE DELETED                         → 0 RED / 512 tests ← what shipped
//   V2   a NUL planted in a jetbrains Kotlin file          → 1 red  (the walk reaches that half)
//   V3   the shared detector blinded                       → 1 red  (the PAIR only)
//   V4   the walk returning nothing                        → 1 red  (the literal floor)
//   V5   a reworded comment                                → 0 red
//
// ⚠⚠ V3 IS THE ARM THAT FOUND A HOLE IN THIS FILE AND IT IS WHY hasNul EXISTS. Its first cut
// deleted one assertion from the self-test and scored 0 RED — correctly, because deleting an
// assertion always goes green. Re-aimed at the detector itself, it exposed the real defect: the
// self-test called bytes.Contains while the census called bytes.IndexByte, so the "vacuity pair"
// was exercising a function the census does not use and could not have caught the census going
// blind. One `hasNul` now, and blinding it reds the pair while the census stays green — which is
// the whole point of having a pair: a blind detector reports a clean tree.
//
// ⚠ V1P IS THE FINDING: with this file absent, 512 tests pass while a tracked source file is
// invisible to plain grep.

// nulSkipDirs are directories whose contents are derived, vendored, or not source.
var nulSkipDirs = map[string]bool{
	"node_modules": true, ".git": true, "out": true, "rel": true, "dist": true,
	"build": true, "bin": true, "vendor": true, ".gradle": true, ".idea": true,
	"coverage": true,
}

// nulTextExts are the extensions a person or a script greps. Binaries are excluded by absence.
var nulTextExts = map[string]bool{
	".ts": true, ".tsx": true, ".js": true, ".mjs": true, ".cjs": true, ".go": true,
	".md": true, ".json": true, ".sh": true, ".yml": true, ".yaml": true, ".css": true,
	".html": true, ".sql": true, ".txt": true, ".kt": true, ".kts": true, ".xml": true,
	".gradle": true, ".py": true,
}

// ⚠ A LITERAL, never the length of what the walk returns. A floor derived from its own subject
// passes at zero, and a walk that silently stops finding files is exactly how this test would go
// green having read nothing. 275 measured at `3f9bffed`; it is a FLOOR, so adding files is fine.
const nulExpectedFiles = 240

// ⚠ THE ROOT COMES FROM THIS PACKAGE'S EXISTING `repoRoot` (wrapper_docs_guard_test.go), which
// anchors on .github/workflows/ci.yaml rather than on a relative hop count. Declaring a second
// one would have been two definitions of "the repository" that can disagree — and the first cut
// of this file did exactly that and failed to build, which is the cheapest possible way to find
// out.

// hasNul is THE detector, and it is one function on purpose.
//
// ⚠ THE FIRST CUT OF THIS FILE HAD TWO. The self-test below called bytes.Contains and the census
// called bytes.IndexByte — two different calls, so the "vacuity pair" was exercising a function
// the census does not use and could not have caught the census going blind. Control V3 is what
// found it: blinding one left the other green and nothing reddened. A pair that guards a
// DIFFERENT function from the one doing the work is the shape this project keeps finding in
// other people's guards, committed here by its own author and caught only by running the control.
func hasNul(b []byte) bool {
	return bytes.IndexByte(b, 0) >= 0
}

func textFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if nulSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if nulTextExts[strings.ToLower(filepath.Ext(d.Name()))] {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return out
}

// TestNulDetectorWorksInBothDirections is the VACUITY PAIR. "No file contains a NUL" is satisfied
// perfectly by a detector that never says yes, so the detector is exercised on this run, before
// the walk below is trusted.
func TestNulDetectorWorksInBothDirections(t *testing.T) {
	if hasNul([]byte("const delimiter = 0")) {
		t.Fatal("the detector reported a NUL in text that has none")
	}
	if !hasNul([]byte{'c', 'o', 'n', 0, 's', 't'}) {
		t.Fatal("the detector missed a NUL that is present — every assertion below is vacuous")
	}
	// The exact shape that shipped: a NUL inside a TypeScript template literal.
	shipped := append([]byte("return `"), 0)
	shipped = append(shipped, []byte("${i}")...)
	shipped = append(shipped, 0)
	if !hasNul(shipped) {
		t.Fatal("the detector missed the shape this repository actually shipped")
	}
	// And the repair — the escape form — must NOT trip it.
	if hasNul([]byte("return `\\u0000${i}\\u0000`")) {
		t.Fatal("the detector reported a NUL in the escape form, which contains none")
	}
}

// TestGreppableWalkReadsARepository is the other half of the pair: the census below is trivially
// true over an empty walk.
func TestGreppableWalkReadsARepository(t *testing.T) {
	root := repoRoot(t)
	files := textFiles(t, root)
	if len(files) < nulExpectedFiles {
		t.Fatalf("the source walk found %d text files, below the pinned floor of %d. Every "+
			"assertion in TestNoSourceFileContainsANulByte is vacuously true on an empty walk",
			len(files), nulExpectedFiles)
	}
	// It must reach every half of the product, not just the one this test happens to live in.
	for _, half := range []string{"extension/src", "agent", "jetbrains", "scripts", ".github"} {
		prefix := filepath.Join(root, half)
		found := false
		for _, f := range files {
			if strings.HasPrefix(f, prefix) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("the walk never reached %s, so nothing there is being checked", half)
		}
	}
}

func TestNoSourceFileContainsANulByte(t *testing.T) {
	root := repoRoot(t)
	var offenders []string
	for _, f := range textFiles(t, root) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		if hasNul(b) {
			i := bytes.IndexByte(b, 0)
			rel, _ := filepath.Rel(root, f)
			offenders = append(offenders, rel+" (first NUL at byte "+itoa(i)+")")
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("a NUL byte makes a whole file opaque to grep with NO error and NO non-zero exit: "+
			"`grep -c` prints nothing, `grep -rn` skips the file, and a tree-wide census is quietly "+
			"one file short. `git grep` is NOT fooled, which is what let the last one survive — the "+
			"tool this project's notes use is the one that reads it. If the byte is deliberate, "+
			"write it as the escape \\u0000: the character is identical and only the bytes on disk "+
			"change.\n%s", strings.Join(offenders, "\n"))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
