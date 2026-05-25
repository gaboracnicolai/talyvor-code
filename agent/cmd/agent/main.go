// Command agent is the Talyvor Code CLI — a sibling to the VS
// Code extension. Phase 1 ships the command structure + flag
// parsing; the interactive chat REPL + ask one-shot land in
// Phase 2 on top of the same Lens client.
package main

import (
	"bufio"
	"context"
	jsonPkg "encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/talyvor/code/internal/codebase"
	"github.com/talyvor/code/internal/config"
	diffPkg "github.com/talyvor/code/internal/diff"
	"github.com/talyvor/code/internal/docs"
	gitpkg "github.com/talyvor/code/internal/git"
	"github.com/talyvor/code/internal/lens"
	"github.com/talyvor/code/internal/mcp"
	modelpkg "github.com/talyvor/code/internal/model"
	"github.com/talyvor/code/internal/rules"
	"github.com/talyvor/code/internal/shell"
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
		docsURL     string
		docsKey     string
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
	fs.StringVar(&docsURL, "docs-url", "", "Docs URL (or TALYVOR_DOCS_URL)")
	fs.StringVar(&docsKey, "docs-key", "", "Docs API key (or TALYVOR_DOCS_API_KEY)")
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
		DocsURL:     docsURL,
		DocsAPIKey:  docsKey,
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
	case "run":
		return runAgent(os.Stdin, stdout, stderr, cfg, rest)
	case "docs":
		return runDocs(stdout, stderr, cfg, rest)
	case "review":
		return runReview(os.Stdin, stdout, stderr, cfg, rest)
	case "commit":
		return runCommit(os.Stdin, stdout, stderr, cfg, rest)
	case "serve":
		return runServe(stdout, stderr, cfg, rest)
	case "init":
		return runInit(stdout, stderr)
	case "shell", "sh":
		return runShell(os.Stdin, stdout, stderr, cfg, rest)
	case "models":
		return runModels(stdout)
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
  run        Run an agentic multi-file task
  review     Review staged changes or files for bugs/security/perf
  commit     Generate a conventional commit message from staged changes
  docs       Search and query Talyvor Docs
  serve      Start the Talyvor Code MCP server
  init       Write a starter .talyvor-rules file in the current directory
  shell      Generate a shell command from a description (alias: sh)
  models     List supported AI models with their profiles
  check      Probe Lens and report whether everything is wired up
  version    Print the agent version

FLAGS
  --lens-url        Lens URL (or TALYVOR_LENS_URL)
  --lens-key        Lens API key (or TALYVOR_LENS_API_KEY)
  --track-url       Track URL (or TALYVOR_TRACK_URL)
  --track-key       Track API key (or TALYVOR_TRACK_API_KEY)
  --docs-url        Docs URL (or TALYVOR_DOCS_URL)
  --docs-key        Docs API key (or TALYVOR_DOCS_API_KEY)
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
		modelOpt  string
	)
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&filePath, "file", "", "Path to file to include as context")
	fs.StringVar(&lineRange, "lines", "", "Line range, e.g. 10-50 (requires --file)")
	fs.StringVar(&issue, "issue", "", "Override active issue for this call")
	fs.StringVar(&modelOpt, "model", "", "Override AI model (see `talyvor-code models`)")
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
	chosenModel, err := resolveAndValidate(modelOpt, cfg.Model, "ask")
	if err != nil {
		return err
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
		// Surface .talyvor-rules at the start of the prompt; the
		// model attends to leading content more reliably than the
		// tail. Language is derived from the included file when
		// present.
		if rs, _ := rules.Load("."); rs != nil {
			prompt.WriteString(rules.PromptPrefix(rules.ForLanguage(rs, lang)))
		}
		fmt.Fprintf(&prompt, "File: %s\n```%s\n%s\n```\n\n",
			filepath.Base(filePath), lang, body)
	} else {
		if rs, _ := rules.Load("."); rs != nil {
			prompt.WriteString(rules.PromptPrefix(rules.ForLanguage(rs, "")))
		}
	}
	prompt.WriteString("Question: ")
	prompt.WriteString(question)

	ctx := context.Background()
	lc := lens.New(cfg.LensURL, cfg.LensAPIKey)
	out, err := lc.Complete(ctx,
		[]lens.Message{{Role: "user", Content: prompt.String()}},
		chosenModel, "ask", cfg.WorkspaceID, cfg.ActiveIssue)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, out)
	// Cost-attribution summary on stderr — keeps stdout clean
	// for pipes.
	fmt.Fprintf(os.Stderr, "issue=%s model=%s chars=%d\n",
		nonEmpty(cfg.ActiveIssue, "(none)"), chosenModel, len(out))
	return nil
}

// resolveAndValidate is the small helper every subcommand uses:
// resolve via the priority order (flag → env/cfg.Model → command
// default) and validate against the catalogue. Returns a clean
// error for invalid IDs so the user sees the valid list.
func resolveAndValidate(flagValue, envValue, command string) (string, error) {
	chosen := modelpkg.ResolveModel(flagValue, envValue, command)
	if err := modelpkg.Validate(chosen); err != nil {
		return "", err
	}
	return chosen, nil
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
	chosenModel, err := resolveAndValidate("", cfg.Model, "chat")
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Talyvor Code Chat (issue: %s, model: %s)\n",
		nonEmpty(cfg.ActiveIssue, "(none)"), chosenModel)
	fmt.Fprintln(stdout, `Type your message. "exit" to quit, "/clear" to reset history, "/issue <id>" to change issue, "/model <id>" to swap model, "/file <path>" to attach a file.`)

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
		if strings.HasPrefix(line, "/model ") {
			newModel := strings.TrimSpace(strings.TrimPrefix(line, "/model"))
			if err := modelpkg.Validate(newModel); err != nil {
				fmt.Fprintf(stderr, "! %v\n", err)
				continue
			}
			chosenModel = newModel
			fmt.Fprintf(stdout, "Model: %s\n", chosenModel)
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
		reply, err := lc.Complete(ctx, messages, chosenModel, "chat",
			cfg.WorkspaceID, cfg.ActiveIssue)
		if err != nil {
			fmt.Fprintf(stderr, "! %v\n", err)
			continue
		}
		history = append(history, lens.Message{Role: "assistant", Content: reply})
		history = trimChatHistory(history)
		fmt.Fprintln(stdout, reply)
		fmt.Fprintf(stderr, "(issue=%s model=%s chars=%d)\n",
			nonEmpty(cfg.ActiveIssue, "(none)"), chosenModel, len(reply))
	}
	return scanner.Err()
}

// chatSystemPrompt mirrors the extension's buildSystemPrompt so
// both surfaces feel the same. Kept short — the model already
// knows it's a coding assistant; the active-issue line is the
// load-bearing part. Project rules (if any) ride at the very
// start of the system prompt where models attend most reliably.
func chatSystemPrompt(issueID string) string {
	issueLine := "No active issue is set."
	if issueID != "" {
		issueLine = "The active issue is " + issueID + "."
	}
	base := "You are an expert coding assistant. When showing code, " +
		"use markdown code fences with the language identifier. " +
		"Be concise but complete. " + issueLine
	rs, _ := rules.Load(".")
	if rs == nil {
		return base
	}
	return rules.PromptPrefix(rules.ForLanguage(rs, "")) + base
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

// MaxAgentFiles caps a single task at 20 files. Beyond that the
// model usually loses the plot and the user loses oversight — a
// hard ceiling keeps runaway tasks contained.
const MaxAgentFiles = 20

// Model selection for the agent now flows through
// modelpkg.ResolveModel(--model, $TALYVOR_MODEL, "run"). Haiku is
// too brittle for multi-file refactors by default; the resolver
// picks Sonnet unless the user opts otherwise.

// runAgent implements the agentic flow:
//   1. Ask Lens for a JSON plan listing files to touch.
//   2. For each file, ask Lens for the complete new content.
//   3. Render a unified diff for human review.
//   4. With --yes apply automatically; with --dry-run stop after
//      the diff render; otherwise prompt per file.
//   5. Apply approved changes, print summary.
//
// `stdin` is split out so tests can drive the per-file prompts
// with a bytes.Buffer instead of needing a real TTY.
func runAgent(stdin io.Reader, stdout, stderr io.Writer, cfg config.Config, args []string) error {
	var (
		dryRun   bool
		yes      bool
		issue    string
		modelOpt string
	)
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&dryRun, "dry-run", false, "Show plan + diffs without writing")
	fs.BoolVar(&yes, "yes", false, "Auto-approve all changes")
	fs.StringVar(&issue, "issue", "", "Override active issue for this task")
	fs.StringVar(&modelOpt, "model", "", "Override AI model (see `talyvor-code models`)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	tail := fs.Args()
	if len(tail) == 0 {
		return fmt.Errorf("run: task description required")
	}
	taskDesc := strings.Join(tail, " ")

	if issue != "" {
		cfg.ActiveIssue = issue
	}
	chosenModel, err := resolveAndValidate(modelOpt, cfg.Model, "run")
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	workspaceRoot, err := os.Getwd()
	if err != nil {
		return err
	}

	lc := lens.New(cfg.LensURL, cfg.LensAPIKey)
	ctx := context.Background()

	// ── Phase 0: index codebase ──
	// Index up front so the planner sees the actual stack and so
	// we can later fuzzy-match planner-supplied paths against
	// real ones (planner hallucinations are common when paths
	// drift from the task description).
	idx, idxErr := codebase.IndexDirectory(workspaceRoot, codebase.DefaultMaxFiles)
	codebaseSummary := ""
	if idxErr != nil {
		fmt.Fprintf(stderr, "! codebase index: %v (continuing without it)\n", idxErr)
	} else {
		codebaseSummary = idx.Summary()
		fmt.Fprintln(stdout, "▸ Indexed codebase:")
		for _, line := range strings.Split(strings.TrimRight(codebaseSummary, "\n"), "\n") {
			fmt.Fprintf(stdout, "  %s\n", line)
		}
	}

	// ── Phase 1: plan ──
	fmt.Fprintln(stdout, "▸ Planning…")
	planText, err := lc.Complete(ctx,
		[]lens.Message{{Role: "user", Content: planPrompt(taskDesc, workspaceRoot, cfg.ActiveIssue, codebaseSummary)}},
		chosenModel, "agent-plan", cfg.WorkspaceID, cfg.ActiveIssue,
	)
	if err != nil {
		return fmt.Errorf("plan: %w", err)
	}
	plan, err := parsePlan(planText)
	if err != nil {
		return fmt.Errorf("plan: %w (raw response: %s)", err, truncate(planText, 200))
	}
	if len(plan.Files) == 0 {
		return fmt.Errorf("plan: model returned no files to change")
	}
	if len(plan.Files) > MaxAgentFiles {
		return fmt.Errorf("plan: %d files exceeds MaxAgentFiles (%d)", len(plan.Files), MaxAgentFiles)
	}

	// ── Phase 1.5: smart file discovery ──
	// For modify ops where the planner picked a non-existent
	// path, look for a close-by file in the index and swap it
	// in. Reduces "file not found" failures during execute.
	if idx != nil {
		for i, pf := range plan.Files {
			if pf.Operation != "modify" {
				continue
			}
			abs := pf.Path
			if !isAbs(abs) {
				abs = filepath.Join(workspaceRoot, pf.Path)
			}
			if _, err := os.Stat(abs); err == nil {
				continue
			}
			matches := idx.FindRelevantFiles(filepath.Base(pf.Path), 1)
			if len(matches) > 0 {
				fmt.Fprintf(stdout, "  ↪ remapping %s → %s (closest match)\n", pf.Path, matches[0].Path)
				plan.Files[i].Path = matches[0].Path
			}
		}
	}

	fmt.Fprintln(stdout, "Plan:")
	for _, step := range plan.Plan {
		fmt.Fprintf(stdout, "  • %s\n", step)
	}
	fmt.Fprintf(stdout, "Files (%d):\n", len(plan.Files))
	for _, f := range plan.Files {
		fmt.Fprintf(stdout, "  · [%s] %s — %s\n", f.Operation, f.Path, f.Description)
	}

	// ── Phase 2: execute (per file) ──
	reader := bufio.NewReader(stdin)
	applied, skipped := 0, 0
	for i, pf := range plan.Files {
		fmt.Fprintf(stdout, "\n▸ Generating %d/%d: %s\n", i+1, len(plan.Files), pf.Path)
		change, err := generateChange(ctx, lc, cfg, taskDesc, pf, workspaceRoot, chosenModel)
		if err != nil {
			fmt.Fprintf(stderr, "! %s: %v\n", pf.Path, err)
			skipped++
			continue
		}
		d := GenerateUnifiedDiffWrap(change.OriginalContent, change.NewContent, change.Path)
		if d == "" {
			fmt.Fprintf(stdout, "  (no change)\n")
			continue
		}
		fmt.Fprintln(stdout, d)

		if dryRun {
			continue
		}
		approve := yes
		if !approve {
			fmt.Fprintf(stdout, "Apply this change? [y/N] ")
			ans, _ := reader.ReadString('\n')
			ans = strings.ToLower(strings.TrimSpace(ans))
			approve = ans == "y" || ans == "yes"
		}
		if !approve {
			skipped++
			continue
		}
		if err := writeChange(workspaceRoot, change); err != nil {
			fmt.Fprintf(stderr, "! write %s: %v\n", change.Path, err)
			skipped++
			continue
		}
		applied++
	}

	fmt.Fprintf(stdout, "\nApplied %d/%d changes (skipped %d)\n", applied, len(plan.Files), skipped)
	fmt.Fprintf(stderr, "(model=%s issue=%s)\n", chosenModel,
		nonEmpty(cfg.ActiveIssue, "(none)"))

	// Best-effort Track comment so the issue trail captures the
	// automated change. Failures here never fail the CLI — the
	// user already has the change applied locally.
	if applied > 0 && cfg.ActiveIssue != "" {
		tc := track.New(cfg.TrackURL, cfg.TrackAPIKey)
		if tc.IsConfigured() {
			comment := buildAgentCompletionComment(taskDesc, applied, chosenModel)
			cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer ccancel()
			if err := tc.AddComment(cctx, cfg.WorkspaceID, cfg.ActiveIssue, comment); err != nil {
				fmt.Fprintf(stderr, "! Track comment failed: %v\n", err)
			}
		}
	}
	return nil
}

// buildAgentCompletionComment is the body posted to Track after a
// successful agent run. Mirrors the extension-side helper so the
// audit trail looks the same regardless of which client did it.
func buildAgentCompletionComment(taskDesc string, filesChanged int, model string) string {
	return fmt.Sprintf(
		"🤖 Talyvor Agent completed task: %s\nFiles changed: %d\nModel: %s",
		taskDesc, filesChanged, model,
	)
}

// ─── review subcommand ─────────────────────────────

// Model selection for review flows through modelpkg.ResolveModel
// — defaults to Sonnet, but --model lets the user opt in to Opus
// for higher-stakes review or Haiku for a quick pass.

// runReview drives the code-review flow: read the staged diff
// (or supplied files), build a structured prompt, call Lens, and
// print the reply. Defaults to Sonnet (quality matters) but the
// user can override via --model.
func runReview(_ io.Reader, stdout, stderr io.Writer, cfg config.Config, args []string) error {
	var (
		reviewType string
		issue      string
		modelOpt   string
	)
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&reviewType, "type", "general", "Review type: general|security|performance")
	fs.StringVar(&issue, "issue", "", "Override active issue")
	fs.StringVar(&modelOpt, "model", "", "Override AI model (see `talyvor-code models`)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if issue != "" {
		cfg.ActiveIssue = issue
	}
	chosenModel, err := resolveAndValidate(modelOpt, cfg.Model, "review")
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	files := fs.Args()

	var body string
	switch {
	case len(files) > 0:
		ctx, err := codebase.ReadFilesForContext(files, codebase.DefaultMaxTotalBytes)
		if err != nil {
			return fmt.Errorf("review: %w", err)
		}
		body = ctx
	default:
		diff, err := gitpkg.GetStagedDiff()
		if err != nil {
			return fmt.Errorf("review: %w", err)
		}
		if strings.TrimSpace(diff) == "" {
			return fmt.Errorf("review: no staged changes — pass files explicitly or stage with `git add`")
		}
		body = "=== git diff --staged ===\n" + diff
	}

	system := reviewSystemPrompt(reviewType)
	if rs, _ := rules.Load("."); rs != nil {
		system = rules.PromptPrefix(rules.ForReview(rs)) + system
	}
	user := system + "\n\nReview this code:\n\n" + body

	lc := lens.New(cfg.LensURL, cfg.LensAPIKey)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := lc.Complete(ctx,
		[]lens.Message{{Role: "user", Content: user}},
		chosenModel, "code-review", cfg.WorkspaceID, cfg.ActiveIssue,
	)
	if err != nil {
		return fmt.Errorf("review: %w", err)
	}
	fmt.Fprintln(stdout, strings.TrimSpace(out))
	fmt.Fprintf(stderr, "(model=%s issue=%s)\n", chosenModel, nonEmpty(cfg.ActiveIssue, "(none)"))
	return nil
}

// reviewSystemPrompt builds the structured review prompt. The
// "type" knob shifts emphasis without overhauling the framing —
// security/performance reviews still benefit from the same
// "Issues Found / Critical / Warnings / Suggestions" skeleton.
func reviewSystemPrompt(reviewType string) string {
	focus := "Bugs and logic errors, security vulnerabilities, performance issues, code quality, and maintainability."
	switch strings.ToLower(reviewType) {
	case "security":
		focus = "Authentication & authorization gaps, input validation, injection (SQL/command/template), unsafe deserialization, secret handling, CSRF/XSS, dependency CVEs, and data leakage."
	case "performance":
		focus = "Algorithmic complexity, N+1 queries, memory allocations on hot paths, blocking I/O, lock contention, and unnecessary computation in render paths."
	}
	return "You are an expert code reviewer. Focus on: " + focus + "\n\n" +
		"Format your response as Markdown with these sections:\n" +
		"## Summary\n## Issues Found\n### Critical\n### Warnings\n### Suggestions\n## Overall Assessment"
}

// ─── commit subcommand ─────────────────────────────

// Model selection for commit flows through modelpkg.ResolveModel —
// defaults to Haiku because the subject is short and speed beats
// nuance, but --model lets a team upgrade to Sonnet if their
// commit-message standards demand it.

// runCommit generates a conventional commit message from the
// staged diff and confirms with the user before running `git
// commit`.
func runCommit(stdin io.Reader, stdout, stderr io.Writer, cfg config.Config, args []string) error {
	var (
		issue    string
		doPush   bool
		modelOpt string
	)
	fs := flag.NewFlagSet("commit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&issue, "issue", "", "Prepend issue ID to message (e.g. ENG-42:)")
	fs.BoolVar(&doPush, "push", false, "Push after a successful commit")
	fs.StringVar(&modelOpt, "model", "", "Override AI model (see `talyvor-code models`)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	chosenModel, err := resolveAndValidate(modelOpt, cfg.Model, "commit")
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	diff, err := gitpkg.GetStagedDiff()
	if err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	if strings.TrimSpace(diff) == "" {
		return fmt.Errorf("commit: no staged changes")
	}

	system := "Generate a concise git commit message. Follow the conventional-commits format:\n" +
		"<type>(<scope>): <description>\n\n" +
		"Types: feat, fix, docs, refactor, test, chore. Keep the subject under 72 characters. " +
		"Return ONLY the commit message — no explanation, no markdown fences, no quotes."

	lc := lens.New(cfg.LensURL, cfg.LensAPIKey)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	raw, err := lc.Complete(ctx,
		[]lens.Message{{Role: "user", Content: system + "\n\n=== staged diff ===\n" + diff}},
		chosenModel, "code-commit", cfg.WorkspaceID, cfg.ActiveIssue,
	)
	if err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	msg := cleanCommitMessage(raw)
	chosen := nonEmpty(issue, cfg.ActiveIssue)
	if chosen != "" {
		msg = chosen + ": " + msg
	}

	fmt.Fprintln(stdout, "Proposed commit message:")
	fmt.Fprintln(stdout, "  "+msg)
	fmt.Fprint(stdout, "Use this message? [Y/n/e] ")
	reader := bufio.NewReader(stdin)
	ans, _ := reader.ReadString('\n')
	ans = strings.ToLower(strings.TrimSpace(ans))
	switch ans {
	case "", "y", "yes":
		// accept as-is
	case "n", "no":
		fmt.Fprintln(stdout, "Aborted.")
		return nil
	case "e", "edit":
		edited, err := editInExternalEditor(stderr, msg)
		if err != nil {
			return fmt.Errorf("commit: editor: %w", err)
		}
		if strings.TrimSpace(edited) == "" {
			return fmt.Errorf("commit: empty message — aborted")
		}
		msg = edited
	default:
		return fmt.Errorf("commit: unknown response %q (expected Y/n/e)", ans)
	}

	if err := gitpkg.Commit(msg); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	fmt.Fprintln(stdout, "Committed.")
	if doPush {
		if err := gitpkg.Push(); err != nil {
			return fmt.Errorf("commit: push: %w", err)
		}
		fmt.Fprintln(stdout, "Pushed.")
	}
	return nil
}

// cleanCommitMessage strips fences/quotes/leading whitespace the
// model sometimes adds despite the "no fences" instruction.
func cleanCommitMessage(s string) string {
	out := strings.TrimSpace(s)
	out = strings.TrimPrefix(out, "```\n")
	out = strings.TrimPrefix(out, "```")
	out = strings.TrimSuffix(out, "```")
	out = strings.TrimSpace(out)
	// Drop matching surrounding quotes.
	if strings.HasPrefix(out, "\"") && strings.HasSuffix(out, "\"") {
		out = strings.TrimSuffix(strings.TrimPrefix(out, "\""), "\"")
	}
	// Take only the first line for the subject. Body (if any)
	// remains in the next lines but trimmed of trailing space.
	return strings.TrimRight(out, "\n ")
}

// editInExternalEditor opens $EDITOR with the proposed message,
// returning the trimmed user-edited body. Falls back to nano /
// vi when no $EDITOR is set, matching git's own behaviour.
func editInExternalEditor(stderr io.Writer, initial string) (string, error) {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	tmp, err := os.CreateTemp("", "talyvor-commit-*.txt")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(initial); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	cmd := exec.Command(editor, tmpName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	buf, err := os.ReadFile(tmpName)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(buf)), nil
}

// ─── docs subcommand ────────────────────────────────

// runDocs dispatches the docs sub-subcommand. The agent needs
// docs URL + key + workspace; lens credentials are optional here
// (docs.ask uses the docs server, not Lens directly).
func runDocs(stdout, stderr io.Writer, cfg config.Config, args []string) error {
	if len(args) == 0 {
		printDocsUsage(stdout)
		return nil
	}
	sub, rest := args[0], args[1:]
	if cfg.DocsURL == "" || cfg.DocsAPIKey == "" {
		return fmt.Errorf("docs: --docs-url and --docs-key (or TALYVOR_DOCS_URL/TALYVOR_DOCS_API_KEY) required")
	}
	if cfg.WorkspaceID == "" && sub != "get" {
		return fmt.Errorf("docs: --workspace (or TALYVOR_WORKSPACE_ID) required")
	}
	dc := docs.New(cfg.DocsURL, cfg.DocsAPIKey)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch sub {
	case "search":
		return runDocsSearch(ctx, stdout, dc, cfg, rest)
	case "ask":
		return runDocsAsk(ctx, stdout, dc, cfg, rest)
	case "get":
		return runDocsGet(ctx, stdout, dc, rest)
	case "help", "-h", "--help":
		printDocsUsage(stdout)
		return nil
	}
	_ = stderr
	return fmt.Errorf("unknown docs subcommand %q", sub)
}

func printDocsUsage(w io.Writer) {
	fmt.Fprintln(w, `talyvor-code docs — search and query Talyvor Docs

USAGE
  talyvor-code docs <subcommand> [args]

SUBCOMMANDS
  search <query>            Full-text + semantic search
  ask <question>            Ask the docs Q&A model
  get <spaceID/pageID>      Fetch a single page

EXAMPLES
  talyvor-code docs search "authentication flow"
  talyvor-code docs ask "How do we handle JWT refresh tokens?"
  talyvor-code docs get space-eng/page-abc`)
}

func runDocsSearch(ctx context.Context, stdout io.Writer, dc *docs.Client, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("docs search: query required")
	}
	query := strings.Join(args, " ")
	results, err := dc.Search(ctx, cfg.WorkspaceID, query, 10)
	if err != nil {
		return fmt.Errorf("docs search: %w", err)
	}
	if len(results) == 0 {
		fmt.Fprintln(stdout, "(no results)")
		return nil
	}
	for i, r := range results {
		score := r.Rank
		if score == 0 {
			score = r.Similarity
		}
		fmt.Fprintf(stdout, "%2d. [%-9s] %s — %s\n", i+1, r.Source, r.PageTitle, r.SpaceName)
		if r.Headline != "" {
			fmt.Fprintf(stdout, "    %s\n", truncate(r.Headline, 160))
		}
		fmt.Fprintf(stdout, "    rank=%.2f %s\n", score, r.URL)
	}
	return nil
}

func runDocsAsk(ctx context.Context, stdout io.Writer, dc *docs.Client, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("docs ask: question required")
	}
	question := strings.Join(args, " ")
	res, err := dc.AskDocs(ctx, cfg.WorkspaceID, question)
	if err != nil {
		return fmt.Errorf("docs ask: %w", err)
	}
	if res == nil {
		fmt.Fprintln(stdout, "(no answer)")
		return nil
	}
	fmt.Fprintln(stdout, res.Answer)
	if len(res.Sources) > 0 {
		fmt.Fprintln(stdout, "\nSources:")
		for _, s := range res.Sources {
			fmt.Fprintf(stdout, "  • %s — %s\n", s.Title, s.URL)
		}
	}
	return nil
}

func runDocsGet(ctx context.Context, stdout io.Writer, dc *docs.Client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("docs get: <spaceID/pageID> required")
	}
	ref := args[0]
	parts := strings.SplitN(ref, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("docs get: expected spaceID/pageID, got %q", ref)
	}
	page, err := dc.GetPage(ctx, parts[0], parts[1])
	if err != nil {
		return fmt.Errorf("docs get: %w", err)
	}
	if page == nil {
		return fmt.Errorf("docs get: page not found")
	}
	fmt.Fprintf(stdout, "# %s\n\n", page.Title)
	if page.FreshnessStatus != "" {
		fmt.Fprintf(stdout, "Freshness: %s\n", page.FreshnessStatus)
	}
	if page.UpdatedAt != "" {
		fmt.Fprintf(stdout, "Updated:   %s\n", page.UpdatedAt)
	}
	if page.LastVerifiedAt != "" {
		fmt.Fprintf(stdout, "Verified:  %s\n", page.LastVerifiedAt)
	}
	if page.AICostUSD > 0 {
		fmt.Fprintf(stdout, "AI cost:   $%.2f\n", page.AICostUSD)
	}
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, page.ContentText)
	return nil
}

// PlannedFile is one entry in the planner's JSON response. The
// fields mirror the prompt's contract verbatim so a hand-written
// plan slots in just as well as a model-generated one.
type PlannedFile struct {
	Path        string `json:"path"`
	Operation   string `json:"operation"` // create | modify | delete
	Description string `json:"description"`
}

type Plan struct {
	Plan  []string      `json:"plan"`
	Files []PlannedFile `json:"files"`
}

// planPrompt is the user message that follows the planner system
// prompt. We keep both in one string so the model never sees a
// stale "active issue" line from a cached system block.
// codebaseSummary (optional) gives the planner a coarse map of
// the repo — languages, file count, branch — so it picks paths
// that line up with real files.
func planPrompt(taskDesc, workspaceRoot, issueID, codebaseSummary string) string {
	system := "You are an expert software engineer agent. " +
		"Given a task description, create a detailed plan. " +
		"List the files that need to be created or modified. " +
		"Reply with a JSON object only — no prose, no markdown fences. " +
		"Schema: {\"plan\":[\"step 1\",\"step 2\"], " +
		"\"files\":[{\"path\":\"src/foo.go\",\"operation\":\"modify\",\"description\":\"…\"}]}. " +
		"Valid operations are create, modify, delete. " +
		"Use paths relative to the workspace root."
	if rs, _ := rules.Load(workspaceRoot); rs != nil {
		system = rules.PromptPrefix(rules.ForAgent(rs)) + system
	}
	out := fmt.Sprintf("%s\n\nTask: %s\nWorkspace: %s\nActive issue: %s",
		system, taskDesc, workspaceRoot, nonEmpty(issueID, "(none)"))
	if strings.TrimSpace(codebaseSummary) != "" {
		out += "\n\nCodebase summary:\n" + codebaseSummary
	}
	return out
}

// parsePlan parses the planner's JSON reply. Strips an optional
// markdown fence (models often add ```json … ``` despite being
// told not to).
func parsePlan(raw string) (Plan, error) {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		// Drop opening fence + optional language tag.
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		// Drop trailing fence if present.
		if j := strings.LastIndex(s, "```"); j >= 0 {
			s = strings.TrimRight(s[:j], "\n")
		}
	}
	var p Plan
	if err := jsonDecode([]byte(s), &p); err != nil {
		return Plan{}, fmt.Errorf("invalid plan json: %w", err)
	}
	return p, nil
}

// FileChange is the per-file payload the executor produces.
// OriginalContent is empty for new files; NewContent is always set.
type FileChange struct {
	Path            string
	Operation       string
	OriginalContent string
	NewContent      string
}

// generateChange asks Lens for the complete new content for one
// file. For modify operations we include the existing content so
// the model has the full context; for create we just describe
// what should land at the path.
func generateChange(ctx context.Context, lc *lens.Client, cfg config.Config, task string, pf PlannedFile, root, model string) (*FileChange, error) {
	change := &FileChange{Path: pf.Path, Operation: pf.Operation}
	abs := pf.Path
	if !isAbs(abs) {
		abs = filepath.Join(root, pf.Path)
	}
	if pf.Operation == "modify" || pf.Operation == "delete" {
		body, err := os.ReadFile(abs)
		if err != nil && pf.Operation == "modify" {
			return nil, fmt.Errorf("read existing: %w", err)
		}
		change.OriginalContent = string(body)
	}
	if pf.Operation == "delete" {
		change.NewContent = ""
		return change, nil
	}

	var user strings.Builder
	if rs, _ := rules.Load(root); rs != nil {
		user.WriteString(rules.PromptPrefix(rules.ForAgent(rs)))
	}
	user.WriteString("You are an expert software engineer. Make the specified change to this file. ")
	user.WriteString("Return ONLY the complete new file content. No explanations, no markdown fences. ")
	user.WriteString("The file must be syntactically correct.\n\n")
	fmt.Fprintf(&user, "Task: %s\nFile: %s\nOperation: %s\n", task, pf.Path, pf.Operation)
	if pf.Operation == "modify" {
		fmt.Fprintf(&user, "\nCurrent content:\n```\n%s\n```\n", change.OriginalContent)
	}
	fmt.Fprintf(&user, "\nChange to make: %s\n", pf.Description)

	reply, err := lc.Complete(ctx,
		[]lens.Message{{Role: "user", Content: user.String()}},
		model, "agent-execute", cfg.WorkspaceID, cfg.ActiveIssue,
	)
	if err != nil {
		return nil, err
	}
	change.NewContent = stripFences(reply)
	return change, nil
}

// writeChange persists one FileChange to disk. For create/modify
// it writes the new content (creating parent dirs as needed); for
// delete it removes the file.
func writeChange(root string, c *FileChange) error {
	abs := c.Path
	if !isAbs(abs) {
		abs = filepath.Join(root, c.Path)
	}
	switch c.Operation {
	case "delete":
		return os.Remove(abs)
	case "create", "modify":
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		return os.WriteFile(abs, []byte(c.NewContent), 0o644)
	}
	return fmt.Errorf("unknown operation %q", c.Operation)
}

// ─── tiny helpers ────────────────────────────────────

// GenerateUnifiedDiffWrap is a thin re-export of
// diff.GenerateUnifiedDiff with our standard 3 lines of context.
// Kept here so the main package doesn't have to import the diff
// package directly everywhere.
func GenerateUnifiedDiffWrap(original, modified, filename string) string {
	return diffPkg.GenerateUnifiedDiff(original, modified, filename, 3)
}

func isAbs(p string) bool {
	return filepath.IsAbs(p)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// stripFences trims the leading/trailing markdown fence the
// executor's response sometimes carries despite the prompt's "no
// fences" instruction.
func stripFences(s string) string {
	out := strings.TrimSpace(s)
	if strings.HasPrefix(out, "```") {
		if i := strings.Index(out, "\n"); i >= 0 {
			out = out[i+1:]
		}
	}
	if j := strings.LastIndex(out, "```"); j >= 0 && strings.TrimSpace(out[j:]) == "```" {
		out = strings.TrimRight(out[:j], "\n")
	}
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}

// jsonDecode is a one-line wrapper to keep the encoding/json
// import scoped to this file. Lets us swap to a tolerant parser
// later without touching call sites.
func jsonDecode(data []byte, v any) error {
	return jsonPkg.Unmarshal(data, v)
}

// Model selection for `test` flows through modelpkg.ResolveModel —
// defaults to Sonnet (quality matters; a wrong test is worse than
// no test) but `--model` lets users opt in to Opus for high-stakes
// suites or Haiku for a fast scaffold.

// runTest implements the `test` subcommand. Usage:
//   talyvor-code test [--output path] [--framework jest] [--issue ENG-42] [--model id] <file>
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
		modelOpt   string
	)
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&outputPath, "output", "", "Output file path (default: auto-detect; use '-' for stdout)")
	fs.StringVar(&framework, "framework", "", "Framework hint (jest/pytest/go-testing/...)")
	fs.StringVar(&issue, "issue", "", "Override active issue for this call")
	fs.StringVar(&modelOpt, "model", "", "Override AI model (see `talyvor-code models`)")
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
	chosenModel, err := resolveAndValidate(modelOpt, cfg.Model, "test")
	if err != nil {
		return err
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
	if rs, _ := rules.Load("."); rs != nil {
		system = rules.PromptPrefix(rules.ForTesting(rs, languageID)) + system
	}
	user := fmt.Sprintf(
		"Generate tests for this %s file:\nFile: %s\n```%s\n%s\n```",
		languageID, filepath.Base(sourcePath), languageID, string(body),
	)

	ctx := context.Background()
	lc := lens.New(cfg.LensURL, cfg.LensAPIKey)
	reply, err := lc.Complete(ctx,
		[]lens.Message{{Role: "user", Content: system + "\n\n" + user}},
		chosenModel, "test-gen", cfg.WorkspaceID, cfg.ActiveIssue,
	)
	if err != nil {
		return err
	}
	clean := sanitiseTestOutput(reply)

	// Dispatch: stdout / explicit path / suggested path.
	if outputPath == "-" {
		fmt.Fprintln(stdout, clean)
		fmt.Fprintf(stderr, "Generated %d lines of tests (model=%s issue=%s)\n",
			lineCount(clean), chosenModel,
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
		chosenModel, nonEmpty(cfg.ActiveIssue, "(none)"))
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

// ─── models subcommand ─────────────────────────────

// runModels prints the supported model catalogue as a padded
// table. We compute column widths up front so the output stays
// readable when new models with longer IDs land.
func runModels(stdout io.Writer) error {
	rows := modelpkg.ListModels()
	idW, prW, spW, ctW := len("Model"), len("Provider"), len("Speed"), len("Cost")
	for _, m := range rows {
		if len(m.ID) > idW {
			idW = len(m.ID)
		}
		if len(m.Provider) > prW {
			prW = len(m.Provider)
		}
		if len(m.SpeedTier) > spW {
			spW = len(m.SpeedTier)
		}
		if len(m.CostTier) > ctW {
			ctW = len(m.CostTier)
		}
	}
	fmt.Fprintf(stdout, "%-*s  %-*s  %-*s  %-*s  Best for\n",
		idW, "Model", prW, "Provider", spW, "Speed", ctW, "Cost")
	fmt.Fprintln(stdout, strings.Repeat("─", idW+prW+spW+ctW+12))
	for _, m := range rows {
		fmt.Fprintf(stdout, "%-*s  %-*s  %-*s  %-*s  %s\n",
			idW, m.ID, prW, m.Provider, spW, m.SpeedTier, ctW, m.CostTier,
			strings.Join(m.BestFor, ", "))
	}
	return nil
}

// ─── shell subcommand ──────────────────────────────

// maxShellFixAttempts caps the recovery loop. After three tries
// the user is better off writing the command themselves than
// burning more Lens calls.
const maxShellFixAttempts = 3

// runShell drives the shell-command generation flow. Steps:
//   1. Resolve shell + OS context.
//   2. Ask Lens (Haiku) for a single command.
//   3. Print the command.
//   4. Optional --explain pass for a brief breakdown.
//   5. Optional --run with safety prompt + fix-on-failure loop.
func runShell(stdin io.Reader, stdout, stderr io.Writer, cfg config.Config, args []string) error {
	var (
		explain  bool
		runIt    bool
		shellArg string
		issue    string
		modelOpt string
	)
	fs := flag.NewFlagSet("shell", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.BoolVar(&explain, "explain", false, "Explain the command before running")
	fs.BoolVar(&runIt, "run", false, "Execute the command after confirmation")
	fs.StringVar(&shellArg, "shell", "", "Shell type: bash|zsh|fish|powershell (default: auto)")
	fs.StringVar(&issue, "issue", "", "Override active issue for this call")
	fs.StringVar(&modelOpt, "model", "", "Override AI model (see `talyvor-code models`)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	tail := fs.Args()
	if len(tail) == 0 {
		return fmt.Errorf("shell: description required (talyvor-code shell \"kill process on port 8080\")")
	}
	description := strings.Join(tail, " ")

	if issue != "" {
		cfg.ActiveIssue = issue
	}
	chosenModel, err := resolveAndValidate(modelOpt, cfg.Model, "shell")
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	shellName := shellArg
	if shellName == "" {
		shellName = shell.DetectShell()
	}
	osName := shell.DetectOS()

	lc := lens.New(cfg.LensURL, cfg.LensAPIKey)
	ctx := context.Background()
	command, cost, err := shell.Generate(ctx, lc, &cfg, description, shellName, osName, chosenModel)
	if err != nil {
		return fmt.Errorf("shell: %w", err)
	}
	fmt.Fprintf(stdout, "$ %s\n", command)
	fmt.Fprintf(stderr, "(cost: $%.4f)\n", cost)

	if explain {
		exp, expCost, err := shell.Explain(ctx, lc, &cfg, command, shellName, osName, chosenModel)
		if err != nil {
			fmt.Fprintf(stderr, "! explain: %v\n", err)
		} else {
			fmt.Fprintln(stdout)
			fmt.Fprintln(stdout, exp)
			fmt.Fprintf(stderr, "(explain cost: $%.4f)\n", expCost)
		}
	}

	if !runIt {
		fmt.Fprintln(stdout, "")
		fmt.Fprintln(stdout, "Add --run to execute this command.")
		return nil
	}

	return runWithFixLoop(ctx, stdin, stdout, stderr, lc, &cfg, command, shellName, osName, chosenModel)
}

// runWithFixLoop executes the command, and on failure asks the
// model to suggest a fix and offers to retry. Capped at
// maxShellFixAttempts so a stubbornly-broken command doesn't
// spiral.
func runWithFixLoop(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, lc *lens.Client, cfg *config.Config, command, shellName, osName, model string) error {
	reader := bufio.NewReader(stdin)
	current := command
	for attempt := 0; attempt < maxShellFixAttempts; attempt++ {
		// Safety warning for known-dangerous patterns. Advisory
		// only — user always confirms.
		if !shell.IsCommandSafe(current) {
			fmt.Fprintln(stdout, "⚠️  This command may be destructive.")
		}
		fmt.Fprint(stdout, "Run this command? [y/N] ")
		ans, _ := reader.ReadString('\n')
		ans = strings.ToLower(strings.TrimSpace(ans))
		if ans != "y" && ans != "yes" {
			fmt.Fprintln(stdout, "Aborted.")
			return nil
		}

		out, errOut, code, runErr := shell.ExecuteCommand(ctx, current)
		if out != "" {
			fmt.Fprint(stdout, out)
			if !strings.HasSuffix(out, "\n") {
				fmt.Fprintln(stdout)
			}
		}
		if errOut != "" {
			fmt.Fprint(stderr, errOut)
			if !strings.HasSuffix(errOut, "\n") {
				fmt.Fprintln(stderr)
			}
		}
		if runErr != nil {
			return fmt.Errorf("shell: execute: %w", runErr)
		}
		if code == 0 {
			return nil
		}

		fmt.Fprintf(stderr, "Command exited with status %d.\n", code)
		if attempt == maxShellFixAttempts-1 {
			fmt.Fprintln(stderr, "Max fix attempts reached.")
			return fmt.Errorf("shell: command failed (exit %d)", code)
		}

		fmt.Fprint(stdout, "Command failed. Try to fix? [y/N] ")
		fixAns, _ := reader.ReadString('\n')
		fixAns = strings.ToLower(strings.TrimSpace(fixAns))
		if fixAns != "y" && fixAns != "yes" {
			return nil
		}
		fixed, err := shell.SuggestFix(ctx, lc, cfg, current, errOut, shellName, osName, model)
		if err != nil {
			return fmt.Errorf("shell: fix: %w", err)
		}
		if fixed == "" || fixed == current {
			fmt.Fprintln(stderr, "No improved command available.")
			return nil
		}
		fmt.Fprintf(stdout, "$ %s\n", fixed)
		current = fixed
	}
	return nil
}

// ─── init subcommand ───────────────────────────────

// runInit writes a starter `.talyvor-rules` to the current
// directory. Refuses to overwrite an existing file so a stray
// `init` doesn't blow away the team's curated rules.
func runInit(stdout, stderr io.Writer) error {
	if _, err := os.Stat(rules.RulesFileName); err == nil {
		fmt.Fprintln(stdout, "Already initialized. Edit "+rules.RulesFileName+" to customize.")
		return nil
	}
	if err := os.WriteFile(rules.RulesFileName, []byte(rules.Example), 0o644); err != nil {
		return fmt.Errorf("init: %w", err)
	}
	fmt.Fprintf(stdout, "Created %s — customize for your project.\n", rules.RulesFileName)
	_ = stderr
	return nil
}

// ─── serve subcommand ──────────────────────────────

// runServe starts the Talyvor Code MCP server. Binds 0.0.0.0 so
// IDE/agent clients on any interface can reach it; the user is
// responsible for the security posture (usually a localhost-only
// SSH tunnel or a firewalled subnet).
func runServe(stdout, stderr io.Writer, cfg config.Config, args []string) error {
	var (
		port int
		root string
	)
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.IntVar(&port, "port", 7777, "Port to listen on")
	fs.StringVar(&root, "root", ".", "Codebase root to index")
	if err := fs.Parse(args); err != nil {
		return err
	}

	lc := lens.New(cfg.LensURL, cfg.LensAPIKey)
	tc := track.New(cfg.TrackURL, cfg.TrackAPIKey)
	dc := docs.New(cfg.DocsURL, cfg.DocsAPIKey)

	server := mcp.New(lc, tc, dc, &cfg, version)
	server.SetRoot(root)
	if err := server.IndexNow(); err != nil {
		fmt.Fprintf(stderr, "! initial index: %v (continuing)\n", err)
	}
	idx := server.CurrentIndex()
	if idx != nil {
		fmt.Fprintf(stdout, "Codebase: %d files indexed (%d lines)\n", len(idx.Files), idx.TotalLines)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server.StartReindex(ctx)
	defer server.Stop()

	mux := http.NewServeMux()
	server.Routes(mux)
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	fmt.Fprintf(stdout, "Talyvor Code MCP server running on :%d\n", port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return srv.ListenAndServe()
}
