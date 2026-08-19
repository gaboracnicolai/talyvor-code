#!/usr/bin/env python3
"""W4.12 — positive controls for the MCP workspace-confinement wiring.

Every control names its PREDICTED catcher before it runs, mutates exactly one thing, runs the
FULL agent suite (so "only the new guard reds" is MEASURED rather than assumed), and restores
from a pristine copy in a finally. The tree's sha256 is verified identical at exit.

Each mutation is derived from the ORIGINAL file text every time. #148's harness planned every
edit against the text as first read, so two edits to one file meant the second write erased the
first and three controls scored VOID; deriving from the original each round closes that.

A control that fails to COMPILE is VOID, not CAUGHT — a build error is not a caught mutation
(the trap W4.11's C3 fell into).
"""

import hashlib
import pathlib
import re
import subprocess
import sys

AGENT = pathlib.Path(__file__).resolve().parent.parent / "agent"
SERVER = AGENT / "internal" / "mcp" / "server.go"
NEWTEST = AGENT / "internal" / "mcp" / "confinement_test.go"

# ─── the four wirings this merge adds, as (label, find, replace) on the ORIGINAL text ───

ASK_CONFINED = '''	safe, cerr := s.confinedReadPaths(files)
	if cerr != nil {
		return nil, rpcErrInvalidParam, "ask_code: path outside workspace"
	}
	fileCtx := ""
	if len(safe) > 0 {
		out, err := codebase.ReadFilesForContext(safe, codebase.DefaultMaxTotalBytes)'''
ASK_RAW = '''	fileCtx := ""
	if len(files) > 0 {
		out, err := codebase.ReadFilesForContext(files, codebase.DefaultMaxTotalBytes)'''

GEN_CONFINED = '''	safe, cerr := s.confinedReadPath(a.File)
	if cerr != nil {
		return nil, rpcErrInvalidParam, "generate_tests: path outside workspace"
	}
	body, err := codebase.ReadFile(safe, codebase.DefaultMaxFileBytes)'''
GEN_RAW = '''	body, err := codebase.ReadFile(a.File, codebase.DefaultMaxFileBytes)'''

REV_CONFINED = '''	safe, cerr := s.confinedReadPaths(a.Files)
	if cerr != nil {
		return nil, rpcErrInvalidParam, "review_code: path outside workspace"
	}
	body, _ := codebase.ReadFilesForContext(safe, codebase.DefaultMaxTotalBytes)'''
REV_RAW = '''	body, _ := codebase.ReadFilesForContext(a.Files, codebase.DefaultMaxTotalBytes)'''

BATCH_FAILCLOSED = '''		abs, err := s.confinedReadPath(p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}'''
BATCH_DROPS = '''		abs, err := s.confinedReadPath(p)
		if err != nil {
			continue
		}'''

BOUNDARY = '''	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {'''
BOUNDARY_BLIND = '''	if false && (err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))) {'''
BOUNDARY_ALWAYS = '''	if true || err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {'''

# ⚠ THREE OF THESE SEVEN PREDICTIONS WERE WRONG ON THE FIRST RUN. The lists below are the
# MEASURED catchers; each wrong one is recorded at its control with the reason, because a
# prediction quietly re-fitted to the result is not a prediction.
CONTROLS = [
    # ⚠ PREDICTED [RefusesFileOutsideRoot] ALONE — WRONG, under-listed. Reverting ask_code also
    # reds the must-stay-green companion, and NOT for the reason the escape cases fire: a
    # RELATIVE in-root path ("in.go") then resolves against the serve process's cwd, misses,
    # and ReadFilesForContext turns that into an "[error reading …]" NOTE inside the prompt
    # while returning nil. The RPC still succeeds. Only the assertion that the file's CONTENT
    # reached Lens can see it — so ask_code today answers confidently over an error string.
    ("C1  ask_code reverted to the raw caller path",
     [(ASK_CONFINED, ASK_RAW)], False,
     ["TestAskCode_RefusesFileOutsideRoot", "TestConfinedTools_InRootStillWork"]),

    ("C2  generate_tests reverted to the raw caller path",
     [(GEN_CONFINED, GEN_RAW)], False,
     ["TestGenerateTests_RefusesFileOutsideRoot", "TestConfinedTools_InRootStillWork"]),

    # ⚠ PREDICTED [RefusesFileOutsideRoot] ALONE — WRONG the same way as C1, and this is the
    # worst of the three: review_code returns a review with critical_count/warning_count over a
    # prompt whose "file" is the text of a read error. generate_tests at least fails LOUDLY
    # ("read source: no such file"); these two fail silently.
    ("C3  review_code reverted to the raw caller path",
     [(REV_CONFINED, REV_RAW)], False,
     ["TestReviewCode_RefusesFileOutsideRoot", "TestConfinedTools_InRootStillWork"]),

    ("C4  the batch DROPS the offending path instead of refusing (the attractive near-fix)",
     [(BATCH_FAILCLOSED, BATCH_DROPS)], False,
     ["TestReviewCode_RefusesFileOutsideRoot", "TestAskCode_RefusesFileOutsideRoot"]),

    ("C5  the boundary BLINDED — confinedReadPath resolves but never refuses",
     [(BOUNDARY, BOUNDARY_BLIND)], False,
     ["TestAskCode_RefusesFileOutsideRoot", "TestGenerateTests_RefusesFileOutsideRoot",
      "TestReviewCode_RefusesFileOutsideRoot", "TestReadFile_RefusesOutsideRoot"]),

    # ⚠ PREDICTED WRONG IN BOTH DIRECTIONS, and the miss is the most useful measurement here.
    # I predicted TestAskCode_CallsLensWithFileContext would red — it passes an ABSOLUTE path
    # outside any root and asserts the content reaches Lens. It stayed GREEN, because
    # newServerForTest never SetRoot()s: s.root == "" returns the path unchanged BEFORE the
    # mutated boundary is reached. That pre-existing fixture is structurally blind to
    # confinement for every tool — which is the fixture-level reason three lanes could stay
    # unconfined under a green suite. The unlisted catcher, AutoDiscoversViaSemanticIndex, does
    # build an index and therefore does have a root.
    ("C6  the boundary refuses EVERYTHING (must-stay-green companion)",
     [(BOUNDARY, BOUNDARY_ALWAYS)], False,
     ["TestConfinedTools_InRootStillWork", "TestAskCode_AutoDiscoveredFilesStayInRoot",
      "TestReadFile_RefusesOutsideRoot", "TestAskCode_AutoDiscoversViaSemanticIndex"]),

    # THE MEASURED BLINDNESS. The defect restored on all three lanes AND the new test file
    # deleted — i.e. exactly main. Predicted NOT CAUGHT: nothing else in this repo can see it.
    ("C7  all three lanes reverted AND the new test file removed (= main)",
     [(ASK_CONFINED, ASK_RAW), (GEN_CONFINED, GEN_RAW), (REV_CONFINED, REV_RAW)], True,
     []),
]


def sha_tree() -> str:
    h = hashlib.sha256()
    for p in sorted(AGENT.rglob("*.go")):
        h.update(p.relative_to(AGENT).as_posix().encode())
        h.update(p.read_bytes())
    return h.hexdigest()


def run_suite() -> tuple[bool, set[str], str]:
    """Returns (built_ok, failing_test_names, raw_output)."""
    r = subprocess.run(["go", "test", "-count=1", "./..."],
                       cwd=AGENT, capture_output=True, text=True)
    out = r.stdout + r.stderr
    # A compile/vet failure is VOID, not a catch.
    built = not re.search(r"\[build failed\]|cannot use|undefined:|declared and not used|syntax error", out)
    fails = set(re.findall(r"^--- FAIL: (\w+)", out, re.M))
    return built, fails, out


def main() -> int:
    original = SERVER.read_text()
    test_src = NEWTEST.read_text()
    baseline_sha = sha_tree()

    print(f"tree sha256 at start: {baseline_sha[:16]}…\n")
    built, fails, _ = run_suite()
    if not built or fails:
        print(f"U0 baseline is NOT clean — built={built} fails={sorted(fails)}")
        return 1
    print("U0 baseline: full agent suite GREEN, no mutation\n")

    bad = 0
    try:
        for label, edits, drop_test, predicted in CONTROLS:
            print("─" * 78)
            print(label)
            print(f"  PREDICTED catchers: {sorted(predicted) if predicted else 'NONE (must stay green)'}")

            mutated = original
            for find, repl in edits:
                if find not in mutated:
                    print("  !! anchor not found — control VOID")
                    bad += 1
                    mutated = None
                    break
                mutated = mutated.replace(find, repl, 1)
            if mutated is None:
                continue

            SERVER.write_text(mutated)
            if drop_test:
                NEWTEST.unlink()

            built, fails, out = run_suite()
            if drop_test:
                NEWTEST.write_text(test_src)
            SERVER.write_text(original)

            if not built:
                print("  !! DID NOT BUILD — VOID, not a catch")
                print("     " + "\n     ".join(out.splitlines()[:6]))
                bad += 1
                continue

            got = sorted(fails)
            print(f"  ACTUAL catchers:    {got if got else 'NONE — green'}")
            if set(predicted) == set(fails):
                print("  ✅ prediction matched")
            else:
                missed = set(predicted) - fails
                extra = fails - set(predicted)
                print(f"  ⚠ PREDICTION WRONG — missed={sorted(missed)} unlisted={sorted(extra)}")
                bad += 1
    finally:
        SERVER.write_text(original)
        if not NEWTEST.exists():
            NEWTEST.write_text(test_src)

    end_sha = sha_tree()
    print("─" * 78)
    print(f"tree sha256 at exit:  {end_sha[:16]}…  {'IDENTICAL' if end_sha == baseline_sha else '!! DRIFTED'}")
    if end_sha != baseline_sha:
        return 1
    print(f"\n{len(CONTROLS) - bad}/{len(CONTROLS)} controls matched their prediction")
    return 0 if bad == 0 else 2


if __name__ == "__main__":
    sys.exit(main())
