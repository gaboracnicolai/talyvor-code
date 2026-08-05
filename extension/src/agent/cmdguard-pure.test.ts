import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "fs";
import * as path from "path";
import { check, type Decision } from "./cmdguard-pure";

// The extension's `run` tool hands a model-authored string to `sh -c` with a cwd and a timeout and
// nothing else. #35 bounded the Go agent's identical tool; this is the same bound on this side.
//
// ⚠ THE TWO IMPLEMENTATIONS ARE KEPT IN STEP BY A SHARED CORPUS, NOT BY DISCIPLINE. The Go parser
// cannot be called from TypeScript, so the design is ported — and a ported design drifts unless
// something fails when it does. testdata/cmdguard-corpus.json is read by both test suites and both
// must produce the same verdict for every command in it.

interface Case {
  command: string;
  decision: Decision;
  why: string;
}

/** repoRoot walks up until it finds the corpus, so this anchors on a path that exists. */
function corpusPath(): string {
  let dir = __dirname;
  for (let i = 0; i < 10; i++) {
    const candidate = path.join(dir, "testdata", "cmdguard-corpus.json");
    if (fs.existsSync(candidate)) return candidate;
    const parent = path.dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  // ⚠ LOUD, NOT EMPTY. A guard whose corpus has moved would otherwise assert nothing and pass.
  throw new Error(
    `cmdguard corpus not found above ${__dirname} — this suite would have verified nothing`,
  );
}

const corpus = JSON.parse(fs.readFileSync(corpusPath(), "utf8")) as {
  cases: Case[];
  honestUserCost: { minimumUnattended: number; commands: string[] };
};

test("the corpus is actually loaded and non-trivial", () => {
  assert.ok(corpus.cases.length >= 30, `only ${corpus.cases.length} cases — the corpus is too thin to prove parity`);
  for (const d of ["allow", "confirm", "refuse"] as Decision[]) {
    assert.ok(corpus.cases.some((c) => c.decision === d), `the corpus contains no ${d} case`);
  }
});

// ⚠ EVERY CASE, INDIVIDUALLY. A single aggregate assertion would report "1 failure" for any number
// of divergences and hide which command changed meaning.
for (const c of corpus.cases) {
  test(`${c.decision}: ${JSON.stringify(c.command)}`, () => {
    const v = check(c.command);
    assert.equal(
      v.decision,
      c.decision,
      `check(${JSON.stringify(c.command)}) = ${v.decision} (${v.reason}), want ${c.decision} — ${c.why}`,
    );
  });
}

// ⚠ A REFUSAL IS NEVER DOWNGRADED TO A CONFIRMATION. Showing someone `go test $(cat /tmp/x)` and
// calling their yes informed consent would be worse than refusing: nobody can know what it expands
// to before a shell runs it.
test("nothing unparseable is ever offered for confirmation", () => {
  for (const c of corpus.cases.filter((x) => x.decision === "refuse")) {
    assert.notEqual(check(c.command).decision, "confirm", `${c.command} was offered for confirmation`);
  }
});

// ⚠ EVERY REFUSAL AND CONFIRMATION NAMES WHAT IS UNUSUAL. A prompt that shows the whole command and
// asks the user to spot the problem is how people learn to click yes.
test("a non-allow verdict says which segment forced it", () => {
  for (const c of corpus.cases.filter((x) => x.decision !== "allow")) {
    const v = check(c.command);
    assert.ok(v.reason.length > 0, `check(${JSON.stringify(c.command)}) gave no reason`);
  }
});

// ⚠ WHAT THE BOUND COSTS AN HONEST USER, MEASURED — the same 20 commands the Go side measures.
// If TypeScript comes out meaningfully worse than Go, that is a real difference, not a rounding.
test("most ordinary work still runs unattended", () => {
  const { commands, minimumUnattended } = corpus.honestUserCost;
  const allowed = commands.filter((c) => check(c).decision === "allow");
  console.log(
    `HONEST-USER COST (TypeScript): ${allowed.length}/${commands.length} unattended; ` +
      `${commands.length - allowed.length} need one confirmation`,
  );
  assert.ok(
    allowed.length >= minimumUnattended,
    `only ${allowed.length}/${commands.length} ordinary commands run unattended — the bound is too expensive to survive`,
  );
});
