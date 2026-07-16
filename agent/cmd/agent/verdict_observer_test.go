package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/talyvor/code/internal/agentloop"
)

// fakeReporter captures ReportMechanicalVerdict calls (satisfies verdictReporter) so the
// pairing rule can be proven without any network.
type reportCall struct {
	outputID string
	verdict  string
	tool     string
	exit     int
}
type fakeReporter struct{ calls []reportCall }

func (f *fakeReporter) ReportMechanicalVerdict(_ context.Context, outputID, verdict string, exitCode int, tool, reason string) error {
	f.calls = append(f.calls, reportCall{outputID, verdict, tool, exitCode})
	return nil
}

func editStep(oid string) agentloop.StepInfo {
	return agentloop.StepInfo{Tool: "edit_file", OutputID: oid}
}
func editStepErr(oid string) agentloop.StepInfo {
	return agentloop.StepInfo{Tool: "edit_file", OutputID: oid, ToolErr: true}
}
func runStepInfo(cmd string, exit int) agentloop.StepInfo {
	args, _ := json.Marshal(map[string]string{"cmd": cmd})
	return agentloop.StepInfo{Tool: "run", Args: args, Ran: true, RunExit: exit}
}

// feed runs a sequence of StepInfos through a fresh verdictObserver + fakeReporter.
func feed(steps ...agentloop.StepInfo) *fakeReporter {
	rep := &fakeReporter{}
	obs := newVerdictObserver(context.Background(), rep, nil)
	for _, s := range steps {
		obs.ObserveStep(s)
	}
	return rep
}

// (1) Clean 1:1 — one edit then a failing test run → exactly one verdict for that edit's
// output_id, with the correct tests_failed classification + exit code.
func TestPairing_CleanOneToOne(t *testing.T) {
	rep := feed(editStep("oid-A"), runStepInfo("go test ./...", 1))
	if len(rep.calls) != 1 {
		t.Fatalf("expected exactly 1 verdict; got %d: %+v", len(rep.calls), rep.calls)
	}
	c := rep.calls[0]
	if c.outputID != "oid-A" {
		t.Errorf("verdict attributed to %q, want oid-A", c.outputID)
	}
	if c.verdict != "tests_failed" || c.exit != 1 {
		t.Errorf("verdict=%q exit=%d, want tests_failed/1", c.verdict, c.exit)
	}
}

// (2) 1:many batch → SKIP. Two edits then a passing run is NOT soundly attributable to
// one generation — report nothing (the load-bearing moat-integrity property).
func TestPairing_MultiEditBatch_Skips(t *testing.T) {
	rep := feed(editStep("oid-A"), editStep("oid-B"), runStepInfo("go test ./...", 0))
	if len(rep.calls) != 0 {
		t.Fatalf("a 1:many edit batch must report NO verdict (unattributable); got %+v", rep.calls)
	}
}

// (3) A non-build/test run is ignored and does NOT consume the pending edit; the next
// real build attributes 1:1.
func TestPairing_NonBuildRun_Ignored(t *testing.T) {
	rep := feed(editStep("oid-A"), runStepInfo("ls -la", 0), runStepInfo("go build ./...", 0))
	if len(rep.calls) != 1 || rep.calls[0].outputID != "oid-A" || rep.calls[0].verdict != "compiled" {
		t.Fatalf("non-build run must be ignored; the build must attribute 1:1 to oid-A/compiled; got %+v", rep.calls)
	}
}

// (4) A build/test run before any edit reports nothing.
func TestPairing_RunBeforeEdit_Nothing(t *testing.T) {
	if rep := feed(runStepInfo("go test ./...", 0)); len(rep.calls) != 0 {
		t.Fatalf("a run with no preceding edit must report nothing; got %+v", rep.calls)
	}
}

// (5) No double-report: a second build with no edit between reports nothing (the pending
// set was cleared by the first).
func TestPairing_NoDoubleReport(t *testing.T) {
	rep := feed(editStep("oid-A"), runStepInfo("go test ./...", 0), runStepInfo("go test ./...", 0))
	if len(rep.calls) != 1 || rep.calls[0].outputID != "oid-A" {
		t.Fatalf("must report exactly once for oid-A; got %+v", rep.calls)
	}
}

// (6) Unknown (empty) output_id → skip: an edit whose generation had no output_id can't
// be attributed.
func TestPairing_UnknownOutputId_Skips(t *testing.T) {
	if rep := feed(editStep(""), runStepInfo("go test ./...", 0)); len(rep.calls) != 0 {
		t.Fatalf("an edit with an unknown output_id must not be attributed; got %+v", rep.calls)
	}
}

// (7) A FAILED edit (tool error) produced no code and must not be tracked/attributed.
func TestPairing_FailedEdit_NotTracked(t *testing.T) {
	if rep := feed(editStepErr("oid-A"), runStepInfo("go test ./...", 0)); len(rep.calls) != 0 {
		t.Fatalf("a failed edit_file must not be attributed a verdict; got %+v", rep.calls)
	}
}

// (8) Multi-cycle: edit→run→edit→run each pairs 1:1 (full coverage of the common pattern).
func TestPairing_MultiCycle_EachOneToOne(t *testing.T) {
	rep := feed(
		editStep("oid-A"), runStepInfo("go test ./...", 1),
		editStep("oid-B"), runStepInfo("go test ./...", 0),
	)
	if len(rep.calls) != 2 {
		t.Fatalf("expected 2 verdicts (one per cycle); got %+v", rep.calls)
	}
	if rep.calls[0].outputID != "oid-A" || rep.calls[0].verdict != "tests_failed" {
		t.Errorf("cycle 1: want oid-A/tests_failed; got %+v", rep.calls[0])
	}
	if rep.calls[1].outputID != "oid-B" || rep.calls[1].verdict != "tests_passed" {
		t.Errorf("cycle 2: want oid-B/tests_passed; got %+v", rep.calls[1])
	}
}
