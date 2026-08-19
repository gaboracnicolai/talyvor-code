import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "fs";
import * as path from "path";
import { costDisclaimerLines, formatSessionCost } from "./cost-label-pure";

// THE DOUBLE-COUNT IS GONE, AND THE DISPLAY STILL WORKS.
//
// ⚠ BOTH HALVES ARE ASSERTED ON PURPOSE. A test that checked only the ABSENCE of the PATCH passes
// on a completely broken extension — one that renders nothing, syncs nothing and starts nothing
// would sail through it. The second half is what makes the first mean something.
//
// Background: the extension used to compute `issue.aiCostUsd + localEstimate` and PATCH that
// ABSOLUTE total onto issues.ai_cost_usd, the same column Lens's exactly-once syncer writes from
// the real per-request cost — with `catch {}` hiding every lost race. Lens now records the issue
// itself (migration 0116 put request_id on request_attribution; /v1/api/spend/by-request emits
// issue_id) and Track's syncer prefers it over the feature, so the client-side writer is a pure
// double-count with a worse number.

// ⚠ THE TESTS RUN FROM out/, SO __dirname IS THE COMPILED TREE. Resolving "src" relative to it
// walked out/src looking for .ts files, found an EMPTY SET, and passed unconditionally — the guard
// was scanning nothing. Caught by re-introducing the write and watching the test still pass. Walk up
// to the package root and take its src/ instead, and fail loudly if that directory is not there.
function extensionRoot(): string {
  let dir = __dirname;
  for (let i = 0; i < 8; i++) {
    if (fs.existsSync(path.join(dir, "package.json"))) return dir;
    dir = path.dirname(dir);
  }
  throw new Error("cannot locate the extension root from " + __dirname);
}

const srcDir = path.join(extensionRoot(), "src");
if (!fs.existsSync(srcDir)) {
  throw new Error("source tree not found at " + srcDir + " — this guard would scan nothing");
}

function walk(dir: string): string[] {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) return walk(p);
    return e.isFile() && p.endsWith(".ts") && !p.endsWith(".test.ts") ? [p] : [];
  });
}

// ⚠ HALF ONE: no cost write to Track, anywhere in the extension.
test("the extension makes no cost PATCH to Track", () => {
  const offenders: string[] = [];
  for (const file of walk(srcDir)) {
    const src = fs.readFileSync(file, "utf8");
    // The write is identifiable by its payload: a PATCH carrying ai_cost_usd.
    if (/ai_cost_usd\s*:/.test(src) && /"PATCH"/.test(src)) {
      offenders.push(path.relative(srcDir, file));
    }
    if (/updateIssueCost\s*\(/.test(src)) {
      offenders.push(path.relative(srcDir, file) + " (updateIssueCost)");
    }
    if (/syncCostToTrack\s*\(/.test(src)) {
      offenders.push(path.relative(srcDir, file) + " (syncCostToTrack)");
    }
  }
  assert.deepEqual(
    offenders,
    [],
    "the client-side cost write is back. Lens writes issues.ai_cost_usd from the real per-request " +
      "cost; a second writer here is a double-count, and the last writer wins silently:\n" +
      offenders.join("\n"),
  );
});

// ⚠ HALF TWO: the status bar still produces a figure. Without this, half one is vacuous.
test("the status bar still renders a session cost", () => {
  assert.equal(formatSessionCost(0), "~$0.00");
  assert.equal(formatSessionCost(0.126), "~$0.13");
  assert.equal(formatSessionCost(12.5), "~$12.50");
});

// ⚠ AND IT IS MARKED AS AN ESTIMATE. The figure sits beside an issue identifier that Track also
// shows a number for; unlabelled, a reader takes them for the same quantity — and this one is
// 20x low on claude-opus-5, and 4x low on claude-haiku-4-5, the default model (cost-claim.test.ts).
test("the rendered cost is marked approximate", () => {
  assert.ok(formatSessionCost(1).startsWith("~"), "the tilde is what marks it approximate");
});

test("the tooltip says it is an estimate and names where the real figure lives", () => {
  const generic = costDisclaimerLines().join("\n");
  assert.match(generic, /not your bill/i);
  assert.match(generic, /Track shows the real/i);

  const scoped = costDisclaimerLines("ENG-42").join("\n");
  assert.match(scoped, /ENG-42/, "when an issue is active the tooltip should name it");
});
