package agentloop

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE BOUND AT THE TOOL, not at the classifier. cmdguard has its own tests; these assert what the
// `run` TOOL does with each verdict — that an allowed command still runs, that a refused one does
// not execute, and that the absence of a human is a refusal rather than an approval.

func runToolWith(t *testing.T, confirm func(string, string) bool) (Tool, string) {
	t.Helper()
	dir := t.TempDir()
	return NewRunToolWithConfirm(dir, confirm), dir
}

func call(t *testing.T, tool Tool, cmd string) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"cmd": cmd})
	out, err := tool.Run(context.Background(), raw)
	if err != nil {
		t.Fatalf("run(%q): %v", cmd, err)
	}
	return out
}

// ⚠ THE POSITIVE CONTROL. Without this, a guard that refuses everything passes every other test in
// this file — and an agent that cannot run its own test suite is one nobody will leave enabled.
func TestRunTool_AllowedCommandStillRuns(t *testing.T) {
	tool, _ := runToolWith(t, nil) // no confirmer: an allowed command must not need one
	out := call(t, tool, "git status --porcelain")
	if strings.Contains(out, "NOT RUN") || strings.Contains(out, "REFUSED") {
		t.Fatalf("an allowlisted command did not run:\n%s", out)
	}
	if !strings.Contains(out, "exit ") {
		t.Fatalf("expected a captured exit code from a real execution, got:\n%s", out)
	}
}

// ⚠ THE ONE THAT MATTERS: the side effect must not happen. Asserting the message alone would pass
// on a guard that prints a refusal and runs the command anyway.
func TestRunTool_RefusedCommandDoesNotExecute(t *testing.T) {
	tool, dir := runToolWith(t, nil)
	marker := filepath.Join(dir, "SHOULD-NOT-EXIST")
	out := call(t, tool, "go test $(touch "+marker+")")

	if !strings.Contains(out, "REFUSED") {
		t.Errorf("expected a refusal, got:\n%s", out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the refused command RAN — the file it creates exists. The message was cosmetic.")
	}
}

// ⚠ NO TTY IS A REFUSAL, NEVER AN AUTO-APPROVAL, and the output has to SAY so — "my command
// silently did nothing" is the report that follows if it does not.
func TestRunTool_NoConfirmerRefusesAndSaysWhy(t *testing.T) {
	tool, dir := runToolWith(t, nil)
	marker := filepath.Join(dir, "NOPE")
	out := call(t, tool, "touch "+marker)

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a non-allowlisted command ran with no human present")
	}
	if !strings.Contains(out, "NOT RUN") {
		t.Errorf("output does not say the command was not run:\n%s", out)
	}
	if !strings.Contains(out, "no interactive terminal") {
		t.Errorf("output does not explain that the absence of a terminal is why:\n%s", out)
	}
}

// A human saying yes must actually run it — the confirmation is a gate, not a decoration.
func TestRunTool_ConfirmedCommandRuns(t *testing.T) {
	var sawCmd, sawReason string
	tool, dir := runToolWith(t, func(cmd, reason string) bool {
		sawCmd, sawReason = cmd, reason
		return true
	})
	marker := filepath.Join(dir, "created")
	call(t, tool, "touch "+marker)

	if _, err := os.Stat(marker); err != nil {
		t.Fatal("a confirmed command did not run — the confirmation leads nowhere")
	}
	// ⚠ THE PROMPT MUST SHOW THE EXACT COMMAND. Approving "a shell command" is not consent.
	if !strings.Contains(sawCmd, marker) {
		t.Errorf("the confirmation was not shown the exact command, got %q", sawCmd)
	}
	if sawReason == "" {
		t.Error("the confirmation was given no reason, so it could not say what was unusual")
	}
}

// And declining must leave the world untouched.
func TestRunTool_DeclinedCommandDoesNotRun(t *testing.T) {
	tool, dir := runToolWith(t, func(string, string) bool { return false })
	marker := filepath.Join(dir, "declined")
	out := call(t, tool, "touch "+marker)

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a DECLINED command ran")
	}
	if !strings.Contains(out, "declined by the user") {
		t.Errorf("output does not record the decline:\n%s", out)
	}
}

// ⚠ THE PREFIX-MATCH TRAP, AT THE TOOL. Starts with an allowed verb; must still stop.
func TestRunTool_AllowedVerbThenSomethingElseIsNotAutoRun(t *testing.T) {
	tool, dir := runToolWith(t, nil)
	marker := filepath.Join(dir, "chained")
	out := call(t, tool, "git status && touch "+marker)

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("`git status && touch X` ran unattended — the guard prefix-matched")
	}
	if !strings.Contains(out, "NOT RUN") {
		t.Errorf("expected the chained command to be withheld:\n%s", out)
	}
}
