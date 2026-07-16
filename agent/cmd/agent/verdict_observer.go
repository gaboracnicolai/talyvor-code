package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/talyvor/code/internal/agentloop"
)

// verdictReporter is the slice of the Lens client the observer needs — self-reporting a
// mechanical build/test verdict for one output_id. The real *lens.Client satisfies it;
// tests inject a fake. Code holds NO authority: this reports a mechanical fact and Lens
// is the authority (gateway-bound output_id, first-report-wins).
type verdictReporter interface {
	ReportMechanicalVerdict(ctx context.Context, outputID, verdict string, exitCode int, tool, reason string) error
}

// verdictObserver implements agentloop.Observer to bring K4 mechanical-verdict reporting
// to the iterative loop. It applies the "sound 1:1, or skip" pairing rule (see
// BUILD_STATE): track the output_ids of edit_file generations since the last build/test
// run; on a build/test run, report the verdict for a generation ONLY when EXACTLY ONE
// un-verdicted, output_id-known code-producing generation preceded it — otherwise skip.
// Best-effort: a report failure is logged + swallowed and NEVER blocks the loop.
//
// It is installed ONLY when cfg.ReportVerdicts is on, so with the flag off the loop is
// byte-identical to before (no observer ⇒ the pure loop path).
type verdictObserver struct {
	ctx     context.Context // the loop run's context (the reporter call is fire-and-forget within it)
	rep     verdictReporter
	log     io.Writer // best-effort error sink (may be nil)
	pending []string  // output_ids of edit_file generations since the last build/test run
}

func newVerdictObserver(ctx context.Context, rep verdictReporter, log io.Writer) *verdictObserver {
	return &verdictObserver{ctx: ctx, rep: rep, log: log}
}

func (o *verdictObserver) ObserveStep(s agentloop.StepInfo) {
	switch s.Tool {
	case "edit_file":
		// A code-producing generation. A FAILED edit produced no code (skip). Track its
		// output_id (which may be "" / unknown — handled at report time).
		if !s.ToolErr {
			o.pending = append(o.pending, s.OutputID)
		}
	case "run":
		if !s.Ran {
			return // a run that didn't actually execute (bad args) — ignore
		}
		cmd := runCommand(s.Args)
		if !looksLikeBuildOrTest(cmd) {
			return // not a build/test — don't report, don't consume the pending edits
		}
		// A build/test run TESTS the cumulative state of every pending edit; whether we
		// report or skip, those generations have now been tested → clear the set.
		pend := o.pending
		o.pending = nil
		// Sound 1:1 ONLY: exactly one un-verdicted generation, with a known output_id.
		if len(pend) != 1 || pend[0] == "" {
			return // zero / 1:many batch / unknown output_id → not soundly attributable → skip
		}
		verdict := mechanicalVerdict(cmd, s.RunExit == 0)
		if err := o.rep.ReportMechanicalVerdict(o.ctx, pend[0], verdict, s.RunExit, cmd, ""); err != nil && o.log != nil {
			fmt.Fprintf(o.log, "! verdict report failed (ignored): %v\n", err)
		}
	}
}

// runCommand extracts the command string from a `run` tool call's args.
func runCommand(args json.RawMessage) string {
	var a struct {
		Cmd string `json:"cmd"`
	}
	_ = json.Unmarshal(args, &a)
	return a.Cmd
}

// buildTestTokens are the CONSERVATIVE markers of a build/compile/test command. A false
// positive (a verdict for a non-build command like `ls`) would corrupt the K4 signal; a
// false negative (skipping a real build) only lowers coverage — so we match only
// well-known build/test tooling and skip everything else.
var buildTestTokens = []string{
	"test", "build", "vet", "lint", "compile", "tsc",
	"cargo", "make", "gradle", "gradlew", "mvn",
	"pytest", "jest", "mocha", "vitest", "go run",
}

// looksLikeBuildOrTest reports whether a run command is plausibly a build/compile/test
// invocation worth a mechanical verdict.
func looksLikeBuildOrTest(cmd string) bool {
	l := strings.ToLower(cmd)
	for _, tok := range buildTestTokens {
		if strings.Contains(l, tok) {
			return true
		}
	}
	return false
}
