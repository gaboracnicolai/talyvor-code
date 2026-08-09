#!/usr/bin/env python3
"""W6.7 positive controls for the exec redirect seam.

Every control: assert the anchor count BEFORE writing · apply · run · require the named test to go
RED · require a companion test to STAY GREEN · restore the file and verify sha256 is identical.

WHY EACH RULE IS HERE, each paid for in this repo or a sibling:
  · ANCHOR COUNT FIRST — a substitution that matches nothing edits zero bytes, and a control that
    no-ops is byte-indistinguishable from a guard that works (talyvor-track #71).
  · CUMULATIVE STAGING — a control with two edits in one file that computes both from the ORIGINAL
    text silently discards the first write, and then reports a working guard as blind (#99, #414).
  · A COMPANION THAT MUST STAY GREEN — a mutation that breaks the build reds everything, and a
    control that cannot tell "the guard caught it" from "nothing compiled" is not a control (#74).
  · sha256 RESTORE — an exit path that leaves the tree dirty makes every later measurement suspect.

Run:  python3 scripts/w67-bypass-controls.py
"""
import hashlib
import pathlib
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
AGENT = ROOT / "agent"
SIDECAR = AGENT / "internal" / "sidecar" / "sidecar.go"
RUN = AGENT / "internal" / "sidecar" / "run.go"
TEST = AGENT / "internal" / "sidecar" / "bypass_test.go"

REDIRECT_TEST = "TestEveryMeasuredRedirectPointsAtTheSidecar"
REFUSE_TEST = "TestTheSidecarRefusesAnEnvironmentThatWouldBypassIt"
PREDICATE_TEST = "TestTheBypassPredicateIsTheOneThatWasMeasured"
# A test this change did not touch. If a control reds THIS, the mutation broke the package rather
# than being caught, and the control proves nothing.
UNRELATED_TEST = "TestTheChildEnvironmentRedirectsWithoutPlantingAKey"


def sha(p):
    return hashlib.sha256(p.read_bytes()).hexdigest()


def run_test(name):
    """Return True when the named test PASSES."""
    p = subprocess.run(
        ["go", "test", "./internal/sidecar/", "-run", f"^{name}$", "-count=1"],
        cwd=AGENT, capture_output=True, text=True,
    )
    return p.returncode == 0, (p.stdout + p.stderr)


class Control:
    """expect_red=False marks a control whose CORRECT outcome is GREEN.

    ⚠ Such a control is a DEMONSTRATION, not a catch, and is reported as one rather than counted
    among them (talyvor-track #75's C3 precedent). C8 is the case: it shows what the guard would be
    worth if it read the package's own list instead of a hand-written one.
    """

    def __init__(self, cid, what, edits, target, must_stay_green, expect_red=True):
        self.cid, self.what, self.edits = cid, what, edits
        self.must_red, self.must_stay_green = target, must_stay_green
        self.expect_red = expect_red


CONTROLS = [
    Control("C1", "drop ANTHROPIC_API_BASE from RedirectVars — the whole finding",
            [(SIDECAR, '\t"ANTHROPIC_BASE_URL",\n\t"ANTHROPIC_API_BASE",\n', '\t"ANTHROPIC_BASE_URL",\n', 1)],
            REDIRECT_TEST, REFUSE_TEST),

    Control("C2", "set only the FIRST redirect var, the shape the code shipped with",
            [(SIDECAR,
              "\tfor _, name := range RedirectVars {\n\t\tout = append(out, name+\"=\"+s.BaseURL())\n\t}",
              "\tout = append(out, RedirectVars[0]+\"=\"+s.BaseURL())", 1)],
            REDIRECT_TEST, REFUSE_TEST),

    Control("C3", "delete the refusal from Run — exec proceeds into an unattributed session",
            [(RUN,
              "\tif reason := BypassReason(os.Environ()); reason != \"\" {\n\t\treturn 1, errors.New(reason)\n\t}\n",
              "", 1)],
            REFUSE_TEST, REDIRECT_TEST),

    Control("C4", "predicate becomes 'any non-empty value' — refuses someone who set it to 0",
            [(SIDECAR, '\tcase "", "0", "false":\n\t\treturn false\n', '\tcase "":\n\t\treturn false\n', 1)],
            PREDICATE_TEST, REDIRECT_TEST),

    Control("C5", "predicate never engages — the ON half must notice",
            [(SIDECAR, "func engagesBypass(v string) bool {\n", "func engagesBypass(v string) bool {\n\treturn false\n", 1)],
            PREDICATE_TEST, REDIRECT_TEST),

    Control("C6", "drop CLAUDE_CODE_USE_VERTEX from BypassVars — a stale list must not pass",
            [(SIDECAR, '\t"CLAUDE_CODE_USE_BEDROCK",\n\t"CLAUDE_CODE_USE_VERTEX",\n', '\t"CLAUDE_CODE_USE_BEDROCK",\n', 1)],
            REFUSE_TEST, REDIRECT_TEST),

    # ⚠ THIS CONTROL WAS WRONG ON ITS FIRST RUN AND THE RUN IS WHAT SAID SO. Its first version
    # blanked only the FIRST of the three %s slots, so the message still ended "Unset
    # CLAUDE_CODE_USE_BEDROCK to run through Lens" and the guard's Contains check passed — CORRECTLY.
    # Reported as a blind guard; it was a blind mutation. Blinding all three args is the real test.
    Control("C7", "the refusal stops naming the variable — the developer cannot act on it",
            [(SIDECAR, "\t\t\t\tname, v, name)", '\t\t\t\t"an endpoint", v, "it")', 1)],
            REFUSE_TEST, REDIRECT_TEST),

    # ⚠ THE VACUITY CONTROL. Two edits, and BOTH are the point: the guard's hardcoded list is
    # replaced by the package's own, AND the package's list loses the name. A guard that reads the
    # constant it is checking compares it to itself and passes for every possible value — including
    # the empty list. If this control comes back GREEN, the hardcoding is what is holding the line.
    Control("C8", "make the guard read the package's own list, then empty that list (vacuity demo)",
            [(TEST,
              '\t"ANTHROPIC_BASE_URL",\n\t"ANTHROPIC_API_BASE",\n}',
              '}\n\nvar _ = RedirectVars', 1),
             (TEST, "var theseAreTheRedirectsMeasuredToWork = []string{\n",
              "var theseAreTheRedirectsMeasuredToWork = RedirectVars\n\nvar _ = []string{\n", 1),
             (SIDECAR, '\t"ANTHROPIC_BASE_URL",\n\t"ANTHROPIC_API_BASE",\n', '\t"ANTHROPIC_BASE_URL",\n', 1)],
            REDIRECT_TEST, REFUSE_TEST, expect_red=False),

    # ⚠ Proves the marker-file assertion is load-bearing and not decoration: the refusal still
    # HAPPENS and still returns an error, it just happens after the child has already run.
    Control("C9", "refuse AFTER starting the child — the error is right and the child ran anyway",
            [(RUN,
              "\tif reason := BypassReason(os.Environ()); reason != \"\" {\n\t\treturn 1, errors.New(reason)\n\t}\n",
              "", 1),
             (RUN,
              "\tif err := cmd.Start(); err != nil {\n\t\treturn 1, fmt.Errorf(\"could not start %s: %w\", command[0], err)\n\t}\n",
              "\tif err := cmd.Start(); err != nil {\n\t\treturn 1, fmt.Errorf(\"could not start %s: %w\", command[0], err)\n\t}\n"
              "\tif reason := BypassReason(os.Environ()); reason != \"\" {\n\t\t_ = cmd.Wait()\n\t\treturn 1, errors.New(reason)\n\t}\n", 1)],
            REFUSE_TEST, REDIRECT_TEST),
]


def main():
    originals = {p: p.read_bytes() for p in (SIDECAR, RUN, TEST)}
    hashes = {p: sha(p) for p in originals}

    ok, green = True, run_test(REDIRECT_TEST)[0] and run_test(REFUSE_TEST)[0] and run_test(PREDICATE_TEST)[0]
    print(f"BASELINE all three guards green: {green}")
    if not green:
        print("  refusing to run controls against a red baseline")
        return 1

    for c in CONTROLS:
        # ⚠ EVERY ANCHOR ASSERTED BEFORE ANY WRITE, and staged CUMULATIVELY so a second edit to the
        # same file sees the first.
        staged = {p: originals[p].decode() for p in originals}
        counts = []
        for path, old, new, want in c.edits:
            n = staged[path].count(old)
            counts.append(n)
            if n != want:
                print(f"{c.cid} NOT RUN — anchor {old[:45]!r} found {n}x in {path.name}, expected {want}")
                ok = False
                break
            staged[path] = staged[path].replace(old, new, 1)
        else:
            for path in {p for p, *_ in c.edits}:
                path.write_text(staged[path])
            red_ok, out = run_test(c.must_red)
            green_ok, _ = run_test(c.must_stay_green)
            unrelated_ok, _ = run_test(UNRELATED_TEST)
            for path in originals:
                path.write_bytes(originals[path])
            restored = sha(path := SIDECAR) == hashes[SIDECAR] and sha(RUN) == hashes[RUN] and sha(TEST) == hashes[TEST]

            caught = not red_ok
            behaved = (caught == c.expect_red)
            if c.expect_red:
                verdict = "CAUGHT" if behaved else "!! BLIND"
            else:
                verdict = "VACUOUS(expected)" if behaved else "!! demo reddened"
            print(f"{c.cid} anchors={counts} {verdict:17s} "
                  f"{c.must_red}={'RED' if caught else 'green'} "
                  f"companion={'green' if green_ok else '!! ALSO RED'} "
                  f"unrelated={'green' if unrelated_ok else '!! ALSO RED'} "
                  f"restored={restored}")
            print(f"     {c.what}")
            if not behaved or not green_ok or not unrelated_ok or not restored:
                ok = False
                if not behaved:
                    print("     " + "\n     ".join(out.strip().splitlines()[:4]))

    for path in originals:
        path.write_bytes(originals[path])
    print("\nALL CONTROLS BEHAVED:", ok)
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
