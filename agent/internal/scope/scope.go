// Package scope tells the AI which part of the codebase to attend to. The
// user defines named scopes (auth, api, frontend, …) in .talyvor-scopes; one
// can be "active" at a time and is surfaced in every prompt.
//
// ⚠ AN ACTIVE SCOPE IS A HINT IN THE PROMPT. IT DOES NOT RESTRICT WHICH FILES
// ARE READ. This doc comment used to describe a scope as being enforced against
// the indexed file population, and both front ends told the user that clearing
// one put every file back in context — a sentence that only means anything if
// some had been kept out. Measured end to end in filter_wiring_test.go through
// the exact call cmd/agent/main.go:791 makes: with a scope active whose one
// include pattern is `internal/auth/**`, the population handed to the planner
// still contains internal/billing. It always did. FilterFiles and
// GetScopedFiles below are correct and NOTHING calls them — a census of every
// non-test .go file in the module finds zero callers. (The retired wording is
// not quoted anywhere in this file: the census matches on substring, so
// repeating it would re-trigger the guard that watches for its return.)
//
// ⚠ WIRING THEM IS A PRODUCT DECISION, NOT A REPAIR, WHICH IS WHY IT WAS
// MEASURED AND NOT GUESSED. There are five production IndexDirectory sites
// (cmd/agent/main.go:791, internal/projectctx/loader.go:242,
// internal/mcp/server.go:164 and :1094, internal/codebase/semindex_build.go:116)
// and they are not one decision: narrowing the semantic-index BUILD writes a
// scope-shaped hole into a cache that outlives the scope and is shared with
// every other one, and narrowing the MCP server changes what an external client
// sees on the strength of a CLI state it never set. Until that is decided, the
// honest thing is that the prompt hint is the whole mechanism — and
// TestScopeFilterClaimMatchesItsWiring fails the day a caller appears without
// the sentences coming back with it.
package scope

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/talyvor/code/internal/codebase"
)

const (
	// ScopesFileName is the on-disk catalogue.
	ScopesFileName = ".talyvor-scopes"

	// ActiveScopeFileName persists the active scope across CLI
	// invocations. Plain text (the scope's key), not JSON, so
	// `cat .talyvor-active-scope` works at a glance.
	ActiveScopeFileName = ".talyvor-active-scope"

	// MaxScopes caps the catalogue size. Beyond ~20 the QuickPick
	// stops being useful — better to nest hierarchically with
	// project context than to flatten everything here.
	MaxScopes = 20
)

// scopeNameRe enforces the documented charset for scope keys —
// alphanumeric + hyphens only.
var scopeNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]*$`)

// Scope is one named view of the codebase.
type Scope struct {
	Name     string   `json:"name"`
	Includes []string `json:"includes"`
	Excludes []string `json:"excludes,omitempty"`
	Focus    string   `json:"focus,omitempty"`
}

// ScopeManager holds the loaded catalogue + the active scope.
// All paths it returns are relative to the root the manager was
// created with.
type ScopeManager struct {
	root      string
	scopes    map[string]Scope
	activeKey string
	active    *Scope
}

// NewManager returns a manager rooted at root. Load() must be
// called before the manager is useful — keeping construction
// cheap (and infallible) makes the wiring easier.
func NewManager(root string) *ScopeManager {
	if root == "" {
		root = "."
	}
	return &ScopeManager{
		root:   root,
		scopes: map[string]Scope{},
	}
}

// Load reads .talyvor-scopes (if present) and validates the
// catalogue. Missing file is not an error — scopes are optional.
func (sm *ScopeManager) Load() error {
	path := filepath.Join(sm.root, ScopesFileName)
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	parsed := map[string]Scope{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("scope: invalid JSON in %s: %w", ScopesFileName, err)
	}
	if len(parsed) > MaxScopes {
		return fmt.Errorf("scope: %d scopes defined; max is %d", len(parsed), MaxScopes)
	}
	for key := range parsed {
		if !scopeNameRe.MatchString(key) {
			return fmt.Errorf("scope: invalid name %q (alphanumeric + hyphens only)", key)
		}
	}
	sm.scopes = parsed
	return nil
}

// List returns the catalogue's keys in alphabetical order so the
// CLI's `scope list` and the IDE QuickPick render deterministically.
func (sm *ScopeManager) List() []string {
	keys := make([]string, 0, len(sm.scopes))
	for k := range sm.scopes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Get returns a scope by key. Returns (zero, false) for unknown
// keys so callers can branch cleanly.
func (sm *ScopeManager) Get(key string) (Scope, bool) {
	s, ok := sm.scopes[key]
	return s, ok
}

// SetActive marks the supplied scope as active and persists it
// to ActiveScopeFileName.
func (sm *ScopeManager) SetActive(key string) error {
	s, ok := sm.scopes[key]
	if !ok {
		return fmt.Errorf("scope: unknown scope %q (run `talyvor-code scope list`)", key)
	}
	sm.activeKey = key
	sm.active = &s
	return os.WriteFile(filepath.Join(sm.root, ActiveScopeFileName), []byte(key+"\n"), 0o644)
}

// LoadActive reads ActiveScopeFileName and re-applies it on top
// of the loaded catalogue. Missing file is fine; unknown scope
// name clears the active pointer rather than failing — the
// catalogue may have been edited since.
func (sm *ScopeManager) LoadActive() error {
	body, err := os.ReadFile(filepath.Join(sm.root, ActiveScopeFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	key := strings.TrimSpace(string(body))
	if key == "" {
		return nil
	}
	s, ok := sm.scopes[key]
	if !ok {
		sm.activeKey = ""
		sm.active = nil
		return nil
	}
	sm.activeKey = key
	sm.active = &s
	return nil
}

// ClearActive drops the active scope and removes its on-disk
// marker. Idempotent — missing file is fine.
func (sm *ScopeManager) ClearActive() error {
	sm.activeKey = ""
	sm.active = nil
	err := os.Remove(filepath.Join(sm.root, ActiveScopeFileName))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// GetActive returns the active scope or nil when none is set.
// The pointer is to a copy; mutating it does NOT mutate the
// catalogue.
func (sm *ScopeManager) GetActive() *Scope {
	if sm.active == nil {
		return nil
	}
	out := *sm.active
	return &out
}

// ActiveName returns the active scope's catalogue key (e.g. "auth")
// or "" when none is set. Useful for status-bar rendering.
func (sm *ScopeManager) ActiveName() string {
	return sm.activeKey
}

// FilterFiles applies the active scope's include/exclude rules
// to a slice of indexed files. No active scope → pass-through.
func (sm *ScopeManager) FilterFiles(files []codebase.FileInfo) []codebase.FileInfo {
	if sm.active == nil {
		return files
	}
	out := make([]codebase.FileInfo, 0, len(files))
	for _, f := range files {
		if MatchAny(sm.active.Excludes, f.Path) {
			continue
		}
		if len(sm.active.Includes) == 0 || MatchAny(sm.active.Includes, f.Path) {
			out = append(out, f)
		}
	}
	return out
}

// GetScopedFiles indexes the workspace and returns the slice
// already filtered by the active scope.
//
// ⚠ NO COMMAND CALLS THIS. It was described as a convenience wrapper for the
// commands that need a ready-to-consume file list, and there are none — see the
// package doc and the census in filter_wiring_test.go. Kept because it is the
// ready-made call site for whoever decides which of the five index populations
// a scope should narrow.
//
// ⚠ AND IT CAPS BEFORE IT FILTERS, WHICH WHOEVER WIRES IT HAS TO ANSWER FOR:
// IndexDirectory stops collecting at `limit` and the scope filter then runs on
// whatever the walk reached first, so on a repo larger than the cap the result
// is the matching subset of an arbitrary prefix, not the matching subset of the
// repo — and it comes back silently short rather than empty.
func (sm *ScopeManager) GetScopedFiles(root string, limit int) ([]codebase.FileInfo, error) {
	idx, err := codebase.IndexDirectory(root, limit)
	if err != nil {
		return nil, err
	}
	return sm.FilterFiles(idx.Files), nil
}

// ToPromptSection renders the "Active scope:" block for system
// prompts. Empty string when no scope is active so callers can
// concatenate unconditionally.
func (sm *ScopeManager) ToPromptSection() string {
	if sm.active == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Active scope: %s\n", scopeOrKey(sm.active.Name, sm.activeKey))
	if strings.TrimSpace(sm.active.Focus) != "" {
		fmt.Fprintf(&b, "  Focus: %s\n", strings.TrimSpace(sm.active.Focus))
	}
	if len(sm.active.Includes) > 0 {
		fmt.Fprintf(&b, "  Included files: %s\n", strings.Join(sm.active.Includes, ", "))
	}
	if len(sm.active.Excludes) > 0 {
		fmt.Fprintf(&b, "  Excluded files: %s\n", strings.Join(sm.active.Excludes, ", "))
	}
	return b.String()
}

// MatchAny returns true when any pattern in `patterns` matches
// `path`. Each pattern is dispatched through MatchGlob, which
// understands `**` on top of stdlib filepath.Match semantics.
func MatchAny(patterns []string, path string) bool {
	for _, p := range patterns {
		if MatchGlob(p, path) {
			return true
		}
	}
	return false
}

// MatchGlob extends stdlib filepath.Match with `**` semantics —
// "zero or more path segments". Patterns are written with /;
// the matcher normalises both sides so a Windows-style path
// with backslashes still matches.
//
// Algorithm: split both pattern and path on /, walk left-to-
// right. `**` consumes zero-or-more remaining path segments
// recursively; literal/single-star segments dispatch to
// filepath.Match for the per-segment glob.
func MatchGlob(pattern, path string) bool {
	pat := normalizeSlashes(pattern)
	p := normalizeSlashes(path)
	return matchSegments(splitSegs(pat), splitSegs(p))
}

func normalizeSlashes(s string) string {
	return strings.ReplaceAll(s, "\\", "/")
}

func splitSegs(s string) []string {
	if s == "" {
		return nil
	}
	out := strings.Split(s, "/")
	// Drop a leading empty segment from an absolute-looking
	// pattern (rare for the scope use-case but harmless).
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	return out
}

func matchSegments(pat, path []string) bool {
	for len(pat) > 0 {
		seg := pat[0]
		if seg == "**" {
			rest := pat[1:]
			if len(rest) == 0 {
				return true
			}
			// Try consuming 0, 1, 2, … path segments before
			// matching the remainder.
			for i := 0; i <= len(path); i++ {
				if matchSegments(rest, path[i:]) {
					return true
				}
			}
			return false
		}
		if len(path) == 0 {
			return false
		}
		ok, _ := filepath.Match(seg, path[0])
		if !ok {
			return false
		}
		pat = pat[1:]
		path = path[1:]
	}
	return len(path) == 0
}

func scopeOrKey(name, key string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	return key
}

// Example is the starter `.talyvor-scopes` body shipped by the
// CLI's `scope add` interactive flow (and the `.example` file
// at the repo root). Kept as a Go const so the binary is
// self-contained.
const Example = `{
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
  },
  "database": {
    "name": "Database Layer",
    "includes": ["internal/db/**", "migrations/**"],
    "focus": "PostgreSQL queries and schema migrations"
  },
  "frontend": {
    "name": "Frontend",
    "includes": ["frontend/src/**"],
    "excludes": ["frontend/src/**/*.test.*"],
    "focus": "React components and TypeScript"
  }
}
`

// AddScope updates the catalogue, persists the JSON file, and
// returns an error when validation fails. Used by `scope add`.
func (sm *ScopeManager) AddScope(key string, s Scope) error {
	if !scopeNameRe.MatchString(key) {
		return fmt.Errorf("scope: invalid name %q (alphanumeric + hyphens only)", key)
	}
	if strings.TrimSpace(s.Name) == "" {
		s.Name = key
	}
	if _, exists := sm.scopes[key]; exists {
		return fmt.Errorf("scope: %q already exists", key)
	}
	if len(sm.scopes)+1 > MaxScopes {
		return fmt.Errorf("scope: catalogue at cap (%d); remove one before adding", MaxScopes)
	}
	if len(s.Includes) == 0 {
		return errors.New("scope: at least one include pattern required")
	}
	sm.scopes[key] = s
	return sm.persistCatalogue()
}

func (sm *ScopeManager) persistCatalogue() error {
	body, err := json.MarshalIndent(sm.scopes, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(sm.root, ScopesFileName), append(body, '\n'), 0o644)
}
