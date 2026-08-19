import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "fs";
import * as path from "path";
import { allowedHeads, pipeFilters, flagsTakingValue } from "./cmdguard-pure";

// THE CORPUS IS A SAMPLE; THESE TABLES ARE THE POPULATION.
//
// cmdguard-pure.ts:12 says "a drift fails a build" and corpus_test.go:19 says "If someone widens the
// Go allowlist without widening the port, this fails". Measured by mutation rather than read: that is
// true only for entries cmdguard-corpus.json happens to carry a case for. Adding "awk" to the Go
// pipeFilters and nothing else — a straight widening of the allowlist that decides what a
// model-authored shell command may run unattended — left BOTH suites green. 25 of the 39 table
// entries had no corpus case at all: 9 of 18 allowedHeads keys, 7 of 9 pipeFilters, 9 of 12
// flagsTakingValue flags.
//
// So the sample stays — it is the only thing checking the two PARSERS agree on real commands — and
// the population is pinned beside it. This file asserts the TypeScript tables equal
// testdata/cmdguard-tables.json; agent/internal/cmdguard/tables_parity_test.go asserts the same of
// the Go maps. Widening one side now fails that side; editing the manifest alone fails both.

interface Manifest {
  allowedHeads: Record<string, string[]>;
  pipeFilters: string[];
  flagsTakingValue: Record<string, string[]>;
}

/** Walks up for the shared manifest. ⚠ LOUD, NOT EMPTY — a manifest that has moved must not leave
 *  this suite comparing against nothing, which is the failure the thing it guards already had. */
function manifestPath(): string {
  let dir = __dirname;
  for (let i = 0; i < 10; i++) {
    const candidate = path.join(dir, "testdata", "cmdguard-tables.json");
    if (fs.existsSync(candidate)) return candidate;
    const parent = path.dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  throw new Error(
    `cmdguard tables manifest not found above ${__dirname} — this suite would have verified nothing`,
  );
}

const manifest = JSON.parse(fs.readFileSync(manifestPath(), "utf8")) as Manifest;

/** Differences BY NAME, in both directions. "the tables differ" says something moved; naming the
 *  entry says what a shell may now do. */
function assertSameSet(what: string, got: Iterable<string>, want: string[]): void {
  const g = new Set(got);
  const w = new Set(want);
  for (const s of [...g].sort()) {
    assert.ok(
      w.has(s),
      `${what}: TypeScript has ${JSON.stringify(s)} and the shared manifest does not — widen testdata/cmdguard-tables.json AND the Go port, or drop it`,
    );
  }
  for (const s of [...w].sort()) {
    assert.ok(g.has(s), `${what}: the shared manifest has ${JSON.stringify(s)} and TypeScript does not — the two guards have drifted`);
  }
}

test("the manifest is actually loaded and non-trivial", () => {
  const entries =
    Object.keys(manifest.allowedHeads).length +
    manifest.pipeFilters.length +
    Object.values(manifest.flagsTakingValue).reduce((n, f) => n + f.length, 0);
  assert.ok(entries >= 30, `only ${entries} entries in the manifest — too thin to pin the population`);
  // The measured census, not a target. Changing it means changing the Go count in the same commit.
  assert.equal(Object.keys(manifest.allowedHeads).length, 18, "allowedHeads census changed");
  assert.equal(manifest.pipeFilters.length, 9, "pipeFilters census changed");
  assert.equal(Object.keys(manifest.flagsTakingValue).length, 6, "flagsTakingValue census changed");
});

test("allowedHeads matches the shared manifest", () => {
  assertSameSet("allowedHeads keys", Object.keys(allowedHeads), Object.keys(manifest.allowedHeads));
  for (const [head, subs] of Object.entries(allowedHeads)) {
    if (manifest.allowedHeads[head]) {
      assertSameSet(`allowedHeads[${head}]`, subs, manifest.allowedHeads[head]);
    }
  }
});

test("pipeFilters matches the shared manifest", () => {
  assertSameSet("pipeFilters", pipeFilters, manifest.pipeFilters);
});

test("flagsTakingValue matches the shared manifest", () => {
  assertSameSet("flagsTakingValue keys", Object.keys(flagsTakingValue), Object.keys(manifest.flagsTakingValue));
  for (const [cmd, flags] of Object.entries(flagsTakingValue)) {
    if (manifest.flagsTakingValue[cmd]) {
      assertSameSet(`flagsTakingValue[${cmd}]`, flags, manifest.flagsTakingValue[cmd]);
    }
  }
});
