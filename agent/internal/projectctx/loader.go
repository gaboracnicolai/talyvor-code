// Package projectctx loads the .talyvor-context file — the
// project-level companion to .talyvor-rules. Rules describe HOW
// the team writes code; context describes WHAT the codebase is
// (stack, architecture, conventions). Both feed into every AI
// prompt; context lives at the *top* of the system prompt where
// models attend most reliably.
package projectctx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/talyvor/code/internal/codebase"
	"github.com/talyvor/code/internal/config"
	"github.com/talyvor/code/internal/lens"
)

// ContextFileName is the on-disk filename we search for. Matches
// the .talyvor-rules / .editorconfig convention: hidden dotfile
// at the repo root.
const ContextFileName = ".talyvor-context"

// MaxContextFileBytes caps the file at 64KB. Beyond that the
// context floods the prompt with content that adds no signal.
const MaxContextFileBytes = 64 * 1024

// MaxContextPromptBytes caps the rendered prompt section. The
// spec's 2000-char ceiling — we truncate the description to fit.
const MaxContextPromptBytes = 2000

// ProjectContext is the on-disk schema. We deliberately keep the
// shape stable across CLI + extension so a team's .talyvor-context
// works in both surfaces without re-config.
type ProjectContext struct {
	Name         string            `json:"name" yaml:"name"`
	Description  string            `json:"description" yaml:"description"`
	Stack        []string          `json:"stack" yaml:"stack"`
	Architecture string            `json:"architecture" yaml:"architecture"`
	Conventions  map[string]string `json:"conventions" yaml:"conventions"`
	KeyFiles     []string          `json:"key_files" yaml:"key_files"`
	TeamSize     int               `json:"team_size,omitempty" yaml:"team_size,omitempty"`
	Links        map[string]string `json:"links,omitempty" yaml:"links,omitempty"`

	// FilePath records the on-disk path the context was loaded
	// from. Surfaced by `context show` so a user can quickly
	// `$EDITOR <path>` the file.
	FilePath string `json:"-" yaml:"-"`
}

// Load searches for `.talyvor-context` starting at root and
// walking up to the filesystem root. Returns (nil, nil) when no
// file is found — context is always optional. Read errors and
// size-cap violations surface as errors so the user knows
// something exists but couldn't be used.
func Load(root string) (*ProjectContext, error) {
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	dir := abs
	for {
		candidate := filepath.Join(dir, ContextFileName)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			if info.Size() > MaxContextFileBytes {
				return nil, fmt.Errorf("projectctx: %s exceeds %d bytes", candidate, MaxContextFileBytes)
			}
			body, err := os.ReadFile(candidate)
			if err != nil {
				return nil, err
			}
			pc, err := ParseAuto(body)
			if err != nil {
				return nil, err
			}
			if pc == nil {
				return nil, nil
			}
			pc.FilePath = candidate
			return pc, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, nil
		}
		dir = parent
	}
}

// ParseAuto detects JSON vs YAML by sniffing the first non-blank
// byte. `{` or `[` → JSON; anything else → YAML. Returns nil
// (no error) when the content trims to nothing.
func ParseAuto(body []byte) (*ProjectContext, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, nil
	}
	first := trimmed[0]
	if first == '{' || first == '[' {
		return ParseJSON([]byte(trimmed))
	}
	return ParseYAML([]byte(trimmed))
}

// ParseJSON decodes a JSON-encoded .talyvor-context.
func ParseJSON(body []byte) (*ProjectContext, error) {
	var pc ProjectContext
	if err := json.Unmarshal(body, &pc); err != nil {
		return nil, fmt.Errorf("projectctx: invalid JSON: %w", err)
	}
	return &pc, nil
}

// ParseYAML decodes a YAML-encoded .talyvor-context.
func ParseYAML(body []byte) (*ProjectContext, error) {
	var pc ProjectContext
	if err := yaml.Unmarshal(body, &pc); err != nil {
		return nil, fmt.Errorf("projectctx: invalid YAML: %w", err)
	}
	return &pc, nil
}

// ToPromptSection renders the context as the system-prompt
// fragment that gets injected into every Lens call. Nil receiver
// returns "" so callers can build prompts unconditionally. Caps
// the rendered output at MaxContextPromptBytes — the description
// is the field most likely to balloon, so it gets trimmed first.
func (pc *ProjectContext) ToPromptSection() string {
	if pc == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("Project context:\n")
	if pc.Name != "" {
		fmt.Fprintf(&b, "  Name: %s\n", pc.Name)
	}
	if pc.Description != "" {
		desc := pc.Description
		// Description is the field most prone to bloat; cap it
		// here so the overall section stays within the 2000-char
		// envelope.
		maxDesc := MaxContextPromptBytes - 600
		if maxDesc < 200 {
			maxDesc = 200
		}
		if len(desc) > maxDesc {
			desc = desc[:maxDesc] + "…"
		}
		fmt.Fprintf(&b, "  Description: %s\n", desc)
	}
	if len(pc.Stack) > 0 {
		fmt.Fprintf(&b, "  Stack: %s\n", strings.Join(pc.Stack, ", "))
	}
	if pc.Architecture != "" {
		fmt.Fprintf(&b, "  Architecture: %s\n", pc.Architecture)
	}
	if len(pc.Conventions) > 0 {
		b.WriteString("  Key conventions:\n")
		for k, v := range pc.Conventions {
			fmt.Fprintf(&b, "    - %s: %s\n", k, v)
		}
	}
	if len(pc.KeyFiles) > 0 {
		fmt.Fprintf(&b, "  Key files: %s\n", strings.Join(pc.KeyFiles, ", "))
	}
	out := b.String()
	if len(out) > MaxContextPromptBytes {
		// Hard cap with a marker so an enormous conventions map
		// or stack list can't blow the budget either.
		out = out[:MaxContextPromptBytes-3] + "…\n"
	}
	return out
}

// Validate returns non-fatal warnings about the context. Empty
// slice means the context is fit for purpose. Callers should
// print these as info, not errors.
func (pc *ProjectContext) Validate() []string {
	if pc == nil {
		return []string{"projectctx: context is empty"}
	}
	var out []string
	if strings.TrimSpace(pc.Name) == "" {
		out = append(out, "name is required")
	}
	if len(strings.TrimSpace(pc.Description)) < 20 {
		out = append(out, "description should be at least 20 characters")
	}
	if len(pc.Stack) == 0 {
		out = append(out, "stack should list at least one technology")
	}
	return out
}

// CombinedPrefix concatenates the rules prefix (already wrapped
// in "Project rules:" envelope by the caller) and the context
// section. Rules come first, context second — the spec's choice.
// Empty string when both are empty so callers can prepend
// unconditionally.
func CombinedPrefix(rulesPrefix string, pc *ProjectContext) string {
	rp := strings.TrimRight(rulesPrefix, "\n")
	cs := strings.TrimRight(pc.ToPromptSection(), "\n")
	switch {
	case rp == "" && cs == "":
		return ""
	case rp == "":
		return cs + "\n\n"
	case cs == "":
		return rp + "\n\n"
	default:
		return rp + "\n\n" + cs + "\n\n"
	}
}

// ─── GenerateContext ─────────────────────────────────

const generatorModel = "claude-haiku-4-5"

// GenerateContext synthesises a starter .talyvor-context from
// the codebase. Reads the index + README + dependency manifest,
// asks Haiku for the JSON, and parses. The model occasionally
// wraps the JSON in fences — we strip them defensively.
func GenerateContext(
	ctx context.Context,
	root string,
	lc *lens.Client,
	cfg *config.Config,
) (*ProjectContext, error) {
	if lc == nil || !lc.IsConfigured() {
		return nil, errors.New("projectctx: lens not configured")
	}
	idx, err := codebase.IndexDirectory(root, codebase.DefaultMaxFiles)
	if err != nil {
		return nil, fmt.Errorf("projectctx: index: %w", err)
	}
	readme := readFirstN(filepath.Join(root, "README.md"), 2000)
	deps := readDependencyManifest(root)

	system := `Analyze this codebase and return a JSON project context. Return ONLY valid JSON matching this schema:

{
  "name": "string",
  "description": "string (1-2 sentences)",
  "stack": ["string", ...],
  "architecture": "string (one sentence)",
  "conventions": {"area": "convention"},
  "key_files": ["path", ...]
}

No prose, no markdown fences. Be concise — descriptions ≤ 200 chars, conventions ≤ 5 entries.`
	var user strings.Builder
	user.WriteString("Codebase summary:\n")
	user.WriteString(idx.Summary())
	if readme != "" {
		user.WriteString("\nREADME excerpt:\n")
		user.WriteString(readme)
	}
	if deps != "" {
		user.WriteString("\nDependencies:\n")
		user.WriteString(deps)
	}

	wsID, issue := "", ""
	if cfg != nil {
		wsID = cfg.WorkspaceID
		issue = cfg.ActiveIssue
	}
	raw, err := lc.Complete(ctx,
		[]lens.Message{{Role: "user", Content: system + "\n\n" + user.String()}},
		generatorModel, "context-generate", wsID, issue,
	)
	if err != nil {
		return nil, fmt.Errorf("projectctx: lens: %w", err)
	}
	pc, err := ParseAuto([]byte(StripFences(raw)))
	if err != nil {
		return nil, err
	}
	if pc == nil {
		return nil, errors.New("projectctx: empty model response")
	}
	return pc, nil
}

// StripFences removes the common ```json … ``` wrapping models
// produce despite "no fences" instructions. Exported so the
// extension's TS port can mirror the behaviour.
func StripFences(s string) string {
	out := strings.TrimSpace(s)
	if strings.HasPrefix(out, "```") {
		if i := strings.Index(out, "\n"); i >= 0 {
			out = out[i+1:]
		}
	}
	if strings.HasSuffix(out, "```") {
		out = strings.TrimRight(out[:len(out)-3], "\n")
	}
	return strings.TrimSpace(out)
}

func readFirstN(path string, n int) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(body) > n {
		body = body[:n]
	}
	return string(body)
}

func readDependencyManifest(root string) string {
	for _, name := range []string{"go.mod", "package.json", "Cargo.toml", "requirements.txt", "Gemfile"} {
		body, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		if len(body) > 2000 {
			body = body[:2000]
		}
		return fmt.Sprintf("=== %s ===\n%s", name, string(body))
	}
	return ""
}

// ToJSON marshals the context with stable 2-space indentation
// for `context show` and the `init` example writer.
func (pc *ProjectContext) ToJSON() ([]byte, error) {
	if pc == nil {
		return []byte("{}"), nil
	}
	return json.MarshalIndent(pc, "", "  ")
}

// Example is the placeholder content `talyvor-code init` writes
// when the user declines auto-generation. Kept as a Go const so
// the CLI binary is self-contained.
const Example = `{
  "name": "MyApp",
  "description": "B2B e-commerce platform serving enterprise customers",
  "stack": ["Go", "PostgreSQL", "Redis", "React", "TypeScript"],
  "architecture": "Microservices with gRPC internal communication",
  "conventions": {
    "database": "Use pgx driver, never database/sql",
    "errors": "Always wrap errors with fmt.Errorf('context: %w', err)",
    "testing": "Table-driven tests, use testify/assert",
    "api": "RESTful JSON API, OpenAPI spec in docs/api/"
  },
  "key_files": [
    "cmd/server/main.go",
    "internal/api/router.go",
    "internal/db/db.go"
  ],
  "links": {
    "docs": "https://docs.myapp.com",
    "jira": "https://myapp.atlassian.net"
  }
}
`
