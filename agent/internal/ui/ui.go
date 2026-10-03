// Package ui is how the CLI speaks the Talyvor design system in a terminal.
//
// ⚠ WHAT PORTS IS THE GRAMMAR, NOT THE HEX. The site paints #0F7A6C / #3AD6C0 on a background it
// owns, in a font it ships. A terminal owns neither, and 16-colour terminals still exist, so nothing
// in this file names an RGB value — an approximated hex renders wrong on somebody's light terminal.
// What carries over is:
//
//   - THE EYEBROW: the small uppercase label that names a block before the block says anything. A
//     terminal has no "small", so here it is UPPERCASE and FAINT, in a fixed-width column so every
//     row's content starts at the same place.
//   - ONE ACCENT, FOR ONE THING: the issue this work is attributed to, because "this is billed to
//     ENG-42" is the product's claim and nothing else on screen competes with it. The accent is ANSI
//     cyan — a palette SLOT, not a colour, so the user's own terminal theme decides which cyan is
//     legible on its own background. Cyan is the slot nearest the site's teal and the one of the
//     eight that survives both light and dark defaults (yellow vanishes on white, blue on black).
//   - FIGURES BY WEIGHT, NOT HUE: a measured number is bold and uncoloured. An estimate is never
//     styled as a figure — it keeps its "~" and its word.
//   - STATUS ON THE GLYPH ONLY: ✓ is green, ✗ is red, ! is bold (not yellow, for the reason above);
//     the sentence beside the mark stays in the reader's own foreground colour.
//
// ⚠ COLOUR IS OFF unless the writer is a terminal, NO_COLOR is unset or empty (https://no-color.org)
// and TERM is not "dumb". Piped output and log files get the same words and the same layout with no
// escape codes — colour codes in a log file are noise, and a CLI that ignores NO_COLOR gets aliased
// away.
package ui

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
)

// LabelWidth is the eyebrow column. Every Row's content starts after it, so a block of rows reads
// as one aligned table. Wide enough for the longest label in use ("ANTHROPIC") plus two spaces.
const LabelWidth = 11

// Style renders text for one writer. The zero value is plain.
type Style struct{ color bool }

// For returns the style for w: coloured only on an interactive terminal that has not opted out.
func For(w io.Writer) Style { return Style{color: colorEnabled(w)} }

// Plain never emits escape codes.
func Plain() Style { return Style{} }

// Colored always emits escape codes. For tests and for previewing the palette.
func Colored() Style { return Style{color: true} }

// Color reports whether this style emits escape codes.
func (s Style) Color() bool { return s.color }

func colorEnabled(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	// A legacy Windows console prints SGR sequences as literal garbage unless VT processing was
	// switched on. Windows Terminal (WT_SESSION) and the MSYS/Git-Bash terminals (TERM) handle them.
	if runtime.GOOS == "windows" && os.Getenv("WT_SESSION") == "" && os.Getenv("TERM") == "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func (s Style) sgr(code, text string) string {
	if !s.color || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

// Eyebrow is a label that names a block: uppercase, faint.
func (s Style) Eyebrow(label string) string { return s.sgr("2", strings.ToUpper(label)) }

// Issue is the single accent, reserved for the issue identifier the work is attributed to.
func (s Style) Issue(id string) string { return s.sgr("1;36", id) }

// Figure is a measured value or a name the reader is checking: bold, no colour.
func (s Style) Figure(text string) string { return s.sgr("1", text) }

// Muted is secondary text: sources, separators, hints.
func (s Style) Muted(text string) string { return s.sgr("2", text) }

// OK, Warn and Fail put a status mark before a sentence; only the mark is coloured.
func (s Style) OK(msg string) string   { return s.sgr("32", "✓") + " " + msg }
func (s Style) Warn(msg string) string { return s.sgr("1", "!") + " " + msg }
func (s Style) Fail(msg string) string { return s.sgr("31", "✗") + " " + msg }

// ErrorPrefix is the "error:" a failed command leads with.
func (s Style) ErrorPrefix() string { return s.sgr("1;31", "error:") }

// Row is one line of a labelled block: two-space indent, the eyebrow in its column, then content.
// An empty label continues the row above it, aligned under its content.
func (s Style) Row(label, content string) string {
	pad := fmt.Sprintf("%-*s", LabelWidth, strings.ToUpper(label))
	if label == "" {
		return "  " + pad + content
	}
	return "  " + s.sgr("2", pad) + content
}

// Sep is the separator between ideas on one line.
func (s Style) Sep() string { return s.Muted(" · ") }
