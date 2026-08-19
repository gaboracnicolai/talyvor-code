import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "fs";
import * as path from "path";
import { KNOWN_MODELS, getModel } from "./models-pure";

// WHAT THIS FILE EXISTS FOR: this product carries FOUR copies of its model list and, until this
// guard, none of them read any other.
//
//   · package.json → contributes.configuration.properties["talyvor.model"].enum
//        THE DROPDOWN THE USER ACTUALLY PICKS FROM in Settings
//   · src/model/models-pure.ts#KNOWN_MODELS
//        the `Talyvor: Select AI Model` QuickPick, and the status bar's label
//   · agent/internal/model/selector.go
//        the CLI
//   · test/model.test.ts#testListContainsExpectedModels
//        which restates the six ids as a FOURTH hand-copied literal, and therefore compares the
//        list to a copy of itself — it cannot see any of what is described below
//
// MEASURED ON MAIN 93418ee, WHICH IS WHY THIS GUARD WAS WRITTEN AND NOT JUST THE JSON EDITED. The
// settings enum was the only one of the four that nothing read, and it disagreed with KNOWN_MODELS
// in BOTH directions at once:
//
//   · IT OMITTED claude-opus-4-6. `commands/model-selector.ts` builds its QuickPick from
//     KNOWN_MODELS and does `cfg.update("model", picked.modelId, …)`, so the command the status-bar
//     tooltip tells the user to run WRITES a value the setting's own JSON schema does not accept.
//   · IT OFFERED llama-3.1-70b, which nothing in this repo knows — absent from KNOWN_MODELS (so
//     `getModel()` returns undefined, the QuickPick cannot list it and the status bar can only echo
//     the raw id back), absent from selector.go, and absent from talyvor-lens's price catalog.
//
// ⚠ THE ENUM IS READ FROM DISK AS DATA, NEVER RESTATED HERE. A fifth hand-copied list would be the
// defect this file exists to close, written into the guard against it. The expected values live in
// exactly one place — the product — and this file only asserts relationships BETWEEN the copies.
//
// ⚠ WHY BOTH DIRECTIONS AND NOT ONE: the two rules fail on disjoint inputs and neither can see the
// other's defect. Rule A is satisfied by an enum that lists every model plus a hundred fictional
// ones; rule B is satisfied by an enum listing a single model. Only the pair pins the SET.

// ─── locating the manifest ─────────────────────────────────────────────────────────────────────
//
// ⚠ THE TESTS RUN FROM out/, SO __dirname IS THE COMPILED TREE (out/src/model). Resolving
// "package.json" against it finds nothing; the walk goes up to the package root. A sibling guard in
// this repo (track/cost-claim.test.ts) carries the same walk for the same reason — it is repeated
// rather than shared because exporting it from a *.test.ts would re-run that file's tests on import.
// If the walk ever fails it THROWS: a guard that cannot find its subject must not report a clean one.
function extensionRoot(): string {
  let dir = __dirname;
  for (let i = 0; i < 8; i++) {
    if (fs.existsSync(path.join(dir, "package.json"))) return dir;
    dir = path.dirname(dir);
  }
  throw new Error("cannot locate the extension root from " + __dirname);
}

const MANIFEST = path.join(extensionRoot(), "package.json");

// settingsEnum reads the shipped dropdown. Every step that could quietly yield an empty set is a
// THROW rather than a fallback: an absent contributes block, a renamed setting id and a hand-edited
// non-array would each leave the rules below comparing empty sets and passing vacuously, which is
// precisely how a guard comes to certify a product it never read.
function settingsEnum(): string[] {
  const manifest = JSON.parse(fs.readFileSync(MANIFEST, "utf8"));
  const props = manifest?.contributes?.configuration?.properties;
  if (!props) {
    throw new Error(
      MANIFEST + " has no contributes.configuration.properties — this guard would read nothing",
    );
  }
  const setting = props["talyvor.model"];
  if (!setting) {
    throw new Error(
      "no `talyvor.model` setting in " + MANIFEST + ". If it was renamed, retarget this guard — " +
        "do not delete it: the rename is exactly when the four lists drift apart.",
    );
  }
  if (!Array.isArray(setting.enum) || setting.enum.length === 0) {
    throw new Error(
      "`talyvor.model` has no non-empty `enum` in " + MANIFEST + ". Without one the Settings UI is " +
        "a free-text box and nothing constrains what reaches Lens.",
    );
  }
  return setting.enum as string[];
}

function settingsDefault(): string {
  const manifest = JSON.parse(fs.readFileSync(MANIFEST, "utf8"));
  return manifest.contributes.configuration.properties["talyvor.model"].default;
}

// The floor is what makes a green run mean something. Measured population at the time of writing:
// 6 offered ids and 7 profiles. A guard whose read silently degrades to one entry would satisfy
// both rules below and report a clean product.
const OFFERED_FLOOR = 5;
const PROFILE_FLOOR = 5;

test("the guard is reading a real, populated model list on both sides", () => {
  const offered = settingsEnum();
  assert.ok(
    offered.length >= OFFERED_FLOOR,
    `only ${offered.length} ids in the talyvor.model enum (floor ${OFFERED_FLOOR}). This guard is ` +
      `reading nothing, or nearly nothing, and its green is worthless. Check the manifest path and ` +
      `the contributes.configuration shape.`,
  );
  assert.ok(
    KNOWN_MODELS.length >= PROFILE_FLOOR,
    `only ${KNOWN_MODELS.length} profiles in KNOWN_MODELS (floor ${PROFILE_FLOOR}).`,
  );
});

// ─── RULE A — everything the product can WRITE, the setting must ACCEPT ────────────────────────
//
// The writer is `commands/model-selector.ts`, which maps over KNOWN_MODELS and persists the chosen
// id with cfg.update. An id it can write that the enum does not list is a value the extension puts
// into the user's settings.json and the extension's own schema then marks invalid.
test("every model the Select-AI-Model command can write is accepted by the talyvor.model setting", () => {
  const offered = new Set(settingsEnum());
  const unwritable = KNOWN_MODELS.map((m) => m.id).filter((id) => !offered.has(id));
  assert.deepEqual(
    unwritable,
    [],
    `these ids are offered by the QuickPick and written to settings by commands/model-selector.ts, ` +
      `but are NOT in package.json's talyvor.model enum, so the schema rejects the value the ` +
      `extension itself just wrote: ${unwritable.join(", ")}. Add them to the enum, or stop ` +
      `offering them in KNOWN_MODELS — the two lists are one set.`,
  );
});

// ─── RULE B — everything the setting OFFERS, the product must be able to render ────────────────
//
// An id in the dropdown with no profile has no display name (status-bar.ts falls back to echoing
// the raw id), no icon, no tier, and no QuickPick entry — and, being unknown to talyvor-lens's
// price catalog as well, it is billed on a fallback bound rather than a published rate.
test("every model the talyvor.model dropdown offers has a profile the product can render", () => {
  const unknown = settingsEnum().filter((id) => getModel(id) === undefined);
  assert.deepEqual(
    unknown,
    [],
    `these ids are offered to the user in Settings but have no entry in KNOWN_MODELS, so getModel() ` +
      `returns undefined — no display name, no icon, no tier, and no way to pick them from the ` +
      `QuickPick: ${unknown.join(", ")}. Give them a profile, or stop offering them.`,
  );
});

// ─── RULE C — the shipped default must itself be a model the product supports ─────────────────
//
// The default is what every install runs until someone changes it, so it is the one entry whose
// breakage is silent and universal.
test("the default model is offered by the setting and has a profile", () => {
  const def = settingsDefault();
  assert.ok(
    settingsEnum().includes(def),
    `talyvor.model defaults to "${def}", which is not in its own enum.`,
  );
  assert.ok(
    getModel(def) !== undefined,
    `talyvor.model defaults to "${def}", which has no profile in KNOWN_MODELS — every fresh install ` +
      `would run a model this product cannot name.`,
  );
});
