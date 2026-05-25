package diff

import (
	"strings"
	"testing"
)

func TestGenerateUnifiedDiff_ProducesValidOutput(t *testing.T) {
	original := "line one\nline two\nline three\n"
	modified := "line one\nline TWO\nline three\n"
	d := GenerateUnifiedDiff(original, modified, "test.txt", 3)
	if !strings.HasPrefix(d, "--- test.txt\n+++ test.txt\n") {
		t.Fatalf("header missing: %q", d)
	}
	if !strings.Contains(d, "@@") {
		t.Fatalf("hunk header missing: %q", d)
	}
	if !strings.Contains(d, "-line two") {
		t.Fatalf("delete line missing: %q", d)
	}
	if !strings.Contains(d, "+line TWO") {
		t.Fatalf("insert line missing: %q", d)
	}
	if !strings.Contains(d, " line one") {
		t.Fatalf("context line missing: %q", d)
	}
}

func TestGenerateUnifiedDiff_IdenticalReturnsEmpty(t *testing.T) {
	d := GenerateUnifiedDiff("same\nsame\n", "same\nsame\n", "f", 3)
	if d != "" {
		t.Fatalf("expected empty for identical, got %q", d)
	}
}

func TestGenerateUnifiedDiff_EmptyOriginalShowsInsertion(t *testing.T) {
	d := GenerateUnifiedDiff("", "new line\n", "f", 3)
	if !strings.Contains(d, "+new line") {
		t.Fatalf("expected insertion: %q", d)
	}
}

func TestGenerateUnifiedDiff_EmptyModifiedShowsDeletion(t *testing.T) {
	d := GenerateUnifiedDiff("old line\n", "", "f", 3)
	if !strings.Contains(d, "-old line") {
		t.Fatalf("expected deletion: %q", d)
	}
}

func TestApply_RoundTripsTrivialChange(t *testing.T) {
	original := "one\ntwo\nthree\nfour\nfive\n"
	modified := "one\ntwo\nTHREE\nfour\nfive\n"
	d := GenerateUnifiedDiff(original, modified, "f", 3)
	got, err := Apply(original, d)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got != modified {
		t.Fatalf("round-trip mismatch:\n got: %q\nwant: %q", got, modified)
	}
}

func TestApply_RoundTripsMultiHunk(t *testing.T) {
	original := strings.Repeat("a\n", 5) + "x\n" + strings.Repeat("b\n", 10) + "y\n" + strings.Repeat("c\n", 5)
	modified := strings.Repeat("a\n", 5) + "X\n" + strings.Repeat("b\n", 10) + "Y\n" + strings.Repeat("c\n", 5)
	d := GenerateUnifiedDiff(original, modified, "f", 3)
	got, err := Apply(original, d)
	if err != nil {
		t.Fatalf("Apply multi: %v", err)
	}
	if got != modified {
		t.Fatalf("multi-hunk round-trip mismatch:\n got: %q\nwant: %q", got, modified)
	}
}

func TestApply_EmptyDiffPassThrough(t *testing.T) {
	got, err := Apply("hello\n", "")
	if err != nil {
		t.Fatalf("Apply empty: %v", err)
	}
	if got != "hello\n" {
		t.Fatalf("expected pass-through, got %q", got)
	}
}

func TestApply_ErrorsOnUnreachableHunk(t *testing.T) {
	bogus := `--- f
+++ f
@@ -1,1 +1,1 @@
-nonexistent
+replacement
`
	_, err := Apply("totally different content\n", bogus)
	if err == nil {
		t.Fatal("expected error for hunk that doesn't apply")
	}
}
