package runner

import (
	"strings"
	"testing"
)

func TestHealingPrompt_IncludesAllContext(t *testing.T) {
	ctx := HealContext{
		TaskDescription: "Add JWT auth",
		FailedCommand:   "go build ./...",
		ErrorOutput:     "undefined: jwt.Verify",
		ChangedFiles:    []string{"auth.go", "middleware.go"},
		Language:        LangGo,
		Attempt:         1,
	}
	p := HealingPrompt(ctx)
	for _, want := range []string{
		"Add JWT auth",
		"go build ./...",
		"undefined: jwt.Verify",
		"auth.go",
		"middleware.go",
		"JSON",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q:\n%s", want, p)
		}
	}
}

func TestParseHealResult_ParsesValidJSON(t *testing.T) {
	raw := `[
		{"file": "src/auth.ts", "content": "export const x = 1;"},
		{"file": "src/middleware.ts", "content": "export {};"}
	]`
	fixes, err := ParseHealResult(raw)
	if err != nil {
		t.Fatalf("ParseHealResult: %v", err)
	}
	if len(fixes) != 2 {
		t.Fatalf("expected 2 fixes, got %d", len(fixes))
	}
	if fixes[0].File != "src/auth.ts" {
		t.Errorf("file[0] = %q", fixes[0].File)
	}
	if !strings.Contains(fixes[1].Content, "export {}") {
		t.Errorf("content[1] = %q", fixes[1].Content)
	}
}

func TestParseHealResult_StripsMarkdownFences(t *testing.T) {
	// Models sometimes ignore "no markdown" instructions.
	raw := "```json\n[{\"file\":\"a.go\",\"content\":\"package a\"}]\n```"
	fixes, err := ParseHealResult(raw)
	if err != nil {
		t.Fatalf("ParseHealResult: %v", err)
	}
	if len(fixes) != 1 || fixes[0].File != "a.go" {
		t.Fatalf("unexpected: %+v", fixes)
	}
}

func TestParseHealResult_HandlesEmptyArray(t *testing.T) {
	fixes, err := ParseHealResult("[]")
	if err != nil {
		t.Fatalf("ParseHealResult: %v", err)
	}
	if len(fixes) != 0 {
		t.Fatalf("expected empty, got %d", len(fixes))
	}
}

func TestParseHealResult_RejectsInvalidJSON(t *testing.T) {
	_, err := ParseHealResult("not json")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "JSON") && !strings.Contains(err.Error(), "json") {
		t.Errorf("error should mention JSON: %v", err)
	}
}

func TestParseHealResult_DropsEntriesMissingFields(t *testing.T) {
	// Defensive: model may produce partial objects. We keep
	// only entries with both file + content set.
	raw := `[
		{"file": "good.ts", "content": "export {}"},
		{"file": "", "content": "x"},
		{"file": "no-content.ts"}
	]`
	fixes, err := ParseHealResult(raw)
	if err != nil {
		t.Fatalf("ParseHealResult: %v", err)
	}
	if len(fixes) != 1 || fixes[0].File != "good.ts" {
		t.Fatalf("unexpected: %+v", fixes)
	}
}

func TestParseHealResult_PrefersInnerJSONFromProse(t *testing.T) {
	// Some models prepend prose despite the "ONLY JSON" rule.
	// We salvage the array if we can find it.
	raw := `Here are the fixes:

[{"file":"x.go","content":"package x"}]

That should work.`
	fixes, err := ParseHealResult(raw)
	if err != nil {
		t.Fatalf("ParseHealResult: %v", err)
	}
	if len(fixes) != 1 {
		t.Fatalf("expected 1, got %+v", fixes)
	}
}

func TestMaxHealAttempts_Is3(t *testing.T) {
	// Sanity: the spec pins the cap at 3 across both surfaces.
	if MaxHealAttempts != 3 {
		t.Fatalf("MaxHealAttempts = %d, want 3", MaxHealAttempts)
	}
}
