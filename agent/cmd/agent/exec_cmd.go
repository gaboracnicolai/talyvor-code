package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/talyvor/code/internal/config"
	"github.com/talyvor/code/internal/sidecar"
)

// exitCode carries a child process's exit status out through the ordinary error return, so main
// can reproduce it rather than flattening every failure to 1. `talyvor-code exec -- npm test` is
// worthless in CI if a failing child reports success.
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("child exited with status %d", int(e)) }

// runExec runs another tool's coding agent against Lens, with this repository's issue attached.
//
// ⚠ WHY THIS COMMAND EXISTS. Talyvor Code attributes its own spend. Plain Claude Code pointed at
// Lens attributes NOTHING: every request lands unattributed, so the per-issue cost that is the
// product's whole point is empty for the people spending the most. The identifier only exists on
// the developer's machine, next to the checkout, so this cannot be fixed on the server.
func runExec(stdout, stderr io.Writer, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.SetOutput(stderr)
	port := fs.Int("port", 0, "pin the local proxy to this port (default: any free port)")
	quiet := fs.Bool("quiet", false, "suppress the startup summary")
	fs.Usage = func() {
		fmt.Fprintln(stderr, `talyvor-code exec — run another AI tool with this repository's issue attached

USAGE
  talyvor-code exec [flags] -- <command> [args...]

EXAMPLES
  talyvor-code exec -- claude
  talyvor-code exec -- claude -p "explain internal/proxy"
  talyvor-code --issue ENG-42 exec -- claude

FLAGS
  --port    pin the local proxy to this port (default: any free port)
  --quiet   suppress the startup summary`)
	}

	// ⚠ SPLIT ON "--" BEFORE PARSING. The child has its own flags, and -p or --model belong to it.
	// Letting the flag package reach them would consume the child's arguments as ours.
	ours, child := args, []string(nil)
	if i := slices.Index(args, "--"); i >= 0 {
		ours, child = args[:i], args[i+1:]
	}
	if err := fs.Parse(ours); err != nil {
		return err
	}
	if len(child) == 0 {
		// Tolerate the separator being left out when it is unambiguous.
		child = fs.Args()
	}
	if len(child) == 0 {
		fs.Usage()
		return errors.New("no command given: try `talyvor-code exec -- claude`")
	}

	scfg := sidecar.Config{
		LensURL:     cfg.LensURL,
		LensAPIKey:  cfg.LensAPIKey,
		Issue:       cfg.ActiveIssue, // already resolved by internal/issueref in run()
		WorkspaceID: cfg.WorkspaceID,
		Port:        *port,
		Log:         stderr,
	}

	code, err := sidecar.Run(scfg, child, sidecar.Stdio{In: os.Stdin, Out: stdout, Err: stderr},
		func(s *sidecar.Sidecar) {
			if !*quiet {
				printExecBanner(stderr, s, child[0], cfg.ActiveIssue)
			}
		})
	if err != nil {
		return err
	}
	if code != 0 {
		return exitCode(code)
	}
	return nil
}

// printExecBanner says what is about to happen, in the terms a developer would want to check.
//
// ⚠ IT STATES THE CONNECTOR CONSEQUENCE EXPLICITLY. Measured against Claude Code 2.1.221: setting
// ANTHROPIC_API_KEY makes it print "claude.ai connectors are disabled because ANTHROPIC_API_KEY or
// another auth source is set" and the user silently loses every connector. This tool therefore
// plants no key — but a developer who has one set in their own shell IS having it withheld from
// the child, which changes which account is billed. That is worth a line rather than a surprise.
func printExecBanner(w io.Writer, s *sidecar.Sidecar, command, issue string) {
	fmt.Fprintf(w, "talyvor exec: running %s through Lens on %s\n", command, s.BaseURL())
	if issue == "" {
		fmt.Fprintln(w, "  issue=(none) — this work will be recorded in Track as unattributed")
	} else {
		fmt.Fprintf(w, "  issue=%s — spend will be attributed to it in Track\n", issue)
	}
	fmt.Fprintln(w, "  prompts pass through unread: the proxy never logs, stores or inspects them")
	if os.Getenv("ANTHROPIC_API_KEY") != "" || os.Getenv("ANTHROPIC_AUTH_TOKEN") != "" {
		fmt.Fprintln(w, "  your ANTHROPIC_API_KEY is NOT passed to the child — Lens is billed instead of your")
		fmt.Fprintln(w, "  own Anthropic account, and your claude.ai connectors keep working because of it")
	} else {
		fmt.Fprintln(w, "  your claude.ai login and connectors are untouched")
	}
}
