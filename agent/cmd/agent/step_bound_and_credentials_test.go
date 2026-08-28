package main

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// TWO OF W4.42's UNPINNED ROWS, CLOSED. Both are values whose SHIPPED form was
// defended by nothing, and neither needed a new enforcement test — the
// mechanisms behind them are already covered, which was checked rather than
// assumed.
//
// ── 1. THE AGENT LOOP'S STEP BOUND EXISTS AS FOUR COPIES OF 20 ──────────────
//
// -max-steps caps how many tool-call turns the ITERATIVE agent may take — a
// loop that edits files and runs commands. The number 20 is written down four
// times, in two packages, and nothing compared them:
//
//	cmd/agent/main.go       the flag default                     20
//	cmd/agent/main.go       that flag's usage text, shown to users "(default 20)"
//	internal/agentloop      New()'s fallback for MaxSteps <= 0    20
//	internal/agentloop      Config.MaxSteps's doc comment         "(default 20)"
//
// The library fallback is not decoration: it is what applies to ANY caller that
// builds an Agent without setting MaxSteps, so a divergence means the CLI and
// an embedder run different bounds while both call it "the default".
//
// ⚠ ENFORCEMENT IS ALREADY TESTED AND THIS DOES NOT DUPLICATE IT — CHECKED, NOT
// ASSUMED: internal/agentloop's TestLoop_StopsOnBudget drives a model that never
// finishes and asserts the loop stops at MaxSteps. It passes an explicit 4,
// which is correct for testing the mechanism and is exactly why it cannot notice
// the shipped 20 changing.
//
// ── 2. NO CREDENTIAL FLAG MAY SHIP A NON-EMPTY DEFAULT ──────────────────────
//
// -lens-key, -track-key and -docs-key all default to "". That emptiness is
// load-bearing: every client's IsConfigured() is `url != "" && apiKey != ""`,
// and the whole estate treats "unconfigured" as a first-class state — commands
// degrade to best-effort rather than failing. A shipped default credential
// would make IsConfigured() answer TRUE with a key nobody set, so the CLI would
// attempt authenticated calls and collect 401s instead of reporting that it is
// not configured.
//
// ⚠ AGAIN, THE MECHANISM IS ALREADY TESTED: internal/track and internal/docs
// both have TestIsConfigured covering the empty case. What nothing defended was
// the INPUT to it — the flag defaults.
//
// This is a property over a class, not a pin on three literals: any future
// credential flag is covered by construction, and the floor below fails if the
// population is ever empty.

func mainSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}
	return string(b)
}

func loopSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../internal/agentloop/loop.go")
	if err != nil {
		t.Fatalf("reading internal/agentloop/loop.go: %v", err)
	}
	return string(b)
}

var (
	maxStepsFlagRe = regexp.MustCompile(`fs\.IntVar\(&maxSteps,\s*"max-steps",\s*(\d+),\s*"([^"]*)"`)
	loopFallbackRe = regexp.MustCompile(`cfg\.MaxSteps <= 0 \{\s*\n\s*cfg\.MaxSteps = (\d+)`)
	loopDocRe      = regexp.MustCompile(`MaxSteps\s+int\s*// hard cap on tool-call turns \(default (\d+)\)`)
	defaultInText  = regexp.MustCompile(`\(default (\d+)\)`)
)

func TestStepBound_AllFourCopiesAgree(t *testing.T) {
	m := maxStepsFlagRe.FindStringSubmatch(mainSource(t))
	if m == nil {
		t.Fatal("could not find the -max-steps flag registration in main.go — the parser is broken, " +
			"and a guard that finds nothing to compare is the defect it exists to catch")
	}
	flagDefault, _ := strconv.Atoi(m[1])
	usage := m[2]

	loop := loopSource(t)
	f := loopFallbackRe.FindStringSubmatch(loop)
	if f == nil {
		t.Fatal("could not find agentloop's `cfg.MaxSteps <= 0 -> N` fallback — either it is gone " +
			"(so an unset MaxSteps is now unbounded, which is worse than a divergence) or the " +
			"parser is broken")
	}
	fallback, _ := strconv.Atoi(f[1])

	if flagDefault != fallback {
		t.Errorf("-max-steps defaults to %d but internal/agentloop falls back to %d.\n"+
			"    The fallback is what applies to ANY caller that builds an Agent without setting "+
			"MaxSteps, so the CLI and an embedder would run different bounds on a loop that edits "+
			"files and runs commands, while both call it \"the default\".", flagDefault, fallback)
	}

	// The two PROSE copies, which are what a user and a reader actually see.
	if u := defaultInText.FindStringSubmatch(usage); u == nil {
		t.Errorf("the -max-steps usage text no longer states its default: %q", usage)
	} else if n, _ := strconv.Atoi(u[1]); n != flagDefault {
		t.Errorf("-max-steps defaults to %d but its usage text tells the user %d — the help output "+
			"would be wrong", flagDefault, n)
	}
	if d := loopDocRe.FindStringSubmatch(loop); d == nil {
		t.Errorf("Config.MaxSteps's doc comment no longer states a default in the expected shape")
	} else if n, _ := strconv.Atoi(d[1]); n != fallback {
		t.Errorf("agentloop falls back to %d but Config.MaxSteps's comment says %d", fallback, n)
	}
}

var credFlagRe = regexp.MustCompile(`fs\.StringVar\(&\w+,\s*"([a-z0-9-]*key[a-z0-9-]*)",\s*("(?:[^"\\]|\\.)*"),`)

func TestCredentialFlags_ShipNoDefault(t *testing.T) {
	m := credFlagRe.FindAllStringSubmatch(mainSource(t), -1)
	if len(m) == 0 {
		t.Fatal("found ZERO credential-shaped flags in main.go — the parser is broken, and a floor " +
			"that finds nothing to check cannot fail for the right reason")
	}
	for _, g := range m {
		name, def := g[1], g[2]
		if def != `""` {
			t.Errorf("-%s ships a default of %s.\n"+
				"    Every client's IsConfigured() is `url != \"\" && apiKey != \"\"`, and the estate "+
				"treats \"unconfigured\" as a first-class state that commands degrade around. A "+
				"shipped default credential makes IsConfigured() answer TRUE with a key nobody set, "+
				"so the CLI attempts authenticated calls and collects 401s instead of saying it is "+
				"not configured.", name, def)
		}
	}
}
