#!/usr/bin/env python3
"""W3.63 — mutation controls for check-restore-signal-handlers.py itself.

The guard PASSED ON ITS FIRST RUN once the six scripts were converted. This project's standing rule
is to suspect that: three sessions here shipped a guard that could not fail, each caught only by a
control. Every rule below is mutated into existence and required to fire.

⚠ THE ONE THAT MATTERS MOST IS G8, AND IT IS INVERTED ON PURPOSE. It writes the words
`signal.signal(...)` into a COMMENT and requires the guard to STILL red. That is the whole argument
for parsing `ast` instead of grepping: in talyvor-suite `grep -l signal scripts/*.py` reports a
script as protected whose only occurrences of the word are four sentences of English.

⚠ AND G9 IS THE MIRROR: a real, correctly-converted script must NOT red. A guard that reds on
correct work gets relaxed until it reds on nothing.

This harness mutates scripts/ and restores in a `finally` with sha256 compared — and installs a
signal handler, because it is exactly the kind of script it is testing the guard for.
"""
from __future__ import annotations

import hashlib
import os
import pathlib
import signal
import subprocess
import sys

REPO = pathlib.Path(__file__).resolve().parent.parent
SCRIPTS = REPO / "scripts"
GUARD = SCRIPTS / "check-restore-signal-handlers.py"
SUBJECT = SCRIPTS / "w411-cost-claim-controls.py"
PROBE = SCRIPTS / "w363-probe-generated.py"


def restore_on_signal(snapshot: dict) -> None:
    """Put every snapshotted file back, then die of the signal we were sent."""

    def handler(signum, _frame):
        for path, blob in snapshot.items():
            try:
                path.write_bytes(blob)
            except OSError:
                pass
        PROBE.unlink(missing_ok=True)
        sys.stderr.write("\n!! signal %d — restored %d file(s) before exiting\n" % (signum, len(snapshot)))
        signal.signal(signum, signal.SIG_DFL)
        os.kill(os.getpid(), signum)

    for s in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP):
        signal.signal(s, handler)


def sha(p: pathlib.Path) -> str:
    return hashlib.sha256(p.read_bytes()).hexdigest()


def run_guard() -> tuple[bool, str]:
    r = subprocess.run([sys.executable, str(GUARD)], cwd=REPO, capture_output=True, text=True)
    return r.returncode == 0, r.stdout + r.stderr


# A minimal script in each shape, written to scripts/ so the guard's own walk finds it.
MUTATOR_NO_HANDLER = '''import pathlib
p = pathlib.Path("x")
try:
    original = p.read_text()
    p.write_text("mutated")
finally:
    p.write_text(original)
'''

MUTATOR_COMMENTED_HANDLER = '''import pathlib
# This script is protected: it calls signal.signal(signal.SIGTERM, handler) on startup,
# and signal.signal(signal.SIGINT, handler) too, so a signal cannot strand the tree.
p = pathlib.Path("x")
try:
    original = p.read_text()
    p.write_text("mutated")
finally:
    p.write_text(original)
'''

MUTATOR_NO_FINALLY = '''import pathlib
import signal
signal.signal(signal.SIGTERM, lambda *a: None)
p = pathlib.Path("x")
original = p.read_text()
p.write_text("mutated")
p.write_text(original)
'''

GOOD_SCRIPT = '''import pathlib
import signal
signal.signal(signal.SIGTERM, lambda *a: None)
p = pathlib.Path("x")
try:
    original = p.read_text()
    p.write_text("mutated")
finally:
    p.write_text(original)
'''

# (name, kind, payload, rule expected, predict_red, why)
CONTROLS = [
    ("G1 a new mutator with no handler", "probe", MUTATOR_NO_HANDLER, "R1", True,
     "R1 is the rule that stops the population growing"),
    ("G2 a mutator that restores only on the happy path", "probe", MUTATOR_NO_FINALLY, "R6", True,
     "the shape suite's finally-keyed definition cannot see — this port exists for it"),
    ("G3 a script this guard cannot parse", "probe", "def (:::\n", "R5", True,
     "an unparseable file must be reported, never silently skipped"),
    ("G4 UNPROTECTED lists a script that IS protected", "guard",
     ('UNPROTECTED: set[str] = set()', 'UNPROTECTED: set[str] = {"w411-cost-claim-controls.py"}'),
     "R2", True, "a fixed entry left listed rots the list into an excuse"),
    ("G5 NO_FINALLY lists a script that HAS a finally", "guard",
     ('NO_FINALLY: set[str] = set()', 'NO_FINALLY: set[str] = {"w411-cost-claim-controls.py"}'),
     "R2b", True, "same, for the wider half of the population"),
    ("G6 a list names a script that does not exist", "guard",
     ('UNPROTECTED: set[str] = set()', 'UNPROTECTED: set[str] = {"no-such-script.py"}'),
     "R4", True, "a stale entry is a hole nobody is looking at"),
    ("G7 NOT_MUTATORS entry with no reason", "guard",
     ('NOT_MUTATORS: dict[str, str] = {}',
      'NOT_MUTATORS: dict[str, str] = {"w411-cost-claim-controls.py": "  "}'),
     "R7", True, "an unexplained exemption is how the wider net gets narrowed back"),
    ("G8 INVERTED: signal.signal in a COMMENT is not a handler", "probe",
     MUTATOR_COMMENTED_HANDLER, "R1", True,
     "a grep reads the documentation as the implementation; ast does not see comments"),
    ("G9 INVERTED: a correctly converted script must NOT red", "probe", GOOD_SCRIPT, None, False,
     "a guard that reds on correct code gets relaxed until it reds on nothing"),
    ("G10 VACUITY: the detector blinded", "guard",
     ('    return reads and any(_write_call(n) for n in ast.walk(tree))',
      '    return False'),
     "R3", True, "if the walk stops recognising the shape the floor must catch it"),
    ("G11 VACUITY: the `finally` detector blinded", "guard",
     ('    return any(isinstance(n, ast.Try) and n.finalbody and _writes(n.finalbody)\n'
      '               for n in ast.walk(tree))', '    return False'),
     "R3b", True, "the second floor, for the same reason"),
]


def main() -> int:
    originals = {p: p.read_bytes() for p in (GUARD, SUBJECT)}
    hashes = {p: sha(p) for p in originals}
    restore_on_signal(originals)
    PROBE.unlink(missing_ok=True)

    ok, out = run_guard()
    if not ok:
        print("BASELINE IS NOT GREEN — every verdict below would be unreadable:\n" + out)
        return 2
    print("baseline: GREEN\n")

    results = []
    try:
        for name, kind, payload, rule, predict_red, why in CONTROLS:
            try:
                if kind == "probe":
                    PROBE.write_text(payload, encoding="utf-8")
                else:
                    find, repl = payload
                    src = originals[GUARD].decode()
                    if find not in src:
                        results.append((name, "CONTROL DEFECT", f"needle absent: {find[:60]!r}"))
                        print(f"  [CONTROL DEFECT] {name}")
                        continue
                    GUARD.write_text(src.replace(find, repl, 1), encoding="utf-8")
                passed, out = run_guard()
                red = not passed
                named = (rule is None) or any(l.startswith(rule + ":") for l in out.splitlines())
                behaved = (red == predict_red) and (not red or named)
                verdict = "OK" if behaved else "!! BLIND" if not red else "!! WRONG RULE"
                detail = ("RED" if red else "green") + (f" via {rule}" if red and named else "")
                results.append((name, verdict, f"{detail} (predicted {'RED' if predict_red else 'green'}) — {why}"))
                print(f"  [{verdict:14s}] {name}: {detail}")
            finally:
                PROBE.unlink(missing_ok=True)
                GUARD.write_bytes(originals[GUARD])
    finally:
        for p, b in originals.items():
            p.write_bytes(b)
        PROBE.unlink(missing_ok=True)
        bad = [str(p) for p in originals if sha(p) != hashes[p]]
        print("\nrestore: " + ("BYTE-IDENTICAL" if not bad else "MISMATCH " + ",".join(bad)))

    ok, out = run_guard()
    print("post-control baseline: " + ("GREEN" if ok else "RED\n" + out))
    defects = [r for r in results if r[1] != "OK"]
    print(f"\n{len(results)-len(defects)}/{len(results)} controls behaved as predicted")
    for n, v, d in results:
        print(f"  [{v}] {n}: {d}")
    return 1 if defects or not ok else 0


sys.exit(main())
