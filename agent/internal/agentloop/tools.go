// Package agentloop is the iterative, tool-using coding agent: an OBSERVE/ACT loop
// the model drives via tool calls (search → read → edit → run → observe → re-plan),
// replacing the bounded single-pass plan→generate→heal pipeline. Every file tool is
// confined to the repo root (S11); run() reuses the injection-safe runner. The loop
// is bounded (step budget + no-progress detector) so it is safe to run unattended.
package agentloop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/talyvor/code/internal/codebase"
	"github.com/talyvor/code/internal/diff"
	"github.com/talyvor/code/internal/runner"
)

// writeConfined writes content to an ALREADY-confined absolute path, creating
// parent dirs. The caller must pass a path returned by codebase.Confine.
func writeConfined(abs, content string) error {
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, []byte(content), 0o644)
}

// maxObsBytes caps a single tool observation so a huge file/command output can't
// blow the loop's context window. Truncation is marked.
const maxObsBytes = 8 * 1024

// defaultRunTimeout bounds a single run() so a hung build can't stall the loop.
const defaultRunTimeout = 120 * time.Second

// Tool is one action the agent can take on a turn. Run receives the model's JSON
// args and returns an OBSERVATION (fed back to the model next turn). A returned
// error means the tool could not act at all (e.g. a path escaped the root); the
// loop surfaces it as an observation and the model re-plans — a tool error never
// kills the loop.
type Tool interface {
	Name() string
	Description() string
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

// Registry maps tool names to tools and dispatches by name.
type Registry map[string]Tool

func (r Registry) Register(t Tool) { r[t.Name()] = t }

func (r Registry) Dispatch(ctx context.Context, name string, args json.RawMessage) (string, error) {
	t, ok := r[name]
	if !ok {
		return "", fmt.Errorf("unknown tool %q (available: %s)", name, strings.Join(r.Names(), ", "))
	}
	return t.Run(ctx, args)
}

func (r Registry) Names() []string {
	out := make([]string, 0, len(r))
	for n := range r {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// DefaultTools builds the standard agent tool set for a repo root + (optional)
// retriever: search_codebase, read_file, edit_file, run. A nil retriever leaves
// search available but note-only (no index built).
func DefaultTools(root string, ret codebase.Retriever) Registry {
	reg := Registry{}
	reg.Register(NewSearchTool(ret, 6))
	reg.Register(NewReadTool(root))
	reg.Register(NewEditTool(root))
	reg.Register(NewRunTool(root))
	return reg
}

func truncate(s string) string {
	if len(s) <= maxObsBytes {
		return s
	}
	return s[:maxObsBytes] + "\n… (truncated)"
}

// ── read_file ─────────────────────────────────────────

type readTool struct{ root string }

// NewReadTool returns the confined file reader.
func NewReadTool(root string) Tool { return readTool{root: root} }

func (readTool) Name() string { return "read_file" }
func (readTool) Description() string {
	return `read_file {"path":"rel/path.go","start":1,"end":40} — read a file (optional 1-based line span). Confined to the repo root.`
}

func (t readTool) Run(_ context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Path  string `json:"path"`
		Start int    `json:"start"`
		End   int    `json:"end"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("read_file: bad args: %w", err)
	}
	abs, err := codebase.Confine(t.root, a.Path)
	if err != nil {
		return "", err
	}
	content, err := codebase.ReadFile(abs, codebase.DefaultMaxFileBytes)
	if err != nil {
		return "", fmt.Errorf("read_file: %w", err)
	}
	if a.Start > 0 || a.End > 0 {
		content = sliceLines(content, a.Start, a.End)
	}
	return truncate(fmt.Sprintf("%s:\n%s", a.Path, content)), nil
}

func sliceLines(content string, start, end int) string {
	lines := strings.Split(content, "\n")
	if start < 1 {
		start = 1
	}
	if end < 1 || end > len(lines) {
		end = len(lines)
	}
	if start > len(lines) {
		return ""
	}
	return strings.Join(lines[start-1:end], "\n")
}

// ── edit_file ─────────────────────────────────────────

type editTool struct{ root string }

// NewEditTool returns the confined file writer (writes a file's COMPLETE new
// content and returns a unified diff).
func NewEditTool(root string) Tool { return editTool{root: root} }

func (editTool) Name() string { return "edit_file" }
func (editTool) Description() string {
	return `edit_file {"path":"rel/path.go","content":"<full new file content>"} — write a file's COMPLETE new content. Confined to the repo root; returns a unified diff.`
}

func (t editTool) Run(_ context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("edit_file: bad args: %w", err)
	}
	abs, err := codebase.Confine(t.root, a.Path)
	if err != nil {
		return "", err
	}
	original, _ := codebase.ReadFile(abs, codebase.DefaultMaxFileBytes) // "" for a new file
	if err := writeConfined(abs, a.Content); err != nil {
		return "", fmt.Errorf("edit_file: %w", err)
	}
	d := diff.GenerateUnifiedDiff(original, a.Content, a.Path, 3)
	if strings.TrimSpace(d) == "" {
		d = "(no change)"
	}
	return truncate(fmt.Sprintf("edited %s\n%s", a.Path, d)), nil
}

// ── run ───────────────────────────────────────────────

type runTool struct {
	root    string
	timeout time.Duration
}

// NewRunTool returns the confined command runner (build/test/shell), reusing the
// injection-safe runner primitive; the command executes in the repo root.
func NewRunTool(root string) Tool { return runTool{root: root, timeout: defaultRunTimeout} }

func (runTool) Name() string { return "run" }
func (runTool) Description() string {
	return `run {"cmd":"go test ./..."} — run a build/test/shell command in the repo root; returns exit code + captured output.`
}

func (t runTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Cmd string `json:"cmd"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("run: bad args: %w", err)
	}
	if strings.TrimSpace(a.Cmd) == "" {
		return "", fmt.Errorf("run: empty command")
	}
	res, err := runner.Run(ctx, a.Cmd, t.root, t.timeout)
	if err != nil {
		return "", fmt.Errorf("run: %w", err)
	}
	// A non-zero exit is a normal OBSERVATION (the model re-plans on it), never an error.
	out := res.Stdout
	if res.Stderr != "" {
		out += res.Stderr
	}
	return truncate(fmt.Sprintf("$ %s\nexit %d\n%s", a.Cmd, res.ExitCode, out)), nil
}

// ── search_codebase ───────────────────────────────────

type searchTool struct {
	ret codebase.Retriever
	k   int
}

// NewSearchTool returns semantic retrieval over the codebase index (the REAL
// retriever, not path-substring). A nil retriever means "no index built" — search
// returns a clear note so the model proceeds with read/run instead.
func NewSearchTool(ret codebase.Retriever, k int) Tool {
	if k <= 0 {
		k = 6
	}
	return searchTool{ret: ret, k: k}
}

func (searchTool) Name() string { return "search_codebase" }
func (searchTool) Description() string {
	return `search_codebase {"query":"how auth works"} — semantic retrieval over the codebase index; returns the most relevant chunks (file:span + content).`
}

func (t searchTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var a struct {
		Query string `json:"query"`
		K     int    `json:"k"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("search_codebase: bad args: %w", err)
	}
	if t.ret == nil {
		return "(no semantic index — run `talyvor-code index`; use read_file/run instead)", nil
	}
	k := a.K
	if k <= 0 {
		k = t.k
	}
	chunks, err := t.ret.Retrieve(ctx, a.Query, k)
	if err != nil {
		return "", fmt.Errorf("search_codebase: %w", err)
	}
	if len(chunks) == 0 {
		return "(no relevant chunks found)", nil
	}
	var b strings.Builder
	for _, c := range chunks {
		fmt.Fprintf(&b, "// %s:%d-%d (score %.3f)\n%s\n\n", c.File, c.StartLine, c.EndLine, c.Score, c.Content)
	}
	return truncate(b.String()), nil
}
