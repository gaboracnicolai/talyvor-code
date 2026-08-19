import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "fs";
import * as path from "path";
import { estimateCostUSD } from "../providers/cost-tracker";

// WHAT THIS FILE EXISTS FOR: the status bar's cost figure is computed at ONE HARDCODED RATE, and
// every comment that explains it states HOW WRONG that rate is for a given model. Those factors are
// arithmetic — they are `catalog rate / hardcoded rate` — and until this guard existed nothing could
// tell whether the prose matched the arithmetic. It did not.
//
// MEASURED ON MAIN d2e886a, WHICH IS WHY THIS GUARD WAS WRITTEN AND NOT JUST THE COMMENTS FIXED:
// `cost-tracker.ts` said "These are Haiku's rates. A Sonnet call is understated ~4x"; `status-bar.ts`
// said "understates Haiku by ~4x"; `cost-label-pure.ts` said "understates Sonnet by ~4x". Two of the
// three were false and the three could not all be right. The rate is $0.25/$1.25 per 1M — CLAUDE 3
// HAIKU's price — while the model the extension DEFAULTS to, claude-haiku-4-5, costs $1.00/$5.00.
// The number in the bar is 4x low in the default configuration, on the one model the source claimed
// it priced exactly.
//
// ⚠ THE CONSTANTS ARE NOT CHANGED BY THIS FILE. Moving a price a paying user is shown is a decision
// (queue item W4.11 states the three options); making the claim about it checkable is not.

// ─── the cross-repo anchor ────────────────────────────────────────────────────────────────────
//
// Prices as talyvor-lens's catalog holds them — `internal/catalog/seed.go`, the table the proxy
// bills from. Read read-only at lens main on 2026-08-19.
//
// ⚠ KEYED BY MODEL ID, NEVER BY LINE. A `seed.go:88` citation here would decay with no commit in
// this repo and come to point at a different model — that is exactly the class that put three
// contradictory line citations into one enum elsewhere in this project. A model id survives edits.
//
// ⚠ AND THE FACTOR IS NOT A SECOND LITERAL: it is asserted to be the SAME for input and output
// below, so a model whose in:out ratio stops matching the hardcoded pair fails loudly instead of
// being summarised by a single number that fits neither side.
const LENS_CATALOG = [
  // the extension's default model — see package.json, `talyvor.model`
  { id: "claude-haiku-4-5", inputPer1M: 1.0, outputPer1M: 5.0, factor: 4 },
  { id: "claude-sonnet-5", inputPer1M: 2.0, outputPer1M: 10.0, factor: 8 },
  { id: "claude-sonnet-4-5", inputPer1M: 3.0, outputPer1M: 15.0, factor: 12 },
  { id: "claude-opus-5", inputPer1M: 5.0, outputPer1M: 25.0, factor: 20 },
  { id: "claude-fable-5", inputPer1M: 10.0, outputPer1M: 50.0, factor: 40 },
];

// ─── RULE A — the factors are the arithmetic, measured through the shipped function ────────────
//
// Reads `estimateCostUSD` rather than the constants, so it pins the BEHAVIOUR a user sees. If the
// rate is ever restated, every factor moves and this reds — which is what forces the prose (rule B)
// to be corrected in the same commit rather than drifting again.
test("each documented understatement factor is catalog rate / hardcoded rate", () => {
  const IN = 1_000_000;
  const OUT = 1_000_000;
  const estimated = estimateCostUSD(IN, OUT);
  assert.ok(estimated > 0, "the estimator returns zero — the rest of this test would be vacuous");

  for (const m of LENS_CATALOG) {
    const real = (IN * m.inputPer1M + OUT * m.outputPer1M) / 1_000_000;
    const measured = real / estimated;
    assert.ok(
      Math.abs(measured - m.factor) < 1e-9,
      `${m.id}: the source documents ${m.factor}x but the arithmetic says ${measured.toFixed(4)}x ` +
        `(catalog $${m.inputPer1M}/$${m.outputPer1M} per 1M vs the hardcoded rate). Either the rate ` +
        `changed — in which case fix every factor the comments state, they are all wrong now — or ` +
        `the catalog moved, in which case re-read talyvor-lens internal/catalog/seed.go for ${m.id}.`,
    );
  }
});

test("the hardcoded rate keeps the catalog's 1:5 input:output ratio, so one factor per model is honest", () => {
  const inputOnly = estimateCostUSD(1_000_000, 0);
  const outputOnly = estimateCostUSD(0, 1_000_000);
  for (const m of LENS_CATALOG) {
    const fIn = m.inputPer1M / inputOnly;
    const fOut = m.outputPer1M / outputOnly;
    assert.ok(
      Math.abs(fIn - fOut) < 1e-9,
      `${m.id} is understated ${fIn.toFixed(2)}x on input but ${fOut.toFixed(2)}x on output. A single ` +
        `factor cannot describe it and the comments must stop stating one.`,
    );
  }
});

// ─── RULE B — every factor claim in the source names a model and states the measured number ────
//
// ⚠ THE TESTS RUN FROM out/, SO __dirname IS THE COMPILED TREE. Resolving "src" against it walks a
// directory with no .ts files in it, finds an empty set and passes unconditionally — the exact way a
// sibling guard in this directory was once scanning nothing. Walk up to the package root, and fail
// loudly if src/ is not there.
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

// This file is EXCLUDED from its own scan on purpose: it holds the expected values, so a scanner
// that read them would be comparing the table to itself.
const SELF = "track/cost-claim.test.ts";

function sources(): string[] {
  const walk = (dir: string): string[] =>
    fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
      const p = path.join(dir, e.name);
      if (e.isDirectory()) return walk(p);
      return e.isFile() && p.endsWith(".ts") ? [p] : [];
    });
  // .test.ts files are IN scope: a false factor in a test's comment misleads exactly as well as one
  // in product source, and this repo already had one.
  return walk(srcDir).filter((p) => path.relative(srcDir, p) !== SELF);
}

// ⚠ A HEX LITERAL IS NOT A COST FACTOR. This was /~?(\d+)x/g, which reads the "0x" of 0xff, 0x80 and
// 0xa9fea9fe as a claim of "0x" — a factor of zero, with no model id on the line, so every hex
// constant anywhere under src/ was reported as a false factor claim. Found when safeurl-pure.ts
// introduced the first hex constants in this tree: 13 offenders, none of them a claim about money.
// The lookahead refuses a match whose "x" is followed by a hex digit (0xff, 0x80, 0x0f, 0xc0); the
// lookbehind keeps the match off the inside of an identifier. Neither relaxes what a real claim must
// satisfy — "~4x low", "8x on claude-sonnet-5" and "12x" all still match, and the control in this
// file's sibling harness plants a false claim to prove it.
const FACTOR = /(?<![\w.])~?(\d+)x(?![0-9a-fA-F])/g;
const MODEL_IDS = LENS_CATALOG.map((m) => m.id);

// Measured population at the time of writing: 7 claims across 4 files. The floor is what makes a
// green run mean something — a scanner whose walk, regex or filter silently matches nothing reports
// a clean repo, and every assertion after it is vacuous.
const CLAIM_FLOOR = 5;

test("every Nx factor in the extension source names a catalog model and states the measured factor", () => {
  const offenders: string[] = [];
  let claims = 0;

  for (const file of sources()) {
    const rel = path.relative(srcDir, file);
    const lines = fs.readFileSync(file, "utf8").split("\n");
    lines.forEach((line, i) => {
      for (const match of line.matchAll(FACTOR)) {
        const stated = Number(match[1]);
        const at = match.index ?? 0;
        claims++;

        // Pair the factor with the nearest model id ON THE SAME LINE, so one line may carry several
        // claims ("4x on claude-haiku-4-5, 8x on claude-sonnet-5") and each is checked against its
        // own model.
        let nearest: { id: string; dist: number } | undefined;
        for (const id of MODEL_IDS) {
          let from = line.indexOf(id);
          while (from !== -1) {
            const dist = Math.abs(from - at);
            if (!nearest || dist < nearest.dist) nearest = { id, dist };
            from = line.indexOf(id, from + 1);
          }
        }

        if (!nearest) {
          offenders.push(
            `${rel}:${i + 1}: "${match[0]}" states a factor with no catalog model id on the line, so ` +
              `nothing can check it. A family name is not a priced model — claude-sonnet-5 is 8x and ` +
              `claude-sonnet-4-5 is 12x, so "Sonnet" alone is not a factor. Name the id, or drop the ` +
              `number if the sentence is about labelling rather than magnitude.`,
          );
          continue;
        }
        const want = LENS_CATALOG.find((m) => m.id === nearest!.id)!.factor;
        if (stated !== want) {
          offenders.push(
            `${rel}:${i + 1}: claims ${stated}x for ${nearest.id}; measured against the catalog it ` +
              `is ${want}x.`,
          );
        }
      }
    });
  }

  assert.ok(
    claims >= CLAIM_FLOOR,
    `only ${claims} factor claims found in ${srcDir} (floor ${CLAIM_FLOOR}). This guard is reading ` +
      `nothing, or nearly nothing, and its green is worthless. Check the walk, the regex and the filter.`,
  );
  assert.deepEqual(
    offenders,
    [],
    "a factor claim in the source disagrees with the arithmetic:\n" + offenders.join("\n"),
  );
});
