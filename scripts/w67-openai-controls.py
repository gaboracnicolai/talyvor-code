#!/usr/bin/env python3
"""W6.7 positive controls for the OpenAI door — the second provider the sidecar carries.

⚠ A SIBLING OF scripts/w67-credential-controls.py AND w67-bypass-controls.py,
DELIBERATELY DUPLICATED RATHER THAN IMPORTED. Those harnesses carry evidence
gathered at the ANTHROPIC seam; reusing one here would lend this seam's anchors a
provenance nobody gathered for it. The rules are the same and are restated so this
file stands on its own:

  · ANCHOR COUNT FIRST — a substitution that matches nothing edits zero bytes, and
    a control that no-ops is byte-indistinguishable from a guard that works.
  · CUMULATIVE STAGING — two edits to one file computed from the ORIGINAL text
    means the second write discards the first, and a working guard gets reported
    as blind.
  · EACH RED MUST SAY THE THING IT IS SUPPOSED TO SAY. A test that reds for a
    reason other than the one the control exists to trigger is not a catch, and
    NOT CAUGHT and CAUGHT-FOR-THE-WRONG-REASON are otherwise indistinguishable.
  · A COMPANION THAT MUST STAY GREEN, plus an UNTOUCHED test — a mutation that
    breaks the build reds everything.
  · sha256 RESTORE on every exit path.

⚠ WHAT IS UNDER TEST. `exec -- aider --model gpt-4o` reached api.openai.com on the
developer's own key, put ZERO requests on Lens, exited 0 and printed a banner
saying the spend was attributed. The fix hands the OpenAI names a base URL with a
`/openai/v1` prefix and routes what arrives under that prefix to Lens's OpenAI
passthrough. TWO HALVES, ONE DECISION — hand out a door, route what arrives at it —
so the controls attack both halves and the join between them.

Run:  python3 scripts/w67-openai-controls.py
"""
import hashlib
import pathlib
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
AGENT = ROOT / "agent"
SIDECAR = AGENT / "internal" / "sidecar" / "sidecar.go"
TEST = AGENT / "internal" / "sidecar" / "openai_test.go"

DOOR_TEST = "TestTheOpenAIDoorIsPointedAtTheSidecar"
KEY_TEST = "TestTheOpenAIChildIsGivenTheNameAndNotACredential"
ROUTE_TEST = "TestTheOpenAIDoorForwardsOntoLensOpenAIPassthrough"
ATTRIB_TEST = "TestTheOpenAIDoorCarriesTheAttribution"
LEAK_TEST = "TestTheOpenAIDoorNeverCarriesTheChildsOwnCredential"
RULE_TEST = "TestTheDoorDecidesTheProviderNotThePath"
FLOOR_TEST = "TestTheAnthropicDoorIsUnchanged"
# ⚠ TOUCHED BY NO CONTROL HERE. If this reds, the mutation broke the package rather than being
# caught, and the control proves nothing about the guard.
UNRELATED_TEST = "TestCloseReleasesThePort"

OPENAI_PLANT = ('\tfor _, name := range OpenAIRedirectVars {\n'
                '\t\tout = append(out, name+"="+s.BaseURL()+openAIDoor+"/v1")\n'
                '\t}')
ANTHROPIC_PLANT = ('\tfor _, name := range RedirectVars {\n'
                   '\t\tout = append(out, name+"="+s.BaseURL())\n'
                   '\t}')
CONCAT = "slices.Concat(RedirectVars, OpenAIRedirectVars, emptiedCredentialVars, droppedCredentialVars)"
EMPTIED = '\t"ANTHROPIC_API_KEY",\n\t"OPENAI_API_KEY",\n}'
ROUTE = ('\tprovider, path := "anthropic", r.URL.Path\n'
         '\tif rest, ok := strings.CutPrefix(r.URL.Path, openAIDoor+"/"); ok {\n'
         '\t\tprovider, path = "openai", "/"+rest\n'
         '\t} else if r.URL.Path == openAIDoor {\n'
         '\t\tprovider, path = "openai", "/"\n'
         '\t}')
ALLOWLIST = '[]string{"Content-Type", "Accept", "Anthropic-Version", "Anthropic-Beta"}'
ISSUE = ('\tif s.cfg.Issue != "" {\n'
         '\t\treq.Header.Set("X-Talyvor-Issue", s.cfg.Issue)\n'
         '\t}')
DOOR_CONST = 'const openAIDoor = "/openai"'


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

    def __init__(self, cid, what, edits, target, must_say, must_stay_green, expect_red=True):
        self.cid, self.what, self.edits = cid, what, edits
        self.must_red, self.must_say = target, must_say
        self.must_stay_green = must_stay_green
        self.expect_red = expect_red


CONTROLS = [
    Control("C1", "point the OpenAI names at the ROOT — every OpenAI body then lands on Lens's "
                  "ANTHROPIC passthrough, which is a 400 at best and a mispriced row at worst",
            [(SIDECAR, OPENAI_PLANT, OPENAI_PLANT.replace('+openAIDoor+"/v1"', ''), 1)],
            DOOR_TEST, "OPENAI_BASE_URL", ROUTE_TEST),

    Control("C2", "revert the redirect entirely — the shipped defect: the developer's own "
                  "OPENAI_BASE_URL survives and their OpenAI account is billed under our banner",
            [(SIDECAR, OPENAI_PLANT, "", 1)],
            DOOR_TEST, "appears 0 times", FLOOR_TEST),

    Control("C3", "plant the LENS KEY as the OpenAI credential — readable by anything the child spawns",
            [(SIDECAR, '\t\tout = append(out, name+"=")', '\t\tout = append(out, name+"="+s.cfg.LensAPIKey)', 1)],
            KEY_TEST, "must carry NOTHING", ROUTE_TEST),

    # ⚠ THIS CONTROL CORRECTED MY OWN PREDICTION AND THE COMMENT NOW SAYS THE MEASURED THING.
    # I had written that dropping the name produces an ABSENT name (aider raising AuthenticationError
    # locally and never opening a socket). It does not: the SAME list drives the replacement filter,
    # so the name comes back carrying THE DEVELOPER'S OWN KEY — their account billed for work the
    # banner attributes to Lens. Worse than the failure I predicted, and only the required-substring
    # check made the difference visible.
    Control("C4", "drop OPENAI_API_KEY from the emptied set — the developer's own key then reaches "
                  "the child, because that one list drives the replacement filter too",
            [(SIDECAR, EMPTIED, '\t"ANTHROPIC_API_KEY",\n}', 1)],
            KEY_TEST, "the developer's own OpenAI key survived", FLOOR_TEST),

    Control("C5", "drop OpenAIRedirectVars out of the REPLACED set — ours is appended and the "
                  "developer's stale value stays beside it, and which one the child reads is exec(2)'s",
            [(SIDECAR, CONCAT,
              "slices.Concat(RedirectVars, emptiedCredentialVars, droppedCredentialVars)", 1)],
            DOOR_TEST, "appears 2 times", FLOOR_TEST),

    Control("C6", "route by SNIFFING the request path instead of the door the child was given",
            [(SIDECAR, ROUTE,
              '\tprovider, path := "anthropic", r.URL.Path\n'
              '\tif strings.Contains(r.URL.Path, "/chat/completions") {\n'
              '\t\tprovider = "openai"\n'
              '\t}', 1)],
            RULE_TEST, "ROOT door", ATTRIB_TEST),

    Control("C7", "send the OpenAI door to the ANTHROPIC passthrough — metered against the wrong "
                  "provider's prices",
            [(SIDECAR, ROUTE, ROUTE.replace('provider, path = "openai", "/"+rest',
                                            'provider, path = "anthropic", "/"+rest'), 1)],
            ROUTE_TEST, "want /v1/proxy/openai", ATTRIB_TEST),

    Control("C8", "grow the header allowlist by one — the child's organisation name reaches Lens",
            [(SIDECAR, ALLOWLIST, ALLOWLIST.replace('"Anthropic-Beta"',
                                                    '"Anthropic-Beta", "OpenAI-Organization"'), 1)],
            LEAK_TEST, "OpenAI-Organization", ROUTE_TEST),

    Control("C9", "attribute only the Anthropic door — METERED IS NOT ATTRIBUTED, and the per-issue "
                  "cost is the whole product claim",
            [(SIDECAR, ISSUE, ISSUE.replace('if s.cfg.Issue != ""',
                                            'if s.cfg.Issue != "" && provider == "anthropic"'), 1)],
            ATTRIB_TEST, "X-Talyvor-Issue", ROUTE_TEST),

    Control("C10", "give the ANTHROPIC door a prefix too — the supported client is the risk half of "
                   "this merge and must be held by a test of its own",
            [(SIDECAR, ANTHROPIC_PLANT, ANTHROPIC_PLANT.replace('name+"="+s.BaseURL()',
                                                                'name+"="+s.BaseURL()+"/anthropic"'), 1)],
            FLOOR_TEST, "must not acquire a prefix", DOOR_TEST),

    # ⚠ THE VACUITY DEMONSTRATION, reported as one rather than counted as a catch. The guard stops
    # writing the door out and asks the package for it; the package is then moved. GREEN is the
    # correct outcome and IS the point — a test that reads the constant it is checking compares it
    # to itself and passes for every possible value, including a door no client was ever given.
    Control("C11", "make the guard ask the package where the door is, then move the door (vacuity demo)",
            [(TEST, 'want := s.BaseURL() + "/openai/v1"',
              'want := s.BaseURL() + openAIDoor + "/v1"', 1),
             (SIDECAR, DOOR_CONST, 'const openAIDoor = "/not-a-door-any-client-was-given"', 1)],
            DOOR_TEST, "", FLOOR_TEST, expect_red=False),
]


def main():
    files = (SIDECAR, TEST)
    originals = {p: p.read_bytes() for p in files}
    hashes = {p: sha(p) for p in files}

    guards = (DOOR_TEST, KEY_TEST, ROUTE_TEST, ATTRIB_TEST, LEAK_TEST, RULE_TEST, FLOOR_TEST, UNRELATED_TEST)
    baseline = all(run_test(t)[0] for t in guards)
    print(f"BASELINE all {len(guards)} guards green: {baseline}")
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
            said = (c.must_say in out) if (caught and c.must_say) else not c.expect_red
            behaved = (caught == c.expect_red) and said
            if not c.expect_red:
                verdict = "VACUOUS(expected)" if behaved else "!! demo reddened"
            elif not caught:
                verdict = "!! BLIND"
            elif not said:
                verdict = "!! WRONG REASON"
            else:
                verdict = "CAUGHT"
            print(f"{c.cid} anchors={counts} {verdict:17s} "
                  f"{c.must_red}={'RED' if caught else 'green'} "
                  f"companion={'green' if green_ok else '!! ALSO RED'} "
                  f"unrelated={'green' if unrelated_ok else '!! ALSO RED'} "
                  f"restored={restored}")
            print(f"     {c.what}")
            if not behaved or not green_ok or not unrelated_ok or not restored:
                ok = False
                print("     " + "\n     ".join(out.strip().splitlines()[:5]))

    for path in files:
        path.write_bytes(originals[path])
    print("\nALL CONTROLS BEHAVED:", ok)
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
