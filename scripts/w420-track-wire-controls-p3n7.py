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
PINNED = "GetIssue_SendsTheIdentifierWhereTrackReadsAnID"
OLD_UNIT = "AddComment_PostsToCorrectEndpoint"
OLD_E2E = "Run_AgentPostsTrackCommentAfterSuccess"


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

    ("C4 GetIssue's path shape changes", CLIENT,
     'c.url+"/v1/workspaces/"+url.PathEscape(workspaceID)+"/issues/"+url.PathEscape(identifier), nil)',
     'c.url+"/v1/workspaces/"+url.PathEscape(workspaceID)+"/issue/"+url.PathEscape(identifier), nil)',
     {PINNED, "GetIssue_DecodesPayload"}),
]


def main() -> int:
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
    sys.exit(main())
