// Command agent is the Talyvor Code CLI — a sibling to the VS
// Code extension. Phase 1 ships the command structure + flag
// parsing; the interactive chat REPL + ask one-shot land in
// Phase 2 on top of the same Lens client.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/talyvor/code/internal/config"
	"github.com/talyvor/code/internal/lens"
	"github.com/talyvor/code/internal/track"
)

const version = "0.1.0"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// run is the testable entrypoint. Parses global flags, dispatches
// to one of the subcommands, returns an error rather than calling
// os.Exit so test code can drive it directly.
func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}

	// Global flags. We parse them with the standard library rather
	// than pull in cobra — the surface is small enough that the
	// extra dep isn't worth the build-tree noise.
	var (
		lensURL     string
		lensKey     string
		trackURL    string
		trackKey    string
		workspaceID string
		issue       string
		model       string
	)
	fs := flag.NewFlagSet("talyvor-code", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&lensURL, "lens-url", "", "Lens URL (or TALYVOR_LENS_URL)")
	fs.StringVar(&lensKey, "lens-key", "", "Lens API key (or TALYVOR_LENS_API_KEY)")
	fs.StringVar(&trackURL, "track-url", "", "Track URL (or TALYVOR_TRACK_URL)")
	fs.StringVar(&trackKey, "track-key", "", "Track API key (or TALYVOR_TRACK_API_KEY)")
	fs.StringVar(&workspaceID, "workspace", "", "Workspace ID (or TALYVOR_WORKSPACE_ID)")
	fs.StringVar(&issue, "issue", "", "Active issue identifier, e.g. ENG-42 (or TALYVOR_ISSUE)")
	fs.StringVar(&model, "model", "", "Model (default claude-haiku-4-6)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	tail := fs.Args()
	if len(tail) == 0 {
		printUsage(stdout)
		return nil
	}
	cfg := config.Load(config.Config{
		LensURL:     lensURL,
		LensAPIKey:  lensKey,
		TrackURL:    trackURL,
		TrackAPIKey: trackKey,
		WorkspaceID: workspaceID,
		ActiveIssue: issue,
		Model:       model,
	})

	cmd, rest := tail[0], tail[1:]
	switch cmd {
	case "version":
		fmt.Fprintln(stdout, "talyvor-code", version)
		return nil
	case "check":
		return runCheck(stdout, cfg)
	case "ask":
		return runAsk(stdout, cfg, rest)
	case "chat":
		return runChat(stdout, cfg)
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	}
	return fmt.Errorf("unknown command %q (try `talyvor-code help`)", cmd)
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `talyvor-code — Talyvor's AI coding agent

USAGE
  talyvor-code [flags] <command>

COMMANDS
  ask        Ask a single question about code (Phase 2 wires this)
  chat       Start an interactive chat session (Phase 2 wires this)
  check      Probe Lens and report whether everything is wired up
  version    Print the agent version

FLAGS
  --lens-url        Lens URL (or TALYVOR_LENS_URL)
  --lens-key        Lens API key (or TALYVOR_LENS_API_KEY)
  --track-url       Track URL (or TALYVOR_TRACK_URL)
  --track-key       Track API key (or TALYVOR_TRACK_API_KEY)
  --workspace       Workspace ID (or TALYVOR_WORKSPACE_ID)
  --issue           Active issue, e.g. ENG-42 (or TALYVOR_ISSUE)
  --model           Model (default claude-haiku-4-6)`)
}

func runCheck(w io.Writer, cfg config.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	ctx := context.Background()
	lc := lens.New(cfg.LensURL, cfg.LensAPIKey)
	ok, err := lc.Status(ctx)
	if err != nil || !ok {
		return fmt.Errorf("Lens unreachable at %s", cfg.LensURL)
	}
	fmt.Fprintf(w, "✓ Lens reachable at %s\n", cfg.LensURL)

	// Track lookup is informational. The agent works without
	// Track — cost attribution rides on the X-Talyvor-Issue header
	// that Lens itself records.
	if cfg.ActiveIssue != "" {
		tc := track.New(cfg.TrackURL, cfg.TrackAPIKey)
		if tc.IsConfigured() {
			iss, err := tc.GetIssue(ctx, cfg.WorkspaceID, cfg.ActiveIssue)
			if err != nil {
				fmt.Fprintf(w, "! Track lookup failed for %s: %v\n", cfg.ActiveIssue, err)
			} else if iss == nil {
				fmt.Fprintf(w, "! Issue %s not found in Track\n", cfg.ActiveIssue)
			} else {
				fmt.Fprintf(w, "✓ Active issue: %s — %s\n", iss.Identifier, iss.Title)
			}
		}
	}
	return nil
}

// runAsk implements the one-shot Q&A command. Usage:
//   talyvor-code ask [--file path] [--lines a-b] [--issue ENG-42] "question..."
//
// File content (when --file is supplied) gets wrapped in a fenced
// code block so the model sees structured context. The reply goes
// to stdout; a cost-attribution summary goes to stderr so callers
// can pipe stdout cleanly.
func runAsk(stdout io.Writer, cfg config.Config, args []string) error {
	var (
		filePath  string
		lineRange string
		issue     string
	)
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&filePath, "file", "", "Path to file to include as context")
	fs.StringVar(&lineRange, "lines", "", "Line range, e.g. 10-50 (requires --file)")
	fs.StringVar(&issue, "issue", "", "Override active issue for this call")
	if err := fs.Parse(args); err != nil {
		return err
	}
	tail := fs.Args()
	if len(tail) == 0 {
		return fmt.Errorf("ask: question is required")
	}
	question := strings.Join(tail, " ")

	if issue != "" {
		cfg.ActiveIssue = issue
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	// File context precedes the question — the model sees what
	// it's looking at before being told what to do with it.
	var prompt strings.Builder
	if filePath != "" {
		body, lang, err := readFileSlice(filePath, lineRange)
		if err != nil {
			return err
		}
		fmt.Fprintf(&prompt, "File: %s\n```%s\n%s\n```\n\n",
			filepath.Base(filePath), lang, body)
	}
	prompt.WriteString("Question: ")
	prompt.WriteString(question)

	ctx := context.Background()
	lc := lens.New(cfg.LensURL, cfg.LensAPIKey)
	out, err := lc.Complete(ctx,
		[]lens.Message{{Role: "user", Content: prompt.String()}},
		cfg.Model, "ask", cfg.WorkspaceID, cfg.ActiveIssue)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, out)
	// Cost-attribution summary on stderr — keeps stdout clean
	// for pipes.
	fmt.Fprintf(os.Stderr, "issue=%s model=%s chars=%d\n",
		nonEmpty(cfg.ActiveIssue, "(none)"), cfg.Model, len(out))
	return nil
}

// runChat is the Phase-3 placeholder. Today it explains why the
// command exists but defers the REPL implementation.
func runChat(w io.Writer, _ config.Config) error {
	fmt.Fprintln(w, "`chat` ships in Phase 3 alongside the streaming chat surface.")
	return nil
}

// readFileSlice reads a file and optionally limits the result to a
// "N-M" line range. Returns the body + a language hint for the
// markdown fence.
func readFileSlice(path, lineRange string) (string, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("ask: read %s: %w", path, err)
	}
	body := string(raw)
	if lineRange != "" {
		lines := strings.Split(body, "\n")
		start, end, ok := parseLineRange(lineRange, len(lines))
		if !ok {
			return "", "", fmt.Errorf("ask: invalid --lines %q (want N-M)", lineRange)
		}
		body = strings.Join(lines[start-1:end], "\n")
	}
	return body, langFromPath(path), nil
}

// parseLineRange accepts "N-M" with 1-based inclusive bounds. The
// resulting range is clamped to [1, total]; malformed values
// return ok=false so the handler can surface a clear error.
func parseLineRange(s string, total int) (int, int, bool) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	start, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	end, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || start <= 0 || end < start {
		return 0, 0, false
	}
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	return start, end, true
}

// langFromPath maps a file extension to a markdown-fence language
// hint. Unknown extensions return the empty string — the model
// still reads the code, just without the hint.
func langFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx":
		return "javascript"
	case ".py":
		return "python"
	case ".rb":
		return "ruby"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".kt":
		return "kotlin"
	case ".swift":
		return "swift"
	case ".c":
		return "c"
	case ".cpp", ".cc", ".cxx":
		return "cpp"
	case ".sh":
		return "bash"
	case ".json":
		return "json"
	case ".yml", ".yaml":
		return "yaml"
	case ".sql":
		return "sql"
	}
	return ""
}

func nonEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
