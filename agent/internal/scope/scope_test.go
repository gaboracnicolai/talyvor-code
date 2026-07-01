package scope

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/talyvor/code/internal/codebase"
)

const sampleScopes = `{
  "auth": {
    "name": "Authentication",
    "includes": ["internal/auth/**", "cmd/auth/**"],
    "excludes": ["**/*_test.go"],
    "focus": "JWT authentication and session management"
  },
  "api": {
    "name": "API Layer",
    "includes": ["internal/api/**", "internal/handlers/**"],
    "focus": "REST API endpoints and middleware"
  }
}`

// ─── glob matcher ─────────────────────────────────

func TestMatchGlob_LiteralPath(t *testing.T) {
	if !MatchGlob("src/auth.go", "src/auth.go") {
		t.Fatal("literal match should succeed")
	}
}

func TestMatchGlob_StarMatchesOneSegment(t *testing.T) {
	cases := map[string]bool{
		"src/*.go|src/auth.go":   true,
		"src/*.go|src/auth/x.go": false, // * doesn't cross /
		"src/*.go|src/.dotfile":  false, // wildcard skips leading dot
	}
	for in, want := range cases {
		parts := strings.SplitN(in, "|", 2)
		pat, path := parts[0], parts[1]
		got := MatchGlob(pat, path)
		if got != want {
			t.Errorf("MatchGlob(%q, %q) = %v, want %v", pat, path, got, want)
		}
	}
}

func TestMatchGlob_DoubleStarCrossesSegments(t *testing.T) {
	cases := map[string]bool{
		"internal/auth/**|internal/auth/jwt.go":         true,
		"internal/auth/**|internal/auth/jwt/handler.go": true,
		"internal/auth/**|internal/api/server.go":       false,
		"**/*_test.go|src/auth_test.go":                 true,
		"**/*_test.go|src/sub/auth_test.go":             true,
		"**/*_test.go|src/auth.go":                      false,
		"**/handlers/**|src/handlers/foo.go":            true,
		"**/handlers/**|src/api/handlers/v1/foo.go":     true,
		"**/handlers/**|src/api/foo.go":                 false,
	}
	for in, want := range cases {
		parts := strings.SplitN(in, "|", 2)
		pat, path := parts[0], parts[1]
		got := MatchGlob(pat, path)
		if got != want {
			t.Errorf("MatchGlob(%q, %q) = %v, want %v", pat, path, got, want)
		}
	}
}

func TestMatchGlob_HandlesPlatformSeparators(t *testing.T) {
	// Patterns use forward slashes; paths may come in with the
	// host separator. The matcher normalises both sides.
	if !MatchGlob("internal/auth/**", "internal\\auth\\jwt.go") {
		t.Fatal("backslash-separated path should match forward-slash pattern")
	}
}

// ─── Load / persistence ───────────────────────────

func writeScopes(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ScopesFileName), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestLoad_ReadsScopesFromFile(t *testing.T) {
	dir := t.TempDir()
	writeScopes(t, dir, sampleScopes)
	sm := NewManager(dir)
	if err := sm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	all := sm.List()
	if len(all) != 2 {
		t.Fatalf("expected 2 scopes, got %d", len(all))
	}
	got, ok := sm.Get("auth")
	if !ok {
		t.Fatal("auth scope missing")
	}
	if got.Name != "Authentication" || len(got.Includes) != 2 {
		t.Fatalf("auth scope wrong: %+v", got)
	}
}

func TestLoad_MissingFileIsNotAnError(t *testing.T) {
	sm := NewManager(t.TempDir())
	if err := sm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(sm.List()) != 0 {
		t.Fatalf("expected empty scopes, got %d", len(sm.List()))
	}
}

func TestLoad_RejectsTooManyScopes(t *testing.T) {
	dir := t.TempDir()
	var body strings.Builder
	body.WriteString("{")
	for i := 0; i <= MaxScopes; i++ {
		if i > 0 {
			body.WriteString(",")
		}
		body.WriteString(`"s`)
		body.WriteString(strings.Repeat("a", 0))
		body.WriteString(itoa(i))
		body.WriteString(`":{"name":"x","includes":["**"]}`)
	}
	body.WriteString("}")
	writeScopes(t, dir, body.String())
	sm := NewManager(dir)
	err := sm.Load()
	if err == nil {
		t.Fatal("expected too-many-scopes error")
	}
}

func TestSetActive_PersistsAndLoadActiveReadsBack(t *testing.T) {
	dir := t.TempDir()
	writeScopes(t, dir, sampleScopes)
	sm := NewManager(dir)
	if err := sm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := sm.SetActive("auth"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if got := sm.GetActive(); got == nil || got.Name != "Authentication" {
		t.Fatalf("GetActive after SetActive: %+v", got)
	}

	// New manager sees the persisted file.
	sm2 := NewManager(dir)
	if err := sm2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := sm2.LoadActive(); err != nil {
		t.Fatalf("LoadActive: %v", err)
	}
	if got := sm2.GetActive(); got == nil || got.Name != "Authentication" {
		t.Fatalf("LoadActive should reactivate auth, got %+v", got)
	}
}

func TestSetActive_UnknownScopeErrors(t *testing.T) {
	dir := t.TempDir()
	writeScopes(t, dir, sampleScopes)
	sm := NewManager(dir)
	_ = sm.Load()
	if err := sm.SetActive("nope"); err == nil {
		t.Fatal("expected error for unknown scope")
	}
}

func TestClearActive_RemovesFile(t *testing.T) {
	dir := t.TempDir()
	writeScopes(t, dir, sampleScopes)
	sm := NewManager(dir)
	_ = sm.Load()
	if err := sm.SetActive("api"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if err := sm.ClearActive(); err != nil {
		t.Fatalf("ClearActive: %v", err)
	}
	if sm.GetActive() != nil {
		t.Fatal("GetActive should be nil after Clear")
	}
	if _, err := os.Stat(filepath.Join(dir, ActiveScopeFileName)); err == nil {
		t.Fatal("active scope file should be removed")
	}
}

func TestGetActive_NilWhenNoneSet(t *testing.T) {
	dir := t.TempDir()
	writeScopes(t, dir, sampleScopes)
	sm := NewManager(dir)
	_ = sm.Load()
	if sm.GetActive() != nil {
		t.Fatal("expected nil active scope")
	}
}

// ─── FilterFiles ─────────────────────────────────

func TestFilterFiles_KeepsMatchesDropsRest(t *testing.T) {
	dir := t.TempDir()
	writeScopes(t, dir, sampleScopes)
	sm := NewManager(dir)
	_ = sm.Load()
	_ = sm.SetActive("auth")

	files := []codebase.FileInfo{
		{Path: "internal/auth/jwt.go"},
		{Path: "internal/auth/session.go"},
		{Path: "internal/auth/jwt_test.go"}, // excluded
		{Path: "cmd/auth/main.go"},
		{Path: "internal/api/router.go"}, // not included
	}
	filtered := sm.FilterFiles(files)
	got := []string{}
	for _, f := range filtered {
		got = append(got, f.Path)
	}
	want := []string{
		"internal/auth/jwt.go",
		"internal/auth/session.go",
		"cmd/auth/main.go",
	}
	if !sameStrings(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestFilterFiles_NoActiveScopePassesThrough(t *testing.T) {
	sm := NewManager(t.TempDir())
	_ = sm.Load()
	files := []codebase.FileInfo{{Path: "a.go"}, {Path: "b.go"}}
	out := sm.FilterFiles(files)
	if len(out) != 2 {
		t.Fatalf("no active scope should pass through, got %d", len(out))
	}
}

// ─── ToPromptSection ──────────────────────────────

func TestToPromptSection_EmptyWhenNoScope(t *testing.T) {
	sm := NewManager(t.TempDir())
	if got := sm.ToPromptSection(); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestToPromptSection_IncludesNameFocusAndIncludes(t *testing.T) {
	dir := t.TempDir()
	writeScopes(t, dir, sampleScopes)
	sm := NewManager(dir)
	_ = sm.Load()
	_ = sm.SetActive("auth")
	out := sm.ToPromptSection()
	for _, want := range []string{
		"Active scope: Authentication",
		"Focus: JWT authentication",
		"Included files:",
		"internal/auth/**",
		"cmd/auth/**",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt missing %q:\n%s", want, out)
		}
	}
}

// ─── helpers ─────────────────────────────────────

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		if !seen[s] {
			return false
		}
	}
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string('0'+rune(n%10)) + digits
		n /= 10
	}
	return digits
}
