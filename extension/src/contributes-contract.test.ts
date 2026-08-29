import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "fs";
import * as path from "path";
import { SECRET_KEYS } from "./secrets-pure";

// contributes-contract.test.ts — package.json's `contributes` block against the code behind it.
//
// WHAT THIS FILE EXISTS FOR: `contributes` IS A CONTRACT AND ITS READER IS A PERSON. VS Code reads
// that JSON and renders it — a declared command becomes a Command Palette entry, a declared setting
// becomes a row in the Settings UI with a description and a default, a keybinding becomes a live key.
// Nothing in this repository checked that any of it was wired. `model-enum.test.ts` pins ONE property
// of ONE setting (the `talyvor.model` enum, across four copies of the model list); this file pins the
// SHAPE of the whole block.
//
// THE RESULT, MEASURED AT 2054864 (W4.48): 24 declared commands, 24 registrations, EXACT set equality
// in both directions and no duplicate on either side. All 6 keybindings and all 10 `editor/context`
// menu entries name a command that is both declared and registered. All 12 configuration properties
// are read or written by the code. NO DEFECT FOUND — this file is what keeps that true.
//
// ⚠ THE FAILURE THIS PREVENTS IS ASYMMETRIC AND BOTH HALVES ARE USER-VISIBLE. A declared command with
// no `registerCommand` is a Palette entry that answers **"command 'talyvor.x' not found"** when a user
// picks it — VS Code raises that at invocation, not at load, so it ships green. A registered command
// with no declaration is the mirror: the handler exists, the feature works, and no user can find it.
//
// ⚠⚠ HOW THE MEASUREMENT LIED ON THE WAY HERE, BOTH TIMES IN A DIFFERENT DIRECTION, AND NEITHER WAS
// VISIBLE BY READING:
//
//  1. A NAME-KEYED CENSUS SCORES EVERY COMMAND AS WIRED. `"talyvor.explainCode"` is the same string in
//     package.json and in the registration, so any grep over the repo returns a beautiful zero. The
//     registration has to be located as the ARGUMENT of a `registerCommand(...)` call in a non-test
//     `.ts` under src/, with the manifest excluded from the population it is checked against. Control
//     C1 exists to prove this file did not fall for it.
//
//  2. A LITERAL SCAN CALLED A LIVE SETTING INERT, PURELY OVER WHITESPACE. `talyvor.agentIterative` is
//     read in AgentPanel.ts as `vscode.workspace` ⏎ `.getConfiguration("talyvor")` ⏎
//     `.get<boolean>("agentIterative", false)`. A pattern that assumes the chain is on one line reports
//     a declared setting that nothing reads — a FALSE POSITIVE, which costs a reader exactly what a
//     false clean does. Everything below matches against whitespace-collapsed source for that reason.
//
// ⚠ WHAT THIS FILE DOES NOT CLAIM. `contributes` is not the whole declared surface: `activationEvents`
// and the JetBrains plugin's own `plugin.xml` declare overlapping things and are OUTSIDE this
// boundary, stated rather than left to be inferred. And REGISTERED IS NOT REACHED — a command bound to
// a handler that can never succeed is not something a static parity check can see.

// ─── locating the manifest ─────────────────────────────────────────────────────────────────────
//
// ⚠ THE TESTS RUN FROM out/, SO __dirname IS THE COMPILED TREE. Resolving "package.json" against it
// finds nothing; the walk goes up to the package root. `model/model-enum.test.ts` carries the same
// walk for the same reason. If the walk fails it THROWS — a guard that cannot find its subject must
// not report a clean one.
function packageRoot(): string {
  let dir = __dirname;
  for (let i = 0; i < 10; i++) {
    if (fs.existsSync(path.join(dir, "package.json"))) return dir;
    const up = path.dirname(dir);
    if (up === dir) break;
    dir = up;
  }
  throw new Error(
    "contributes-contract: could not find package.json walking up from " + __dirname,
  );
}

const ROOT = packageRoot();

interface Manifest {
  contributes: {
    commands: { command: string; title: string }[];
    keybindings: { command: string; key?: string; when?: string }[];
    menus: Record<string, { command?: string; when?: string }[]>;
    configuration: { properties: Record<string, unknown> };
  };
}

function manifest(): Manifest {
  return JSON.parse(
    fs.readFileSync(path.join(ROOT, "package.json"), "utf8"),
  ) as Manifest;
}

// sourceFiles returns every non-test TypeScript file under src/.
//
// ⚠ TEST FILES ARE EXCLUDED AND THAT IS LOAD-BEARING: a command registered only in a test is not
// registered, and a settings read that only a fixture performs is not a read. This is the same rule
// W6.47 records for talyvor-suite, where 56 test files sit against 22 non-test ones.
function sourceFiles(): string[] {
  const out: string[] = [];
  const walk = (dir: string) => {
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      const p = path.join(dir, e.name);
      if (e.isDirectory()) {
        if (e.name === "node_modules" || e.name === "out") continue;
        walk(p);
        continue;
      }
      if (!e.name.endsWith(".ts") || e.name.endsWith(".test.ts")) continue;
      out.push(p);
    }
  };
  walk(path.join(ROOT, "src"));
  return out;
}

// collapsed returns the file with every run of whitespace flattened to one space, so a method chain
// broken across lines by the formatter matches exactly as a single-line one does. See lie (2) above.
function collapsed(file: string): string {
  return fs.readFileSync(file, "utf8").replace(/\s+/g, " ");
}

// ⚠ EVERY DOT AND PAREN HERE TOLERATES OPTIONAL SPACES, AND THAT IS THE LESSON OF LIE (2)
// ARRIVING A SECOND TIME IN THIS FILE. Collapsing `vscode.workspace` ⏎ `.getConfiguration(` gives
// `vscode.workspace .getConfiguration(` — a space BEFORE the dot and none after. A pattern
// written with literal single spaces around the dots matched ZERO of the real chains and
// reported EIGHT live settings as inert. The guard caught itself only because it was run.
const CFG_CHAIN = String.raw`vscode ?\. ?workspace ?\. ?getConfiguration\( ?(?:"talyvor"|'talyvor'|SECTION) ?\)`;

/** Every command id passed as a literal first argument to registerCommand, with its file. */
function registrations(): { id: string; file: string }[] {
  const out: { id: string; file: string }[] = [];
  for (const f of sourceFiles()) {
    const src = collapsed(f);
    for (const m of src.matchAll(/registerCommand\( *["'`]([^"'`]+)["'`]/g)) {
      out.push({ id: m[1], file: path.relative(ROOT, f) });
    }
  }
  return out;
}

/**
 * Command ids this file could NOT resolve to a literal — a `registerCommand(someVariable, …)` or a
 * template literal with an interpolation.
 *
 * ⚠ REPORTED AND REFUSED, NEVER SILENTLY SKIPPED. An assembled id is a registration no literal scan
 * can see, so scoring such a command "unregistered" would be a false positive of exactly the shape
 * lie (2) produced. There are ZERO at 2054864; if one appears, this file says so and stops rather
 * than quietly weakening its own claim.
 */
function unresolvableRegistrations(): string[] {
  const out: string[] = [];
  for (const f of sourceFiles()) {
    const src = collapsed(f);
    for (const m of src.matchAll(/registerCommand\( *([A-Za-z_$][\w.$]*) *[,)]/g)) {
      out.push(path.relative(ROOT, f) + ": registerCommand(" + m[1] + ", …)");
    }
    for (const m of src.matchAll(/registerCommand\( *`([^`]*\$\{[^`]*)`/g)) {
      out.push(path.relative(ROOT, f) + ": registerCommand(`" + m[1] + "`, …)");
    }
  }
  return out;
}

// ── (1) declared commands and registered commands are the same set ─────────────────────────────

test("every declared command is registered, and every registration is declared", () => {
  const declared = manifest().contributes.commands.map((c) => c.command);
  const regs = registrations();

  // ⚠ VACUITY FLOORS FIRST. A manifest that failed to parse, or a walk that found no sources, makes
  // every comparison below trivially true — "no unregistered commands" and "no commands" are the
  // same output otherwise.
  assert.ok(
    declared.length >= 20,
    `contributes.commands has ${declared.length} entries; 24 at 2054864. Read the manifest before trusting any result here.`,
  );
  assert.ok(
    sourceFiles().length >= 50,
    `the src/ walk found ${sourceFiles().length} non-test files; 73 at 2054864.`,
  );

  const unresolvable = unresolvableRegistrations();
  assert.deepEqual(
    unresolvable,
    [],
    `registerCommand is called with a non-literal id, which no literal scan can resolve:\n  ${unresolvable.join("\n  ")}\n\n` +
      "Scoring the matching command as unregistered would be a false positive, and scoring it as " +
      "registered would weaken this guard silently. Either use a literal, or teach this file how to " +
      "resolve the id and say so here.",
  );

  const dupDeclared = declared.filter((c, i) => declared.indexOf(c) !== i);
  assert.deepEqual(
    dupDeclared,
    [],
    `contributes.commands declares the same id twice: ${dupDeclared.join(", ")}. VS Code shows one Palette entry, so a duplicate hides an edit somebody meant to make.`,
  );
  const ids = regs.map((r) => r.id);
  const dupRegistered = ids.filter((c, i) => ids.indexOf(c) !== i);
  assert.deepEqual(
    dupRegistered,
    [],
    `registerCommand is called twice for: ${dupRegistered.join(", ")}. The second call throws at activation and takes the rest of activate() with it.`,
  );

  const D = new Set(declared);
  const R = new Set(ids);
  const unregistered = [...D].filter((c) => !R.has(c)).sort();
  const undeclared = [...R].filter((c) => !D.has(c)).sort();

  assert.deepEqual(
    unregistered,
    [],
    `${unregistered.length} command(s) are declared in package.json and registered by nothing in src/:\n  ${unregistered.join("\n  ")}\n\n` +
      "Each is a Command Palette entry that answers \"command not found\" when a user picks it. " +
      "VS Code raises that at invocation, not at load, so this ships green without the guard.",
  );
  assert.deepEqual(
    undeclared,
    [],
    `${undeclared.length} command(s) are registered in src/ and declared by nothing in package.json:\n  ${undeclared.join("\n  ")}\n\n` +
      "The handler exists and works, and no user can find it — the Palette lists only what the " +
      "manifest declares. Declare it, or stop registering it.",
  );
});

// ── (2) keybindings and menus name commands that exist on both sides ───────────────────────────

test("every keybinding and menu entry names a declared, registered command", () => {
  const c = manifest().contributes;
  const D = new Set(c.commands.map((x) => x.command));
  const R = new Set(registrations().map((r) => r.id));

  const refs: { where: string; id: string }[] = [];
  for (const k of c.keybindings ?? []) {
    refs.push({ where: `keybindings[${k.key ?? "?"}]`, id: k.command });
  }
  for (const [group, items] of Object.entries(c.menus ?? {})) {
    for (const it of items) {
      if (it.command) refs.push({ where: `menus.${group}`, id: it.command });
    }
  }

  // ⚠ ARMING FLOOR: with no references this test cannot fail whatever the manifest says.
  assert.ok(
    refs.length >= 10,
    `found ${refs.length} keybinding/menu command references; 16 at 2054864 (6 keybindings + 10 editor/context entries).`,
  );

  const broken = refs
    .filter((r) => !D.has(r.id) || !R.has(r.id))
    .map(
      (r) =>
        `${r.where} -> ${r.id} (${D.has(r.id) ? "declared" : "NOT DECLARED"}, ${R.has(r.id) ? "registered" : "NOT REGISTERED"})`,
    );
  assert.deepEqual(
    broken,
    [],
    `${broken.length} keybinding/menu entr(ies) point at a command that is not both declared and registered:\n  ${broken.join("\n  ")}\n\n` +
      "A context-menu item or a key that resolves to nothing is worse than an absent one: the user " +
      "is told the feature exists.",
  );
});

// ── (3) every declared setting is actually read or written ─────────────────────────────────────

test("every declared configuration property is read or written by the code", () => {
  const props = Object.keys(manifest().contributes.configuration.properties);
  assert.ok(
    props.length >= 10,
    `contributes.configuration declares ${props.length} properties; 12 at 2054864.`,
  );

  // Keys used with a literal, on a chain rooted at getConfiguration("talyvor").
  const literal = new Set<string>();
  for (const f of sourceFiles()) {
    const src = collapsed(f);
    const vars = new Set<string>();
    for (const m of src.matchAll(
      new RegExp(String.raw`(?:const|let|var) (\w+) ?= ?` + CFG_CHAIN, "g"),
    )) {
      vars.add(m[1]);
    }
    const roots = [CFG_CHAIN, ...[...vars].map((v) => `\\b${v}\\b`)].join("|");
    for (const m of src.matchAll(
      new RegExp(`(?:${roots}) ?\\. ?(?:get|update)(?:<[^>]*>)? *\\( *["']([^"']+)["']`, "g"),
    )) {
      literal.add(m[1]);
    }
  }

  // ⚠ THE INDIRECT PATH IS ACCOUNTED FOR, NOT WAIVED. The four credentials are never read with a
  // literal key: `TalyvorConfig.secret(cfg, key)` and the plaintext store both take the key as a
  // VARIABLE, driven by SECRET_KEYS. Allowing them unconditionally would excuse any future setting
  // somebody added to that array, so the code that consumes SECRET_KEYS with a variable key must
  // still be PRESENT for the waiver to apply.
  const configSrc = collapsed(path.join(ROOT, "src", "config.ts"));
  const indirectPathExists =
    /secretCache\[key\] \|\| cfg\.get<string>\(key, ""\)/.test(configSrc) &&
    /getConfiguration\(SECTION\)\.get<string>\(k, ""\)/.test(configSrc);
  assert.ok(
    indirectPathExists,
    "src/config.ts no longer contains the variable-key credential read this test waives " +
      "SECRET_KEYS on. Either it moved — repoint this check — or the credentials are no longer " +
      "read from configuration at all, in which case those four settings are now genuinely inert " +
      "and must be re-scored rather than waived.",
  );
  const indirect = new Set<string>(SECRET_KEYS as readonly string[]);

  const inert = props
    .map((p) => p.replace(/^talyvor\./, ""))
    .filter((k) => !literal.has(k) && !indirect.has(k))
    .sort();
  assert.deepEqual(
    inert,
    [],
    `${inert.length} declared setting(s) are read by nothing:\n  ${inert.join("\n  ")}\n\n` +
      "Each renders as a row in the Settings UI, with a description and a default, that changes " +
      "nothing when a user edits it. Wire it, remove it, or — if it is read through a variable key " +
      "like the SECRET_KEYS credentials — extend the indirect-path check above with the new route.",
  );

  // The mirror: a key the code reads that the manifest never declares takes its fallback default
  // forever, because no user can set what the Settings UI does not list.
  const undeclared = [...literal]
    .filter((k) => !props.includes("talyvor." + k))
    .sort();
  assert.deepEqual(
    undeclared,
    [],
    `${undeclared.length} configuration key(s) are read from the talyvor section and declared by nothing:\n  ${undeclared.join("\n  ")}\n\n` +
      "There is no way for a user to set them, so each is permanently its hardcoded fallback — a " +
      "value that reads as configurable and is structural.",
  );
});
