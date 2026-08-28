import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "fs";
import * as path from "path";
import { check } from "./cmdguard-pure";

// ⚠ THE SHARED CORPUS CANNOT EXPRESS THE ONE DRIFT THAT EXISTS.
//
// cmdguard-pure.ts:12 states the contract this pair runs on — "THIS IS A PORT ... Two guards that
// disagree are worse than one, because the weaker one is the one that matters ... a drift fails a
// build" — and testdata/cmdguard-corpus.json is what is supposed to make that true. But a corpus
// case carries ONE `decision` field for BOTH implementations, so it can only ever record a verdict
// the two already share. It is structurally unable to say "these two disagree"; it can only say
// "these two agree on X". Adding a case for an axis where they diverge means first choosing the
// answer, which is the permission decision nobody has taken.
//
// ⚠ AND THEY DO DIVERGE. Measured (W4.34, tab-h4w7), not read: the Go package trims with
// strings.TrimSpace, which is unicode.IsSpace; this file trims with String.prototype.trim, which is
// ECMAScript WhiteSpace + LineTerminator. Those are not the same set. They differ on exactly two
// code points out of the 29 either one claims:
//
//   U+0085 NEXT LINE                  unicode.IsSpace: yes   JS trim: no    -> Go allows, TS confirms
//   U+FEFF ZERO WIDTH NO-BREAK SPACE  unicode.IsSpace: no    JS trim: yes   -> TS allows, Go confirms
//
// Neither is an escalation to an arbitrary command — the head must still be in the allowlist, and
// both call sites treat "confirm" as a prompt rather than a block. What it costs is that one side
// runs unattended what the other stops to ask about, on the boundary whose own header says two
// guards that disagree are worse than one.
//
// ⚠ THIS TEST DOES NOT FIX IT, BECAUSE WHICH WAY TO CONVERGE IS A PERMISSION DECISION. Matching Go
// widens this extension on U+0085; matching JS widens the agent on U+FEFF; trimming neither narrows
// both and widens nothing. That is a call for a human. What this test does is make the drift fail a
// build the way the header already claimed it did.
//
// agent/internal/cmdguard/whitespace_parity_test.go is the mirror of this file.

interface Side {
  decision: string;
  reason: string;
}

interface WsCase {
  codepoint: string;
  name: string;
  shape: string;
  command: string;
  go: Side;
  ts: Side;
  agree: boolean;
}

interface WsManifest {
  divergentCodepoints: string[];
  minimumCases: number;
  minimumAgreeingCases: number;
  cases: WsCase[];
}

// ⚠ HARD LITERAL FLOORS, NOT THE MANIFEST'S OWN NUMBERS. minimumCases lives in the file, so a
// blinded file that sets it to zero and empties `cases` would satisfy a floor read from itself —
// the exact shape of a count that is green in the world it exists to detect.
const FLOOR_CASES = 150;
const FLOOR_AGREEING = 100;
const FLOOR_CODEPOINTS = 25;

// WANT_DIVERGENT is the measured, filed divergence. It is an EXACT set on purpose.
//
// ⚠ IF YOU ARE READING THIS BECAUSE THE TEST WENT RED HERE: a code point was added or removed from
// the divergence, which means either a new drift appeared between the two ports, or somebody
// converged them and this pin is now stale. If it is the second — good, that is the decision being
// taken. Regenerate testdata/cmdguard-whitespace.json, shrink this list, and when it is empty
// delete this file and fold the cases into cmdguard-corpus.json, which can hold them once there is
// a single agreed verdict to hold.
const WANT_DIVERGENT = ["U+0085", "U+FEFF"];

/** Walks up for the shared manifest. ⚠ LOUD, NOT EMPTY — the same rule manifestPath() in
 *  cmdguard-tables.test.ts follows, and for the same reason. */
function manifestPath(): string {
  let dir = __dirname;
  for (let i = 0; i < 10; i++) {
    const candidate = path.join(dir, "testdata", "cmdguard-whitespace.json");
    if (fs.existsSync(candidate)) return candidate;
    const parent = path.dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  throw new Error(
    `cmdguard whitespace manifest not found above ${__dirname} — this suite would have verified nothing`,
  );
}

const manifest = JSON.parse(fs.readFileSync(manifestPath(), "utf8")) as WsManifest;

/** Names the members rather than reporting a count. "the sets differ" says something moved; naming
 *  the code point says which invisible character a shell may now be handed unattended. */
function setDiff(got: Set<string>, want: string[]): string {
  const w = new Set(want);
  const extra = [...got].filter((s) => !w.has(s)).sort();
  const missing = [...w].filter((s) => !got.has(s)).sort();
  if (extra.length === 0 && missing.length === 0) return "";
  return `measured-divergent-but-not-filed=${JSON.stringify(extra)} filed-but-no-longer-divergent=${JSON.stringify(missing)}`;
}

// The floor. Everything below compares against the manifest, so an empty or shrunken manifest would
// make every other assertion trivially true.
test("the whitespace manifest is not vacuous", () => {
  assert.ok(
    manifest.cases.length >= FLOOR_CASES,
    `only ${manifest.cases.length} cases in the whitespace manifest, floor is ${FLOOR_CASES} — a shrunken corpus cannot show a drift`,
  );
  assert.ok(
    manifest.minimumCases >= FLOOR_CASES,
    `manifest declares minimumCases=${manifest.minimumCases}, below this test's floor of ${FLOOR_CASES}`,
  );
  assert.ok(
    manifest.minimumAgreeingCases >= FLOOR_AGREEING,
    `manifest declares minimumAgreeingCases=${manifest.minimumAgreeingCases}, below this test's floor of ${FLOOR_AGREEING}`,
  );

  const agreeing = manifest.cases.filter((c) => c.agree).length;
  const codepoints = new Set(manifest.cases.map((c) => c.codepoint));

  // ⚠ BOTH DIRECTIONS HAVE TO BE POPULATED. A corpus of nothing but divergences proves the two
  // sides differ and says nothing about the far larger space where they must keep agreeing; a
  // corpus of nothing but agreements is what let this drift sit unnoticed in the first place.
  assert.ok(
    agreeing >= FLOOR_AGREEING,
    `only ${agreeing} AGREEING cases, floor is ${FLOOR_AGREEING} — the corpus no longer covers the space where the two ports must not drift`,
  );
  assert.ok(
    codepoints.size >= FLOOR_CODEPOINTS,
    `only ${codepoints.size} distinct code points, floor is ${FLOOR_CODEPOINTS} — the union of unicode.White_Space and ECMAScript WhiteSpace is what this axis is`,
  );
  assert.notEqual(
    agreeing,
    manifest.cases.length,
    "every case agrees, so nothing in this manifest is pinning a divergence — if the ports really were converged, WANT_DIVERGENT should be empty too",
  );
});

// The load-bearing one: it reads the SHIPPED behaviour of this module and holds it to the manifest.
// Decision AND reason — W4.34 measured that a one-sided widening for a head already in allowedHeads
// moves the reason and leaves the decision alone, so a comparison of the decision bit alone is blind
// to half the drift it exists to catch.
test("the TypeScript guard matches its column of the whitespace manifest", () => {
  let checked = 0;
  for (const c of manifest.cases) {
    const v = check(c.command);
    assert.equal(
      v.decision,
      c.ts.decision,
      `${c.codepoint} ${c.name} (${c.shape}) ${JSON.stringify(c.command)}: TypeScript decision is ${v.decision}, manifest says ${c.ts.decision} — this port's whitespace handling moved; regenerate the manifest and re-read the divergence`,
    );
    assert.equal(
      v.reason,
      c.ts.reason,
      `${c.codepoint} ${c.name} (${c.shape}) ${JSON.stringify(c.command)}: TypeScript reason is ${JSON.stringify(v.reason)}, manifest says ${JSON.stringify(c.ts.reason)}`,
    );
    checked++;
  }
  assert.ok(checked >= FLOOR_CASES, `checked only ${checked} commands, floor is ${FLOOR_CASES}`);
});

// Holds the header's claim to the file. The manifest's `agree` column and its `divergentCodepoints`
// list are two statements about the same fact, so deriving one from the other and comparing catches
// an edit to either alone.
test("the whitespace divergence is exactly the filed set", () => {
  const derived = new Set<string>();
  for (const c of manifest.cases) {
    const same = c.go.decision === c.ts.decision && c.go.reason === c.ts.reason;
    assert.equal(
      c.agree,
      same,
      `${c.codepoint} ${c.name} (${c.shape}): \`agree\` says ${c.agree} but the two columns say ${same} — the manifest contradicts itself`,
    );
    if (!c.agree) derived.add(c.codepoint);
  }

  assert.equal(
    setDiff(derived, manifest.divergentCodepoints),
    "",
    `divergentCodepoints does not match the cases it summarises: ${setDiff(derived, manifest.divergentCodepoints)}`,
  );
  assert.equal(
    setDiff(derived, WANT_DIVERGENT),
    "",
    `THE MEASURED DIVERGENCE MOVED: ${setDiff(derived, WANT_DIVERGENT)}\nA code point appearing here is a NEW drift between the Go guard and this TypeScript port. A code point disappearing means somebody converged them — if that is what happened, this pin has done its job: regenerate the manifest, shrink WANT_DIVERGENT, and when it is empty delete this file and fold the cases into cmdguard-corpus.json.`,
  );
});
