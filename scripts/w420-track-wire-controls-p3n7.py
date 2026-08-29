#!/usr/bin/env python3
"""Positive controls for the W4.20 Track wire-contract guards.

WHY THIS EXISTS, IN ONE SENTENCE: the defect these guards catch shipped because two tests
agreed with the caller instead of with the server, so a control campaign that only proves
"the new test goes red" would be repeating the original mistake one level up. C2 is the
answer to that — it makes the new fake PERMISSIVE and requires that the fake's own control
notices, which is the only thing separating a strict fake from another agreeable one.

PREDICTED BEFORE THE RUN, disjoint catchers:

  C1  client posts `content` again          -> the strict-fake test AND both corrected
                                               accomplices (they now assert `body`)
  C2  the strict fake stops disallowing     -> ONLY the fake's own refusal control.
      unknown fields                           ⚠ THIS CONTROL FOUND A REAL HOLE IN THE GUARD ON
                                               ITS FIRST RUN AND THAT IS WHY IT IS FIRST-CLASS:
                                               with the strictness removed the fake STILL answered
                                               400 — for the other reason, an empty Body — so the
                                               refusal control passed while the property it checks
                                               was gone. It now asserts the 400 NAMES an unknown
                                               field. A control that cannot fail was caught by
                                               running it, which is the entire argument for
                                               running them.
  C3  client sends author_id again          -> ONLY the two author_id assertions.
                                               ⚠ PREDICTED WRONG FIRST TIME: I expected the
                                               strict fake to catch it too. It does not, and
                                               should not — author_id IS a field Track declares,
                                               so a strict decode accepts it. What is wrong about
                                               sending it is semantic (the server overwrites it),
                                               and only an assertion about intent can see that.
  C4  GetIssue's path shape changes         -> the characterisation test AND the pre-existing
                                               GetIssue_DecodesPayload. ⚠ ALSO PREDICTED WRONG:
                                               I forgot the older test asserts the path too.
                                               Two guards on one path is redundancy, not a fault.

DISCIPLINE: refuses on an already-dirty target, refuses if anything is not green first,
asserts every mutation changed bytes, restores in a `finally`, sha256-verifies, re-runs green.
"""
from __future__ import annotations

import hashlib
import signal
import os
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
AGENT = ROOT / "agent"
CLIENT = AGENT / "internal" / "track" / "client.go"
WIRE = AGENT / "internal" / "track" / "wire_contract_track_test.go"
TARGETS = [CLIENT, WIRE]

PKGS = ["./internal/track/", "./cmd/agent/"]

STRICT = "AddComment_BodyIsAcceptedByATrackShapedServer"
REFUSES = "TrackLikeServer_RejectsTheKeyThatWasBeingSent"
# ⚠ `PINNED = "GetIssue_SendsTheIdentifierWhereTrackReadsAnID"` STOOD HERE AND THAT TEST DOES NOT
# EXIST. It was introduced by eb9414a (#57) and removed when GetIssue grew its by-identifier
# fallback; nothing noticed, because C4's ANCHOR had drifted in the same refactor and the control
# aborted before its expected-catcher set was ever compared. Two independent rots, and the louder
# one hid the quieter one: a `want` set naming a test that no longer exists can NEVER be satisfied,
# so that control could not have reported `ok` even with a perfect anchor.
#
# Re-derived by RUNNING the mutation and reading which tests actually red, not by guessing which
# name replaced the old one. Subtests included, because this harness matches the failing set
# EXACTLY and a subtest that reds is part of what the mutation costs.
GETISSUE_CATCHERS = {
    "GetIssue_ARowIDStillResolvesOnTheFirstRequest",
    "GetIssue_DecodesPayload",
    "GetIssue_ResolvesABranchDerivedIdentifier",
    "WireContract_MethodAndPath",
    "WireContract_MethodAndPath/GetIssue_by_row_id",
    "WireContract_MethodAndPath/GetIssue_by_human_key_falls_through_to_by-identifier",
}
OLD_UNIT = "AddComment_PostsToCorrectEndpoint"
OLD_E2E = "Run_AgentPostsTrackCommentAfterSuccess"




def restore_on_signal(snapshot: dict) -> None:
    """Put every snapshotted file back, then die of the signal we were sent.

    A `finally` DOES NOT RUN ON SIGTERM, so without this a command timeout landing mid-control
    leaves the mutation in the working tree — with a green suite and a `git status` showing only
    files the session edited on purpose. That is not hypothetical: talyvor-suite W1.7 (78c69c8)
    lost a shell gate to exactly this, and W1.7.3 (5de27e3) reproduced it on demand.

    Re-raising with SIG_DFL keeps the exit status honest: a caller that killed this process still
    sees it die of that signal, not exit 0 with a tidy tree. SIGKILL still strands, and nothing in
    Python can change that.

    Deliberately pasted rather than imported: scripts/check-restore-signal-handlers.py detects the
    handler in this file's OWN ast, and an import is invisible to it.
    """

    def handler(signum, _frame):
        for path, blob in snapshot.items():
            try:
                path.write_bytes(blob)
            except OSError:
                pass
        sys.stderr.write(
            "\n!! signal %d — restored %d mutated file(s) before exiting\n"
            % (signum, len(snapshot))
        )
        signal.signal(signum, signal.SIG_DFL)
        os.kill(os.getpid(), signum)

    for s in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        signal.signal(s, handler)

def sha(p: Path) -> str:
    return hashlib.sha256(p.read_bytes()).hexdigest()


def run() -> str:
    out = []
    for pkg in PKGS:
        r = subprocess.run(["go", "test", "-count=1", "-v", pkg],
                           cwd=AGENT, capture_output=True, text=True)
        out.append(r.stdout + r.stderr)
    return "\n".join(out)


def failed(out: str) -> set[str]:
    """Names from `--- FAIL: TestName (0.00s)`, minus the Test prefix.

    ⚠ Refuses if the marker is present and nothing parses: an empty set would read as
    NOT-CAUGHT for defects that were all caught.
    """
    names = set()
    for ln in out.splitlines():
        ln = ln.strip()
        if ln.startswith("--- FAIL:"):
            n = ln[len("--- FAIL:"):].strip().split()[0]
            names.add(n[len("Test"):] if n.startswith("Test") else n)
    if not names and "--- FAIL:" in out:
        raise SystemExit("REFUSE: '--- FAIL:' present but no name parsed — the classifier is "
                         "broken, not the tree.")
    return names


def mutate(path: Path, old: str, new: str) -> str:
    before = path.read_text(encoding="utf-8")
    if before.count(old) != 1:
        raise SystemExit(f"REFUSE: anchor in {path.name} occurs {before.count(old)}x, want 1 — "
                         "it has drifted; scoring now would report NOT-CAUGHT for a defect never "
                         "introduced.")
    path.write_text(before.replace(old, new, 1), encoding="utf-8")
    return before


CONTROLS = [
    ("C1 client posts `content` again", CLIENT,
     'json.Marshal(map[string]string{"body": comment})',
     'json.Marshal(map[string]string{"content": comment})',
     {STRICT, OLD_UNIT, OLD_E2E}),

    ("C2 the strict fake stops being strict", WIRE,
     "\t\tdec.DisallowUnknownFields() // ← what talyvor-track's httpx.DecodeJSON does",
     "\t\t_ = dec",
     {REFUSES}),

    ("C3 client sends author_id again", CLIENT,
     'json.Marshal(map[string]string{"body": comment})',
     'json.Marshal(map[string]string{"body": comment, "author_id": "talyvor-agent"})',
     {OLD_UNIT, OLD_E2E}),

    # ⚠ C4's ANCHOR HAD DRIFTED AND THE CONTROL COULD NOT RUN. It was written against a GetIssue
    # that built its URL in ONE expression; the function was since refactored to compute a `base`
    # and try two paths off it (`/issues/{id}`, then `/issues/by-identifier/{identifier}`), so the
    # old literal occurred 0x. `mutate()` refused correctly and exited 1 — this was never a silent
    # green — but the campaign then aborted with three of four controls run and no summary line.
    # Re-anchored on what the file says TODAY, and re-verified to CATCH rather than merely to
    # APPLY: an anchor that matches but mutates something inert reports NOT-CAUGHT for a defect
    # never introduced, which is the failure the refusal exists to prevent.
    ("C4 GetIssue's path shape changes", CLIENT,
     'base := c.url + "/v1/workspaces/" + url.PathEscape(workspaceID) + "/issues/"',
     'base := c.url + "/v1/workspaces/" + url.PathEscape(workspaceID) + "/issue/"',
     GETISSUE_CATCHERS),
]


# ─── --check-anchors: the cheap half, and the ONLY half CI can afford to run ───────────────
#
# ⚠ WHY THIS EXISTS. This campaign rotted in TWO independent ways and neither was visible until
# somebody ran it by hand and read the output. (1) C4's ANCHOR drifted when GetIssue was refactored,
# so `mutate()` refused — loudly, exit 1, but to nobody, because nothing in CI runs this script.
# (2) C4's expected-catcher set named `GetIssue_SendsTheIdentifierWhereTrackReadsAnID`, A TEST THAT
# NO LONGER EXISTS — and that rot was HIDDEN BY THE FIRST, because the run aborted before the set
# was ever compared. A `want` set naming a test that is gone can never be satisfied, so the control
# could not have reported `ok` even with a perfect anchor.
#
# ⚠⚠ SO THIS MODE CHECKS BOTH, and that is the difference from the sibling implementations in
# talyvor-docs (#225) and talyvor-track (#218), which check anchors only. A campaign can go inert
# from either end: the thing it mutates, or the thing it expects to break.
#
# The full campaign cannot go in CI — it mutates tracked files and runs the suite once per control.
# This half is pure string work over the tree: no mutation, no `go test`, milliseconds.
#
# ⚠ WHAT IT DOES NOT CLAIM. It proves each control can still be APPLIED and that the names it
# expects still exist — NOT that the mutation still CATCHES them. A control whose anchor matches but
# whose mutation has become inert passes here; the campaign is what proves the rest.
# ⚠ AND ONE STATED LIMIT: a SUBTEST leaf (`Parent/Sub`) is not statically checkable — Go derives it
# from a `t.Run` string with spaces turned into underscores, so it does not appear verbatim in the
# source. Only the parent is verified. The campaign catches a renamed subtest by reporting BAD.
ANCHOR_FLOOR = 4  # controls checked. A loop over an empty list checks nothing.


def check_anchors(ci_mode: bool) -> int:
    bad = []
    if len(CONTROLS) < ANCHOR_FLOOR:
        bad.append("FLOOR: only %d controls to check, floor is %d — a loop over a shrunken list "
                   "reports clean anchors rather than a missing campaign. If controls were "
                   "deleted, lower the floor in the same diff." % (len(CONTROLS), ANCHOR_FLOOR))

    sources = "\n".join(f.read_text(encoding="utf-8")
                        for f in (ROOT / "agent").rglob("*_test.go"))
    if "func Test" not in sources:
        bad.append("FLOOR: no Go test sources were read, so every name below would verify for "
                   "free. The walk is broken, not the tree.")

    for name, path, old, _new, want in CONTROLS:
        n = path.read_text(encoding="utf-8").count(old)
        print("  %-42s anchor %s (%dx, want 1)" % (name, "ok" if n == 1 else "STALE", n))
        if n != 1:
            bad.append(
                "%s: its anchor occurs %dx in %s, want 1 — this control CANNOT RUN. It is not a "
                "failing guard, it is an ABSENT one. Re-anchor it on what the file says today, and "
                "re-verify it CATCHES rather than merely APPLIES."
                % (name, n, path.name))
        for t in sorted(want):
            parent = t.split("/", 1)[0]
            ok = ("func Test" + parent + "(") in sources
            print("       expects %-64s %s" % (t, "ok" if ok else "NO SUCH TEST"))
            if not ok:
                bad.append(
                    "%s expects test %r and no `func Test%s(` exists in agent/. A `want` set naming "
                    "a test that is gone can NEVER be satisfied — the control is permanently BAD, "
                    "and if its anchor is also stale the abort hides it entirely. That is exactly "
                    "how this campaign rotted." % (name, t, parent))

    if bad:
        for b in bad:
            print(("::error::" if ci_mode else "") + b)
        return 1
    print("anchor check: all %d controls still apply and every expected test exists" % len(CONTROLS))
    return 0


def main() -> int:
    # Installed BEFORE the first mutation: every control restores to these pristine bytes, so a
    # signal at any point puts back the right content.
    restore_on_signal({p: p.read_bytes() for p in TARGETS})
    dirty = subprocess.run(["git", "status", "--porcelain", "--"] + [str(p) for p in TARGETS],
                           cwd=ROOT, capture_output=True, text=True).stdout.strip()
    if dirty:
        print("REFUSE: a target file is already modified:\n" + dirty)
        return 3
    base = {p: sha(p) for p in TARGETS}

    out = run()
    if failed(out):
        print(f"REFUSE: not green on the untouched tree: {sorted(failed(out))}")
        return 3
    print("clean tree: internal/track + cmd/agent GREEN\n")

    bad = 0
    for name, path, old, new, want in CONTROLS:
        original = None
        try:
            original = mutate(path, old, new)
            got = failed(run())
            ok = got == want
            bad += 0 if ok else 1
            print(f"  [{'ok ' if ok else 'BAD'}] {name:<40} -> RED: {', '.join(sorted(got)) or '(none)'}")
            if not ok:
                print(f"      expected exactly: {', '.join(sorted(want))}")
        finally:
            if original is not None:
                path.write_text(original, encoding="utf-8")

    for p, want in base.items():
        if sha(p) != want:
            print(f"\nBAD: {p.name} not restored")
            bad += 1
    if failed(run()):
        print("\nBAD: not green again after restore")
        bad += 1

    print(f"\n{len(CONTROLS) - bad} of {len(CONTROLS)} controls behaved as predicted; "
          "targets restored and sha256-verified.")
    return 1 if bad else 0


if __name__ == "__main__":
    if "--check-anchors" in sys.argv:
        sys.exit(check_anchors("--ci" in sys.argv))
    sys.exit(main())
