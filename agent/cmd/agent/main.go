// Command agent is the Talyvor Code CLI — a sibling to the VS
// Code extension. Phase 1 ships the command structure + flag
// parsing; the interactive chat REPL + ask one-shot land in
// Phase 2 on top of the same Lens client.
package main

import (
	"bufio"
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
		return runChat(os.Stdin, stdout, stderr, cfg)
	case "test":
		return runTest(stdout, stderr, cfg, rest)
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
  ask        Ask a single question about code
  chat       Start an interactive chat REPL
  test       Generate tests for a source file
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

// runChat implements an interactive REPL. Each turn appends to a
// conversation history that's re-sent with every request; the
// system prompt rides on every request alongside the trimmed
// history. `/clear`, `/issue <id>`, `/file <path>` slash commands
// manage state without leaving the REPL.
//
// The stdin/stdout/stderr split lets tests drive the REPL with
// bytes.Buffer inputs.
func runChat(stdin io.Reader, stdout, stderr io.Writer, cfg config.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Talyvor Code Chat (issue: %s, model: %s)\n",
		nonEmpty(cfg.ActiveIssue, "(none)"), cfg.Model)
	fmt.Fprintln(stdout, `Type your message. "exit" to quit, "/clear" to reset history, "/issue <id>" to change issue, "/file <path>" to attach a file.`)

	lc := lens.New(cfg.LensURL, cfg.LensAPIKey)
	history := []lens.Message{}
	pendingFile := "" // attached via /file, consumed by next message

	scanner := bufio.NewScanner(stdin)
	// 1 MB scanner buffer accommodates large pasted snippets
	// without truncating the user's input mid-line.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for {
		fmt.Fprint(stdout, "> ")
		if !scanner.Scan() {
			// EOF or scanner error — exit cleanly so Ctrl+D / piped
			// input doesn't crash the process.
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			break
		}
		if line == "/clear" {
			history = history[:0]
			pendingFile = ""
			fmt.Fprintln(stdout, "History cleared.")
			continue
		}
		if strings.HasPrefix(line, "/issue ") {
			cfg.ActiveIssue = strings.TrimSpace(strings.TrimPrefix(line, "/issue"))
			fmt.Fprintf(stdout, "Active issue: %s\n",
				nonEmpty(cfg.ActiveIssue, "(none)"))
			continue
		}
		if strings.HasPrefix(line, "/file ") {
			path := strings.TrimSpace(strings.TrimPrefix(line, "/file"))
			body, _, err := readFileSlice(path, "")
			if err != nil {
				fmt.Fprintf(stderr, "! %v\n", err)
				continue
			}
			pendingFile = fmt.Sprintf("File: %s\n```%s\n%s\n```",
				filepath.Base(path), langFromPath(path), body)
			fmt.Fprintf(stdout, "Attached %s (%d bytes) to your next message.\n",
				filepath.Base(path), len(body))
			continue
		}

		// Build the user turn — optional file context first, then
		// the prompt itself.
		userContent := line
		if pendingFile != "" {
			userContent = pendingFile + "\n\n" + line
			pendingFile = ""
		}
		history = append(history, lens.Message{Role: "user", Content: userContent})
		history = trimChatHistory(history)

		// System prompt rides on every request so /issue changes
		// take effect immediately. We rebuild rather than store
		// because the active issue is the only dynamic input.
		messages := append([]lens.Message{
			{Role: "system", Content: chatSystemPrompt(cfg.ActiveIssue)},
		}, history...)

		ctx := context.Background()
		reply, err := lc.Complete(ctx, messages, cfg.Model, "chat",
			cfg.WorkspaceID, cfg.ActiveIssue)
		if err != nil {
			fmt.Fprintf(stderr, "! %v\n", err)
			continue
		}
		history = append(history, lens.Message{Role: "assistant", Content: reply})
		history = trimChatHistory(history)
		fmt.Fprintln(stdout, reply)
		fmt.Fprintf(stderr, "(issue=%s chars=%d)\n",
			nonEmpty(cfg.ActiveIssue, "(none)"), len(reply))
	}
	return scanner.Err()
}

// chatSystemPrompt mirrors the extension's buildSystemPrompt so
// both surfaces feel the same. Kept short — the model already
// knows it's a coding assistant; the active-issue line is the
// load-bearing part.
func chatSystemPrompt(issueID string) string {
	issueLine := "No active issue is set."
	if issueID != "" {
		issueLine = "The active issue is " + issueID + "."
	}
	return "You are an expert coding assistant. When showing code, " +
		"use markdown code fences with the language identifier. " +
		"Be concise but complete. " + issueLine
}

// trimChatHistory caps the history at MaxChatHistory pairs.
// Drops the oldest user/assistant pair as a unit so the model
// never sees a mismatched half-turn.
const MaxChatHistory = 20

func trimChatHistory(in []lens.Message) []lens.Message {
	if len(in) <= MaxChatHistory {
		return in
	}
	overflow := len(in) - MaxChatHistory
	if overflow%2 != 0 {
		overflow++
	}
	return in[overflow:]
}

// testGenModel pins Sonnet for test generation. Quality matters
// more than latency here — a wrong test is worse than no test.
const testGenModel = "claude-sonnet-4-6"

// runTest implements the `test` subcommand. Usage:
//   talyvor-code test [--output path] [--framework jest] [--issue ENG-42] <file>
//
// Reads the source file, infers the language from its extension,
// asks Lens for tests with the matching system prompt, then
// writes the result to --output (defaulting to the conventional
// test-file companion next to the source). `--output -` streams
// to stdout for shell composition.
func runTest(stdout, stderr io.Writer, cfg config.Config, args []string) error {
	var (
		outputPath string
		framework  string
		issue      string
	)
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&outputPath, "output", "", "Output file path (default: auto-detect; use '-' for stdout)")
	fs.StringVar(&framework, "framework", "", "Framework hint (jest/pytest/go-testing/...)")
	fs.StringVar(&issue, "issue", "", "Override active issue for this call")
	if err := fs.Parse(args); err != nil {
		return err
	}
	tail := fs.Args()
	if len(tail) == 0 {
		return fmt.Errorf("test: a source file is required (talyvor-code test path/to/file.go)")
	}
	sourcePath := tail[0]

	if issue != "" {
		cfg.ActiveIssue = issue
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	body, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("test: read %s: %w", sourcePath, err)
	}
	languageID := languageIDForPath(sourcePath)

	// File-exists check happens BEFORE the Lens round-trip so the
	// user doesn't burn a Sonnet call only to be told they'd
	// clobber an existing file. stdout (`--output -`) and explicit
	// `--output path` both bypass the guard — explicit caller
	// intent.
	if outputPath == "" {
		suggested := suggestTestOutput(sourcePath, languageID)
		if _, err := os.Stat(suggested); err == nil {
			return fmt.Errorf("test: %s already exists — pass --output to overwrite", suggested)
		}
	}

	system := testSystemPrompt(languageID, framework)
	user := fmt.Sprintf(
		"Generate tests for this %s file:\nFile: %s\n```%s\n%s\n```",
		languageID, filepath.Base(sourcePath), languageID, string(body),
	)

	ctx := context.Background()
	lc := lens.New(cfg.LensURL, cfg.LensAPIKey)
	reply, err := lc.Complete(ctx,
		[]lens.Message{{Role: "user", Content: system + "\n\n" + user}},
		testGenModel, "test-gen", cfg.WorkspaceID, cfg.ActiveIssue,
	)
	if err != nil {
		return err
	}
	clean := sanitiseTestOutput(reply)

	// Dispatch: stdout / explicit path / suggested path.
	if outputPath == "-" {
		fmt.Fprintln(stdout, clean)
		fmt.Fprintf(stderr, "Generated %d lines of tests (model=%s issue=%s)\n",
			lineCount(clean), testGenModel,
			nonEmpty(cfg.ActiveIssue, "(none)"))
		return nil
	}
	target := outputPath
	if target == "" {
		target = suggestTestOutput(sourcePath, languageID)
	}
	if err := os.WriteFile(target, []byte(clean), 0o644); err != nil {
		return fmt.Errorf("test: write %s: %w", target, err)
	}
	fmt.Fprintf(stdout, "Generated %d lines of tests → %s\n",
		lineCount(clean), target)
	fmt.Fprintf(stderr, "(model=%s issue=%s)\n",
		testGenModel, nonEmpty(cfg.ActiveIssue, "(none)"))
	return nil
}

// languageIDForPath maps extensions to canonical IDs that match
// the VS Code extension's choices, so prompts stay consistent
// across surfaces.
func languageIDForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts":
		return "typescript"
	case ".tsx":
		return "typescriptreact"
	case ".js":
		return "javascript"
	case ".jsx":
		return "javascriptreact"
	case ".go":
		return "go"
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
	}
	return "plaintext"
}

// suggestTestOutput is the agent-side mirror of the extension's
// suggestTestFileName. Same conventions per language.
func suggestTestOutput(sourcePath, languageID string) string {
	dir := filepath.Dir(sourcePath)
	base := filepath.Base(sourcePath)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	switch languageID {
	case "go":
		return filepath.Join(dir, stem+"_test.go")
	case "python":
		return filepath.Join(dir, "test_"+stem+".py")
	case "typescript":
		return filepath.Join(dir, stem+".test.ts")
	case "typescriptreact":
		return filepath.Join(dir, stem+".test.tsx")
	case "javascript":
		return filepath.Join(dir, stem+".test.js")
	case "javascriptreact":
		return filepath.Join(dir, stem+".test.jsx")
	case "ruby":
		return filepath.Join(dir, stem+"_spec.rb")
	case "rust":
		return filepath.Join(dir, stem+"_test.rs")
	case "java":
		return filepath.Join(dir, pascalCase(stem)+"Test.java")
	case "kotlin":
		return filepath.Join(dir, pascalCase(stem)+"Test.kt")
	case "swift":
		return filepath.Join(dir, pascalCase(stem)+"Tests.swift")
	case "c":
		return filepath.Join(dir, stem+"_test.c")
	case "cpp":
		return filepath.Join(dir, stem+"_test.cpp")
	}
	return filepath.Join(dir, stem+".test"+ext)
}

func pascalCase(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' })
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	if b.Len() == 0 {
		return s
	}
	return b.String()
}

// testSystemPrompt returns the language-tailored system prompt.
// `framework` is an optional caller hint that overrides the
// language default ("write Mocha tests for this TS code" etc.).
func testSystemPrompt(languageID, framework string) string {
	if framework != "" {
		return fmt.Sprintf(
			"Generate %s tests for the following code. Cover happy-path, "+
				"edge-case, and error-case scenarios. Return ONLY the test "+
				"code — no prose, no fences.", framework)
	}
	switch languageID {
	case "typescript", "typescriptreact", "javascript", "javascriptreact":
		return "Generate Jest tests for the following code. Use describe/it " +
			"blocks. Include happy-path, edge-case, and error-case tests. " +
			"Use TypeScript syntax when the source is TypeScript. Import the " +
			"module correctly. Return ONLY the test code — no prose, no fences."
	case "go":
		return "Generate Go tests using the standard `testing` package. " +
			"Prefer table-driven tests when there are multiple cases. " +
			"Name tests Test<FunctionName>. Return ONLY the test code — no " +
			"prose, no fences."
	case "python":
		return "Generate pytest tests. Use descriptive test_* names and " +
			"fixtures where they help. Cover happy path, edge cases, and " +
			"error cases. Return ONLY the test code — no prose, no fences."
	}
	return "Generate a thorough test suite for the following code using the " +
		"idiomatic testing framework for the language. Return ONLY the test " +
		"code — no prose, no fences."
}

// sanitiseTestOutput strips the boilerplate preambles + code
// fences models add even when told not to. Same logic as the
// extension's sanitiseGenerated; mirrored here so the surfaces
// produce identical files.
func sanitiseTestOutput(text string) string {
	out := text
	out = strings.TrimSpace(out)
	// Strip a leading code fence + optional language tag.
	if strings.HasPrefix(out, "```") {
		if i := strings.Index(out, "\n"); i >= 0 {
			out = out[i+1:]
		}
	}
	// Strip a trailing closing fence.
	if i := strings.LastIndex(out, "```"); i >= 0 && strings.TrimSpace(out[i:]) == "```" {
		out = strings.TrimRight(out[:i], "\n")
	}
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n")
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
