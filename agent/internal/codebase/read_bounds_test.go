package codebase

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE THREE BOUNDS ON HOW MUCH OF A CUSTOMER'S SOURCE LEAVES THE MACHINE.
//
// DefaultMaxFiles, DefaultMaxFileBytes and DefaultMaxTotalBytes decide how much
// of a repository is read and then handed to a PAID model. They are a cost
// ceiling and an exfiltration surface at the same time.
//
// MEASURED 2026-08-28 (tab-k2w8, W4.42/W4.43) by mutation, not by reading
// (~/talyvor-queue/w442-code-defaults-census-k2w8.py): each was multiplied by 7
// and the whole suite stayed green.
//
// ⚠ CHECKED AND CLEAN, SO NOBODY RE-MEASURES IT — AND IT IS WHY THIS FILE IS NOT
// THREE MORE ENFORCEMENT TESTS. The bounds ARE enforced and the enforcement IS
// tested: reader_test.go drives ReadFile's truncation and ReadFilesForContext's
// running total, and indexer_test.go drives the file cap. Every one of those
// passes an explicit small value — 100, 500, 10 — which is correct for testing
// the MECHANISM and is exactly why none of them can notice the SHIPPED value
// changing. Enforcement and value are different questions; only the second was
// open.
//
// ⚠ THESE ARE UPPER BOUNDS, NOT EQUALITIES, AND THE ASYMMETRY IS THE POINT.
// A smaller cap reads less, sends less and costs less — it is safe in the
// direction this file cares about, and forcing a test edit to TIGHTEN a safety
// ceiling would be a guard that discourages the safe change. What must never
// happen quietly is WIDENING: that is the direction that increases the bill and
// enlarges what leaves the customer's machine. So tightening is free and
// widening is a declared edit.
//
// (W4.37 made the mirror-image call for a k-anonymity floor — a LOWER bound,
// because there "more" was the safe direction. W4.40 used a plain equality
// because a mint cap had no safe direction at all. The rule is to pick per
// constant and say why, because the three fail differently.)

func TestReadBounds_AreNotWidenedSilently(t *testing.T) {
	for _, c := range []struct {
		name    string
		got     int64
		shipped int64
		why     string
	}{
		{"DefaultMaxFiles", int64(DefaultMaxFiles), 500,
			"files indexed in one pass"},
		{"DefaultMaxFileBytes", DefaultMaxFileBytes, 100 * 1024,
			"bytes of any single file handed to the model"},
		{"DefaultMaxTotalBytes", DefaultMaxTotalBytes, 500 * 1024,
			"bytes of concatenated context in one prompt"},
	} {
		if c.got > c.shipped {
			t.Errorf("%s = %d, above the recorded ceiling of %d (%s).\n"+
				"    This bound decides how much of a customer's repository is read and sent to a "+
				"paid model. Tightening it needs no edit here; WIDENING it is a deliberate act that "+
				"costs money and enlarges what leaves the machine, so say why in the same commit.",
				c.name, c.got, c.shipped, c.why)
		}
	}
}

// TestReadBounds_TotalFitsAtLeastOneWholeFile is the coherence invariant, and it
// is derived rather than chosen: ReadFilesForContext reads each file at
// DefaultMaxFileBytes and stops once the running total tops maxTotalBytes. If
// the total budget were below the per-file cap, the very first file could
// exhaust it — the function would emit a header and immediately truncate, and
// every prompt built through it would be a stub.
func TestReadBounds_TotalFitsAtLeastOneWholeFile(t *testing.T) {
	if DefaultMaxTotalBytes < DefaultMaxFileBytes {
		t.Fatalf("DefaultMaxTotalBytes (%d) is below DefaultMaxFileBytes (%d): a single file at the "+
			"per-file cap cannot fit in the whole-context budget, so ReadFilesForContext would "+
			"truncate on its first file every time", DefaultMaxTotalBytes, DefaultMaxFileBytes)
	}
}

// TestReadBounds_NonPositiveCapDoesNotMeanUnbounded pins the fallback both
// functions carry (`if max <= 0 { max = Default }`). A caller that computes a
// cap and gets 0 must land on the default, not on "no limit" — and not on
// "read nothing" either. Nothing exercised this path.
func TestReadBounds_NonPositiveCapDoesNotMeanUnbounded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	// Comfortably over the per-file cap so an unbounded read would be visible.
	big := strings.Repeat("x", int(DefaultMaxFileBytes)+4096)
	if err := os.WriteFile(path, []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ReadFile(path, 0)
	if err != nil {
		t.Fatalf("ReadFile with a zero cap: %v", err)
	}
	if int64(len(got)) > DefaultMaxFileBytes+64 { // +64 for the truncation marker
		t.Errorf("ReadFile(path, 0) returned %d bytes for a %d-byte file — a non-positive cap "+
			"must fall back to DefaultMaxFileBytes (%d), never to unbounded",
			len(got), len(big), DefaultMaxFileBytes)
	}
	if !strings.Contains(got, "(truncated)") {
		t.Error("ReadFile(path, 0) did not mark the result truncated, so a downstream prompt " +
			"cannot tell the model is seeing a partial file")
	}
	// AND IT MUST NOT COLLAPSE TO THE MARKER EITHER. ⚠ THIS ASSERTION IS A LOWER
	// BOUND ON CONTENT, NOT A NON-EMPTY CHECK, AND A CONTROL IS WHY: the first
	// draft asserted `len(got) == 0`, and removing the fallback outright made
	// ReadFile return `buf[:0]` PLUS the truncation marker — about eighteen
	// bytes, non-empty and correctly marked, so all three assertions passed
	// while the file had been lost entirely. A zero cap that silently yields no
	// content is the failure this case exists for, and "not empty" could not
	// see it.
	if int64(len(got)) < DefaultMaxFileBytes {
		t.Errorf("ReadFile(path, 0) returned only %d bytes of a %d-byte file. The fallback must "+
			"resolve to DefaultMaxFileBytes (%d), so a caller that computes a zero cap still gets "+
			"a full cap's worth of content — not an empty body wearing a truncation marker.",
			len(got), len(big), DefaultMaxFileBytes)
	}
}
