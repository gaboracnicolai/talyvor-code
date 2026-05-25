package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleRules = `[general]
Write clean, idiomatic code.
Prefer explicit error handling.

[go]
Use table-driven tests.
Return errors as the last return value.

[typescript]
No 'any' types without comment.

[testing]
Write tests first.

[agent]
Make atomic changes.

[review]
Flag hardcoded secrets.
`

func writeRules(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, RulesFileName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// ─── Load ──────────────────────────────────────────

func TestLoad_FindsFileInCurrentDir(t *testing.T) {
	dir := t.TempDir()
	writeRules(t, dir, sampleRules)
	r, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r == nil {
		t.Fatal("expected Rules, got nil")
	}
	if !strings.Contains(r.Sections.General, "idiomatic") {
		t.Fatalf("general section missing: %q", r.Sections.General)
	}
}

func TestLoad_WalksUpToParent(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeRules(t, root, sampleRules)
	r, err := Load(child)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r == nil {
		t.Fatal("expected Rules walked up from nested dir")
	}
	if filepath.Dir(r.FilePath) != root {
		t.Fatalf("FilePath dir = %q, want %q", filepath.Dir(r.FilePath), root)
	}
}

func TestLoad_ReturnsNilWhenMissing(t *testing.T) {
	dir := t.TempDir()
	r, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r != nil {
		t.Fatalf("expected nil, got %+v", r)
	}
}

func TestLoad_RejectsOversizeFile(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", MaxRulesFileSize+1)
	writeRules(t, dir, "[general]\n"+big)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected size error for >32KB")
	}
	if !strings.Contains(err.Error(), "32") && !strings.Contains(err.Error(), "size") {
		t.Fatalf("error should mention size cap: %v", err)
	}
}

// ─── Parse ─────────────────────────────────────────

func TestParse_ExtractsGeneralSection(t *testing.T) {
	s := Parse(sampleRules)
	if !strings.Contains(s.General, "idiomatic") {
		t.Fatalf("general: %q", s.General)
	}
	if !strings.Contains(s.General, "error handling") {
		t.Fatalf("general missing second line: %q", s.General)
	}
}

func TestParse_ExtractsLanguageSections(t *testing.T) {
	s := Parse(sampleRules)
	if got := s.Languages["go"]; !strings.Contains(got, "table-driven") {
		t.Fatalf("go: %q", got)
	}
	if got := s.Languages["typescript"]; !strings.Contains(got, "any") {
		t.Fatalf("typescript: %q", got)
	}
}

func TestParse_CaseInsensitiveSectionNames(t *testing.T) {
	s := Parse("[GO]\nrule\n\n[TypeScript]\nrule2\n")
	if got := s.Languages["go"]; !strings.Contains(got, "rule") {
		t.Fatalf("[GO] should normalise to 'go': %q", s.Languages)
	}
	if _, ok := s.Languages["typescript"]; !ok {
		t.Fatalf("[TypeScript] should normalise to 'typescript': %+v", s.Languages)
	}
}

func TestParse_DiscardsEmptySections(t *testing.T) {
	s := Parse("[general]\n\n[go]\n   \n[review]\nbody\n")
	if s.General != "" {
		t.Fatalf("expected empty general, got %q", s.General)
	}
	if _, ok := s.Languages["go"]; ok {
		t.Fatalf("empty [go] should be discarded")
	}
	if !strings.Contains(s.Review, "body") {
		t.Fatalf("review: %q", s.Review)
	}
}

func TestParse_IgnoresCommentsAndBlankLines(t *testing.T) {
	body := "# comment line\n[general]\n# also a comment\n  \nrule body\n"
	s := Parse(body)
	if !strings.Contains(s.General, "rule body") {
		t.Fatalf("general: %q", s.General)
	}
	if strings.Contains(s.General, "#") {
		t.Fatalf("comments should be stripped: %q", s.General)
	}
}

// ─── Section combinators ──────────────────────────

func TestForLanguage_CombinesGeneralAndLang(t *testing.T) {
	r := &Rules{Sections: Parse(sampleRules)}
	out := ForLanguage(r, "go")
	if !strings.Contains(out, "idiomatic") {
		t.Fatalf("general missing: %q", out)
	}
	if !strings.Contains(out, "table-driven") {
		t.Fatalf("go missing: %q", out)
	}
}

func TestForLanguage_UnknownLangFallsBackToGeneral(t *testing.T) {
	r := &Rules{Sections: Parse(sampleRules)}
	out := ForLanguage(r, "haskell")
	if !strings.Contains(out, "idiomatic") {
		t.Fatalf("general missing: %q", out)
	}
}

func TestForLanguage_NilRulesReturnsEmpty(t *testing.T) {
	if got := ForLanguage(nil, "go"); got != "" {
		t.Fatalf("nil rules should return empty, got %q", got)
	}
}

func TestForAgent_CombinesGeneralAndAgent(t *testing.T) {
	r := &Rules{Sections: Parse(sampleRules)}
	out := ForAgent(r)
	if !strings.Contains(out, "idiomatic") || !strings.Contains(out, "atomic changes") {
		t.Fatalf("agent: %q", out)
	}
}

func TestForReview_CombinesGeneralAndReview(t *testing.T) {
	r := &Rules{Sections: Parse(sampleRules)}
	out := ForReview(r)
	if !strings.Contains(out, "Flag hardcoded secrets") {
		t.Fatalf("review: %q", out)
	}
}

func TestForTesting_CombinesGeneralTestingAndLang(t *testing.T) {
	r := &Rules{Sections: Parse(sampleRules)}
	out := ForTesting(r, "go")
	for _, want := range []string{"idiomatic", "tests first", "table-driven"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in: %s", want, out)
		}
	}
}

func TestForAgent_NilReturnsEmpty(t *testing.T) {
	if got := ForAgent(nil); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

// ─── Prompt prefix helper ─────────────────────────

func TestPromptPrefix_FormatsRulesBlock(t *testing.T) {
	body := "Rule one.\nRule two."
	prefix := PromptPrefix(body)
	if !strings.HasPrefix(prefix, "Project rules") {
		t.Fatalf("prefix should lead with header: %q", prefix)
	}
	if !strings.Contains(prefix, "Rule one.") {
		t.Fatalf("body missing: %q", prefix)
	}
	if !strings.Contains(prefix, "---") {
		t.Fatalf("expected delimiter: %q", prefix)
	}
}

func TestPromptPrefix_EmptyReturnsEmpty(t *testing.T) {
	if got := PromptPrefix(""); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := PromptPrefix("   \n  "); got != "" {
		t.Fatalf("whitespace-only should return empty, got %q", got)
	}
}
