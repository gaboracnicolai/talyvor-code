#!/usr/bin/env python3
"""W6.7 positive controls for the credential the child is and is not given.

Same rules as scripts/w67-bypass-controls.py, which this is a sibling of and
deliberately not an extension of — the seams are different and a shared harness
would lend one set of anchors the other's evidence:

  · ANCHOR COUNT FIRST — a substitution that matches nothing edits zero bytes,
    and a control that no-ops is byte-indistinguishable from a guard that works.
  · CUMULATIVE STAGING — two edits to one file computed from the ORIGINAL text
    means the second write discards the first, and a working guard gets reported
    as blind.
  · A COMPANION THAT MUST STAY GREEN, plus an UNTOUCHED test — a mutation that
    breaks the build reds everything, and "the guard caught it" must be
    distinguishable from "nothing compiled".
  · sha256 RESTORE on every exit path.

⚠ THE RULE UNDER TEST IS ABOUT A VALUE, NOT A NAME, and that distinction is the
whole merge: ANTHROPIC_API_KEY must be PRESENT (or aider dials nothing) and
EMPTY (or Claude Code's connectors are disabled). C1 and C2 are the two ways to
get it wrong, and they fail in opposite directions.

Run:  python3 scripts/w67-credential-controls.py
"""
import hashlib
import pathlib
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
AGENT = ROOT / "agent"
SIDECAR = AGENT / "internal" / "sidecar" / "sidecar.go"
TEST = AGENT / "internal" / "sidecar" / "credential_test.go"
OLD = AGENT / "internal" / "sidecar" / "sidecar_test.go"

VALUE_TEST = "TestTheChildIsGivenTheNameAndNotACredential"
REPLACE_TEST = "TestADevelopersOwnKeyIsReplacedRatherThanForwarded"
TOKEN_TEST = "TestTheAuthTokenNameIsRemovedAndNotPlanted"
REDIRECT_TEST = "TestEveryMeasuredRedirectPointsAtTheSidecar"
# ⚠ TOUCHED BY NO CONTROL HERE. If this reds, the mutation broke the package rather than being
# caught, and the control proves nothing about the guard.
UNRELATED_TEST = "TestCloseReleasesThePort"

PLANT = ('\tfor _, name := range emptiedCredentialVars {\n'
         '\t\tout = append(out, name+"=")\n'
         '\t}')
CONCAT = "slices.Concat(RedirectVars, emptiedCredentialVars, droppedCredentialVars)"


def sha(p):
    return hashlib.sha256(p.read_bytes()).hexdigest()


def run_test(name):
    p = subprocess.run(
        ["go", "test", "./internal/sidecar/", "-run", f"^{name}$", "-count=1"],
        cwd=AGENT, capture_output=True, text=True,
    )
    return p.returncode == 0, (p.stdout + p.stderr)


class Control:
    """expect_red=False marks a control whose CORRECT outcome is GREEN — a vacuity
    DEMONSTRATION, reported as one rather than counted among the catches."""

    def __init__(self, cid, what, edits, target, must_stay_green, expect_red=True):
        self.cid, self.what, self.edits = cid, what, edits
        self.must_red, self.must_stay_green = target, must_stay_green
        self.expect_red = expect_red


CONTROLS = [
    Control("C1", "plant a non-empty PLACEHOLDER — measured to disable every claude.ai connector",
            [(SIDECAR, PLANT, PLANT.replace('name+"="', 'name+"=sk-ant-placeholder"'), 1)],
            VALUE_TEST, TOKEN_TEST),

    Control("C2", "revert the planting — the shipped defect: aider dials nothing and spends nothing",
            [(SIDECAR, PLANT, "", 1)],
            VALUE_TEST, REPLACE_TEST),

    Control("C3", "plant the LENS KEY as the value — a credential anything the child spawns can read",
            [(SIDECAR, PLANT, PLANT.replace('name+"="', 'name+"="+s.cfg.LensAPIKey'), 1)],
            REPLACE_TEST, TOKEN_TEST),

    Control("C4", "stop replacing the name, so the developer's OWN key reaches the child and their "
                  "account is billed for work the banner attributes to Lens",
            [(SIDECAR, CONCAT, "slices.Concat(RedirectVars, droppedCredentialVars)", 1)],
            REPLACE_TEST, VALUE_TEST),

    Control("C5", "'complete' the fix by emptying ANTHROPIC_AUTH_TOKEN too — measured to satisfy no "
                  "client, and a name Claude Code counts as an auth source",
            [(SIDECAR, '\t"ANTHROPIC_API_KEY",\n}', '\t"ANTHROPIC_API_KEY",\n\t"ANTHROPIC_AUTH_TOKEN",\n}', 1)],
            TOKEN_TEST, VALUE_TEST),

    Control("C6", "plant the name twice — which value the child reads is exec(2)'s business",
            [(SIDECAR, PLANT, PLANT.replace('out = append(out, name+"=")',
                                            'out = append(out, name+"=")\n\t\tout = append(out, name+"=")'), 1)],
            VALUE_TEST, TOKEN_TEST),

    # ⚠ THE VACUITY DEMONSTRATION. The guard stops naming the variable and asks the package which
    # one to check; the package is then pointed at a name no client reads. GREEN is the correct
    # outcome and IS the point — a guard that reads the constant it is checking compares it to
    # itself and passes for every possible value.
    # ⚠ RECORDED RATHER THAN TUNED AWAY: the OLD pin in sidecar_test.go hardcodes the name too, so
    # it goes RED under this mutation. Two independent hardcodings hold this line, which is why the
    # companion here is a test in the other file rather than that one.
    Control("C7", "make the guard ask the package which name to check, then rename it (vacuity demo)",
            [(TEST, 'strings.CutPrefix(kv, "ANTHROPIC_API_KEY=")',
              'strings.CutPrefix(kv, emptiedCredentialVars[0]+"=")', 1),
             (SIDECAR, '\t"ANTHROPIC_API_KEY",\n}', '\t"ANTHROPIC_NOT_A_NAME_ANY_CLIENT_READS",\n}', 1)],
            VALUE_TEST, REDIRECT_TEST, expect_red=False),

    # ⚠ THE REFACTOR REGRESSION. This merge rewrote the drop-set from a nested append() into
    # slices.Concat, and a rewrite that quietly stops covering the redirects would leave a
    # developer's stale ANTHROPIC_BASE_URL in place beside ours.
    Control("C8", "drop RedirectVars out of the replaced set — the previous merge's finding returns",
            [(SIDECAR, CONCAT, "slices.Concat(emptiedCredentialVars, droppedCredentialVars)", 1)],
            REDIRECT_TEST, VALUE_TEST),
]


def main():
    files = (SIDECAR, TEST, OLD)
    originals = {p: p.read_bytes() for p in files}
    hashes = {p: sha(p) for p in files}

    baseline = all(run_test(t)[0] for t in (VALUE_TEST, REPLACE_TEST, TOKEN_TEST, REDIRECT_TEST, UNRELATED_TEST))
    print(f"BASELINE all five guards green: {baseline}")
    if not baseline:
        print("  refusing to run controls against a red baseline")
        return 1

    ok = True
    for c in CONTROLS:
        staged = {p: originals[p].decode() for p in files}
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
            for path in files:
                path.write_bytes(originals[path])
            restored = all(sha(p) == hashes[p] for p in files)

            caught = not red_ok
            behaved = (caught == c.expect_red)
            verdict = ("CAUGHT" if behaved else "!! BLIND") if c.expect_red else \
                      ("VACUOUS(expected)" if behaved else "!! demo reddened")
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

    for path in files:
        path.write_bytes(originals[path])
    print("\nALL CONTROLS BEHAVED:", ok)
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
