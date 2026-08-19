#!/usr/bin/env python3
"""W4.11 control harness — cost-claim.test.ts, both directions.

Every control names its PREDICTED catcher before it runs, restores the tree in a `finally`, and the
whole tree is sha256-verified at the end. The runner is `npm run test:unit` from extension/, which is
the exact command CI's `unit tests (includes agent path confinement)` step runs.

WHY THIS EXISTS: rule A of the new guard PASSED ON ITS FIRST RUN. A guard that has never been red is
a guard that has never been shown to be able to go red — three sessions in this project shipped one
of those, each caught only by a control. C2 and C6 are the controls that make rule A's green mean
something; C3 is the one that makes rule B's green mean something.
"""

import hashlib
import pathlib
import subprocess
import sys

EXT = pathlib.Path(__file__).resolve().parent.parent / "extension"
SRC = EXT / "src"

TRACKER = SRC / "providers" / "cost-tracker.ts"
LABEL = SRC / "track" / "cost-label-pure.ts"
GUARD = SRC / "track" / "cost-claim.test.ts"


def tree_hash() -> str:
    h = hashlib.sha256()
    for p in sorted(SRC.rglob("*.ts")):
        h.update(p.relative_to(SRC).as_posix().encode())
        h.update(p.read_bytes())
    return h.hexdigest()


def run_tests() -> tuple[bool, str]:
    r = subprocess.run(
        ["npm", "run", "test:unit"],
        cwd=EXT,
        capture_output=True,
        text=True,
    )
    return r.returncode == 0, r.stdout + r.stderr


def failing_test_names(out: str) -> list[str]:
    names = []
    for line in out.splitlines():
        s = line.strip()
        if s.startswith("✖ ") and "tests" not in s:
            names.append(s[2:].split(" (")[0])
    return sorted(set(names))


def control(label: str, prediction: str, edits: list[tuple[pathlib.Path, str, str]]):
    """edits: (path, old, new). Applied against the CURRENT bytes, one file read per edit, so two
    edits to the same file cannot silently erase each other (that bug voided three controls in an
    earlier session on the suite repo)."""
    print(f"\n=== {label}")
    print(f"    PREDICTED: {prediction}")
    originals = {}
    try:
        for path, old, new in edits:
            if path not in originals:
                originals[path] = path.read_text()
            text = path.read_text()
            if old not in text:
                print(f"    VOID — anchor not found in {path.name}: {old[:60]!r}")
                return
            path.write_text(text.replace(old, new, 1))
        ok, out = run_tests()
        # ⚠ A COMPILE ERROR IS NOT A CAUGHT MUTATION. The first cut of C3 used `[] as string[] || ...`
        # and tsc refused it (TS2872) before a single assertion ran; this harness scored the exit
        # code and called it CAUGHT. An instrument that reads a broken build as a catch will bless a
        # guard that cannot fail, so the build failure is now its own verdict.
        if "error TS" in out:
            ts = [l.strip() for l in out.splitlines() if "error TS" in l]
            print(f"    RESULT: VOID — the mutation does not compile, no assertion ran: {ts[:2]}")
            return
        if ok:
            print("    RESULT: NOT CAUGHT — suite green")
        else:
            print(f"    RESULT: CAUGHT — failing: {failing_test_names(out)}")
        for line in out.splitlines():
            s = line.strip()
            if s.startswith("providers/") or s.startswith("track/") or "floor" in s or "arithmetic says" in s:
                print(f"      | {s[:160]}")
    finally:
        for path, text in originals.items():
            path.write_text(text)


def main() -> int:
    before = tree_hash()
    ok, out = run_tests()
    print(f"C0 baseline (no mutation): {'GREEN' if ok else 'RED'}")
    if not ok:
        print("    baseline is red — every control below would be uninterpretable")
        print("\n".join(out.splitlines()[-25:]))
        return 1

    # C1 — the defect exactly as it shipped: a family name and the wrong factor.
    control(
        "C1  the shipped false claim restored in cost-label-pure.ts",
        "rule B reds naming cost-label-pure.ts — a factor with no catalog model id",
        [(LABEL, "4x low on claude-haiku-4-5", "understates Sonnet by ~4x")],
    )

    # C2 — the constants moved to the catalog rate. Rule A must see it THROUGH estimateCostUSD.
    control(
        "C2  the hardcoded rate raised to the catalog's claude-haiku-4-5 price",
        "rule A reds for every model (4x becomes 1x, 8x becomes 2x, ...); rule B reds too",
        [
            (TRACKER, "0.25 / 1_000_000", "1.00 / 1_000_000"),
            (TRACKER, "1.25 / 1_000_000", "5.00 / 1_000_000"),
        ],
    )

    # C3 — the scanner blinded, with C1's defect ON TOP. This is the measured blindness the floor
    # exists for: without the floor the offender list is empty and the run is green over a rule that
    # has stopped reading anything.
    control(
        "C3  rule B's walk blinded by a filter typo WITH C1's defect present",
        "the FLOOR reds saying it read 0 claims, and the offender list is EMPTY",
        [
            (LABEL, "4x low on claude-haiku-4-5", "understates Sonnet by ~4x"),
            # ⚠ RE-CUT. The first version was `[] as string[] || walk(...)`, which tsc rejects
            # (TS2872) — a build failure, not a blinded guard. A filter typo compiles, is the way
            # this actually goes wrong in practice, and leaves the guard running over nothing.
            (GUARD, 'p.endsWith(".ts") ? [p] : []', 'p.endsWith(".tsx") ? [p] : []'),
        ],
    )

    # C4 — a true factor, but the model id removed. A false pointer traded for a vague one.
    control(
        "C4  the model id dropped from a correct claim (claude-opus-5 -> Opus)",
        "rule B reds: 20x now names no catalog model",
        [(TRACKER, "20x on claude-opus-5", "20x on Opus")],
    )

    # C5 — MUST STAY GREEN under the new guard. Without this, "CAUGHT" could just mean "any edit
    # anywhere reds something", which is not a guard, it is a tripwire.
    control(
        "C5  the tooltip's disclaimer wording broken (unrelated to the factors)",
        "NOT caught by cost-claim; CAUGHT by the pre-existing no-cost-write tooltip test",
        [(LABEL, "not your bill", "your bill")],
    )

    # C6 — the cross-repo anchor itself. Proves rule A reads the catalog side, not only the estimate.
    control(
        "C6  one catalog price changed in the pinned table (haiku 1.00 -> 2.00)",
        "rule A reds for claude-haiku-4-5 — AND the 1:5 ratio test, which my first prediction "
        "UNDER-LISTED: moving input alone breaks the ratio as well as the factor",
        [(GUARD, 'id: "claude-haiku-4-5", inputPer1M: 1.0', 'id: "claude-haiku-4-5", inputPer1M: 2.0')],
    )

    after = tree_hash()
    print(f"\ntree sha256 before={before[:16]} after={after[:16]} {'IDENTICAL' if before == after else '*** DIFFERS ***'}")
    ok, _ = run_tests()
    print(f"post-restore suite: {'GREEN' if ok else 'RED'}")
    return 0 if before == after and ok else 1


if __name__ == "__main__":
    sys.exit(main())
