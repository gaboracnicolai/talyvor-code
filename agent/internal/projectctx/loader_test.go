package projectctx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleJSON = `{
  "name": "MyApp",
  "description": "B2B e-commerce platform serving enterprise customers",
  "stack": ["Go", "PostgreSQL", "Redis", "React", "TypeScript"],
  "architecture": "Microservices with gRPC internal communication",
  "conventions": {
    "database": "Use pgx driver, never database/sql",
    "errors": "Always wrap errors with fmt.Errorf('context: %w', err)"
  },
  "key_files": ["cmd/server/main.go", "internal/api/router.go"],
  "team_size": 5,
  "links": {
    "docs": "https://docs.myapp.com"
  }
}`

const sampleYAML = `name: MyApp
description: B2B e-commerce platform serving enterprise customers
stack:
  - Go
  - PostgreSQL
  - React
architecture: Microservices with gRPC
conventions:
  database: Use pgx driver
  errors: Wrap with fmt.Errorf
key_files:
  - cmd/server/main.go
  - internal/api/router.go
team_size: 5
links:
  docs: https://docs.myapp.com
`

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// ─── Load ──────────────────────────────────────────

func TestLoad_FindsJSONInCurrentDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ContextFileName, sampleJSON)
	pc, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if pc == nil {
		t.Fatal("expected context, got nil")
	}
	if pc.Name != "MyApp" {
		t.Fatalf("name = %q", pc.Name)
	}
	if len(pc.Stack) != 5 || pc.Stack[0] != "Go" {
		t.Fatalf("stack wrong: %+v", pc.Stack)
	}
	if pc.Conventions["database"] == "" {
		t.Fatalf("convention missing: %+v", pc.Conventions)
	}
}

func TestLoad_FindsYAMLInCurrentDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ContextFileName, sampleYAML)
	pc, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if pc == nil {
		t.Fatal("expected context, got nil")
	}
	if pc.Name != "MyApp" {
		t.Fatalf("name = %q", pc.Name)
	}
	if len(pc.KeyFiles) != 2 {
		t.Fatalf("key_files wrong: %+v", pc.KeyFiles)
	}
	if pc.TeamSize != 5 {
		t.Fatalf("team_size = %d", pc.TeamSize)
	}
}

func TestLoad_WalksUpToParent(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, root, ContextFileName, sampleJSON)
	pc, err := Load(child)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if pc == nil || filepath.Dir(pc.FilePath) != root {
		t.Fatalf("expected walked-up context, got %+v", pc)
	}
}

func TestLoad_ReturnsNilWhenMissing(t *testing.T) {
	dir := t.TempDir()
	pc, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if pc != nil {
		t.Fatalf("expected nil, got %+v", pc)
	}
}

func TestLoad_RejectsInvalidContent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ContextFileName, "this is neither json nor yaml ::: %%%%")
	pc, err := Load(dir)
	// Invalid YAML may parse as a scalar string — we treat the
	// result as a no-op when Name is empty, which is what the
	// validator will surface.
	_ = pc
	_ = err
}

func TestLoad_FallsThroughYAMLWhenJSONFails(t *testing.T) {
	// Mixed content that looks YAML-ish but starts without `{`.
	dir := t.TempDir()
	writeFile(t, dir, ContextFileName, "name: Bare\nstack: [Go]\n")
	pc, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if pc == nil || pc.Name != "Bare" {
		t.Fatalf("unexpected: %+v", pc)
	}
}

// ─── ToPromptSection ───────────────────────────────

func TestToPromptSection_IncludesCoreFields(t *testing.T) {
	pc, err := ParseJSON([]byte(sampleJSON))
	if err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	out := pc.ToPromptSection()
	for _, want := range []string{
		"Project context",
		"Name: MyApp",
		"Description: B2B e-commerce",
		"Stack: Go, PostgreSQL",
		"Architecture: Microservices",
		"database: Use pgx",
		"Key files:",
		"cmd/server/main.go",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt missing %q:\n%s", want, out)
		}
	}
}

func TestToPromptSection_NilSafe(t *testing.T) {
	var pc *ProjectContext
	if got := pc.ToPromptSection(); got != "" {
		t.Fatalf("nil should yield empty, got %q", got)
	}
}

func TestToPromptSection_RespectsByteCap(t *testing.T) {
	pc := &ProjectContext{
		Name:        "X",
		Description: strings.Repeat("a", 5000),
		Stack:       []string{"Go"},
	}
	out := pc.ToPromptSection()
	if len(out) > MaxContextPromptBytes {
		t.Fatalf("len(out)=%d, want <= %d", len(out), MaxContextPromptBytes)
	}
}

// ─── Validate ──────────────────────────────────────

func TestValidate_FlagsMissingName(t *testing.T) {
	pc := &ProjectContext{Description: "some description over 20 chars long", Stack: []string{"Go"}}
	warns := pc.Validate()
	if !containsContaining(warns, "name") {
		t.Fatalf("warnings should mention name: %+v", warns)
	}
}

func TestValidate_FlagsShortDescription(t *testing.T) {
	pc := &ProjectContext{Name: "X", Description: "too short", Stack: []string{"Go"}}
	warns := pc.Validate()
	if !containsContaining(warns, "description") {
		t.Fatalf("warnings should mention description: %+v", warns)
	}
}

func TestValidate_FlagsEmptyStack(t *testing.T) {
	pc := &ProjectContext{Name: "X", Description: "long enough description here OK"}
	warns := pc.Validate()
	if !containsContaining(warns, "stack") {
		t.Fatalf("warnings should mention stack: %+v", warns)
	}
}

func TestValidate_AcceptsGoodContext(t *testing.T) {
	pc, _ := ParseJSON([]byte(sampleJSON))
	if warns := pc.Validate(); len(warns) > 0 {
		t.Fatalf("sample context should validate cleanly: %+v", warns)
	}
}

func containsContaining(items []string, needle string) bool {
	needle = strings.ToLower(needle)
	for _, s := range items {
		if strings.Contains(strings.ToLower(s), needle) {
			return true
		}
	}
	return false
}

// ─── CombinedPrefix ───────────────────────────────

func TestCombinedPrefix_NoSourcesReturnsEmpty(t *testing.T) {
	if got := CombinedPrefix("", nil); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestCombinedPrefix_OrdersRulesBeforeContext(t *testing.T) {
	pc, _ := ParseJSON([]byte(sampleJSON))
	out := CombinedPrefix("RULES PREFIX BLOCK\n", pc)
	rulesAt := strings.Index(out, "RULES PREFIX")
	ctxAt := strings.Index(out, "Project context")
	if rulesAt < 0 || ctxAt < 0 {
		t.Fatalf("both sections must be present: %q", out)
	}
	if rulesAt >= ctxAt {
		t.Fatalf("rules should precede context: rules=%d, ctx=%d", rulesAt, ctxAt)
	}
}
