#!/usr/bin/env python3
"""W6.42 — controls for `w420-track-wire-controls-p3n7.py --check-anchors`.

The mode PASSED ON ITS FIRST RUN, which this repo's standing rule says to suspect, and it makes two
claims rather than one: every control's ANCHOR still applies, and every test it EXPECTS TO BREAK
still exists. Both halves get a mutation that forces them to fire, because they rot independently —
that is not a hypothesis, it is what happened here. C4 had both rots at once and the anchor one
aborted the run before the name one could ever be compared.

⚠ D5 IS INVERTED: an untouched tree must stay GREEN. A check that reds on correct work gets relaxed
until it reds on nothing.

⚠ D6 DEFENDS THE CI STEP'S PREMISE — `--check-anchors` is in CI only because it mutates nothing.

⚠ D4 IS THE VACUITY CONTROL FOR THE NAME HALF SPECIFICALLY, and it is the one most worth having:
the name check reads every `*_test.go` under agent/ into one string. If that walk ever returns
nothing, EVERY name verifies for free and the check reports a clean campaign over a tree it never
read — the exact shape of the defect it exists to catch.

Mutates tracked files, restores them in a `finally` with sha256 compared, and installs a signal
handler — a `finally` does not run on SIGTERM (scripts/check-restore-signal-handlers.py).
"""
import hashlib
import os
import pathlib
import signal
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
SCRIPT = ROOT / "scripts/w420-track-wire-controls-p3n7.py"
CLIENT = ROOT / "agent/internal/track/client.go"
CTEST = ROOT / "agent/internal/track/client_test.go"


def restore_on_signal(snapshot: dict) -> None:
    """Put every snapshotted file back, then die of the signal we were sent."""

    def handler(signum, _frame):
        for path, blob in snapshot.items():
            try:
                path.write_bytes(blob)
            except OSError:
                pass
        sys.stderr.write("\n!! signal %d — restored %d mutated file(s) before exiting\n"
                         % (signum, len(snapshot)))
        signal.signal(signum, signal.SIG_DFL)
        os.kill(os.getpid(), signum)

    for s in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        signal.signal(s, handler)


def sha(p: pathlib.Path) -> str:
    return hashlib.sha256(p.read_bytes()).hexdigest()


def check() -> tuple[bool, str]:
    r = subprocess.run([sys.executable, str(SCRIPT), "--check-anchors"],
                       cwd=ROOT, capture_output=True, text=True)
    return r.returncode == 0, r.stdout + r.stderr


CASES = [
    ("D1 an anchor drifts (the C4 rot, reproduced)", CLIENT,
     'base := c.url + "/v1/workspaces/" + url.PathEscape(workspaceID) + "/issues/"',
     'base := c.url + "/v1/workspaces/" + url.PathEscape(workspaceID) + "/issue-list/"',
     True, "the mutation target moved; the control cannot be applied"),

    # ⚠ D2'S FIRST DRAFT WAS VOID AND THE HARNESS REPORTED IT AS A BLIND GUARD. It appended a
    # SECOND line reading `_ = c.url + ...` — which is not the anchor, because the anchor begins
    # `base := `. The count stayed 1, `--check-anchors` correctly stayed green, and the scoreboard
    # accused a correct subject. Diagnosed by asking the PRODUCT, not the test: counting the anchor
    # in the mutated text gave 1, and duplicating the anchor ITSELF gives 2. A mutation with no
    # observable effect is VOID, not a catch — and the probe now duplicates the anchor verbatim.
    # (The result is not valid Go; it does not need to be. `--check-anchors` counts, it never
    # compiles, and D6 is what asserts it never writes.)
    ("D2 an anchor stops being unique", CLIENT,
     'base := c.url + "/v1/workspaces/" + url.PathEscape(workspaceID) + "/issues/"',
     'base := c.url + "/v1/workspaces/" + url.PathEscape(workspaceID) + "/issues/"\n\t'
     'base := c.url + "/v1/workspaces/" + url.PathEscape(workspaceID) + "/issues/"',
     True, "2x is as unusable as 0x — the control would mutate an arbitrary one"),

    ("D3 an EXPECTED TEST is renamed away (the quieter C4 rot)", CTEST,
     "func TestGetIssue_DecodesPayload(", "func TestGetIssue_DecodesPayloadRenamed(",
     True, "a `want` set naming a test that is gone can never be satisfied"),

    ("D4 VACUITY: the name check reads no sources", SCRIPT,
     'sources = "\\n".join(f.read_text(encoding="utf-8")\n                        for f in (ROOT / "agent").rglob("*_test.go"))',
     'sources = ""',
     True, "if the walk returns nothing every name verifies for free"),

    ("D5 INVERTED: an untouched tree must stay green", CLIENT,
     'base := c.url + "/v1/workspaces/" + url.PathEscape(workspaceID) + "/issues/"',
     'base := c.url + "/v1/workspaces/" + url.PathEscape(workspaceID) + "/issues/"',
     False, "a check that reds on correct work gets relaxed until it reds on nothing"),
]


def main() -> int:
    originals = {p: p.read_bytes() for p in (SCRIPT, CLIENT, CTEST)}
    hashes = {p: sha(p) for p in originals}
    restore_on_signal(originals)

    ok, out = check()
    if not ok:
        print("BASELINE IS NOT GREEN — every verdict below would be unreadable:\n" + out)
        return 2
    print("baseline: GREEN\n")

    results = []
    try:
        for name, path, find, repl, predict_red, why in CASES:
            src = originals[path].decode()
            if src.count(find) != 1:
                results.append((name, "CONTROL DEFECT",
                                f"needle occurs {src.count(find)}x, want 1 — probe lands nowhere"))
                print(f"  [CONTROL DEFECT] {name}")
                continue
            try:
                path.write_text(src.replace(find, repl, 1))
                passed, _ = check()
            finally:
                path.write_bytes(originals[path])
            red = not passed
            verdict = "OK" if red == predict_red else ("!! BLIND" if not red else "!! REDS ON CORRECT CODE")
            results.append((name, verdict,
                            f"{'RED' if red else 'green'} (predicted {'RED' if predict_red else 'green'}) — {why}"))
            print(f"  [{verdict:22s}] {name}: {'RED' if red else 'green'}")

        pre = {p: sha(p) for p in (CLIENT, CTEST)}
        check()
        moved = [p.name for p in (CLIENT, CTEST) if sha(p) != pre[p]]
        verdict = "OK" if not moved else "!! MUTATES THE TREE"
        results.append(("D6 --check-anchors mutates nothing", verdict,
                        ("no watched file changed" if not moved else "CHANGED: " + ",".join(moved))
                        + " — the CI step is only safe because this holds"))
        print(f"  [{verdict:22s}] D6 --check-anchors mutates nothing")
    finally:
        for p, b in originals.items():
            p.write_bytes(b)
        bad = [p.name for p in originals if sha(p) != hashes[p]]
        print("\nrestore: " + ("BYTE-IDENTICAL" if not bad else "MISMATCH " + ",".join(bad)))

    ok, out = check()
    print("post-control baseline: " + ("GREEN" if ok else "RED\n" + out))
    defects = [r for r in results if r[1] != "OK"]
    print(f"\n{len(results)-len(defects)}/{len(results)} controls behaved as predicted")
    for n, v, d in results:
        print(f"  [{v}] {n}: {d}")
    return 1 if defects or not ok else 0


sys.exit(main())
