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

// runAsk + runChat are placeholders for Phase 2. Today they print
// a helpful "coming in Phase 2" line so users discover the surface
// without bumping into a panic.
func runAsk(w io.Writer, _ config.Config, _ []string) error {
	fmt.Fprintln(w, "`ask` ships in Phase 2 once the streaming chat surface lands.")
	return nil
}

func runChat(w io.Writer, _ config.Config) error {
	fmt.Fprintln(w, "`chat` ships in Phase 2 once the streaming chat surface lands.")
	return nil
}
