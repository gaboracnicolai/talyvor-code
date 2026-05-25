// Smoke tests for the heal pure helpers. Validates the build-
// system detector, prompt builder, and defensive JSON parser
// without needing a vscode runtime or a real filesystem.

import {
  buildHealPrompt,
  detectBuildCommandFromMarkers,
  MAX_HEAL_ATTEMPTS,
  parseHealResult,
} from "../src/agent/heal-pure";
import { allowedTransitions, canTransition } from "../src/agent/agent-pure";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

// ─── detectBuildCommandFromMarkers ────────────────

function testDetectGoMod(): void {
  const plan = detectBuildCommandFromMarkers(new Set(["go.mod"]), false);
  assert(plan?.command === "go build ./...", "go cmd");
  assert(plan?.language === "go", "go lang");
}

function testDetectCargo(): void {
  const plan = detectBuildCommandFromMarkers(new Set(["Cargo.toml"]), false);
  assert(plan?.command === "cargo check", "cargo cmd");
  assert(plan?.language === "rust", "rust lang");
}

function testDetectPython(): void {
  const plan = detectBuildCommandFromMarkers(new Set(["requirements.txt"]), false);
  assert(plan?.command === "python -m pytest", "python cmd");
  assert(plan?.language === "python", "python lang");
}

function testDetectGemfile(): void {
  const plan = detectBuildCommandFromMarkers(new Set(["Gemfile"]), false);
  assert(plan?.command === "bundle exec rspec", "ruby cmd");
  assert(plan?.language === "ruby", "ruby lang");
}

function testDetectPackageJSONWithoutTestSkips(): void {
  const plan = detectBuildCommandFromMarkers(new Set(["package.json"]), false);
  assert(plan === undefined, "bare package.json should skip");
}

function testDetectPackageJSONWithTestPicksJS(): void {
  const plan = detectBuildCommandFromMarkers(new Set(["package.json"]), true);
  assert(plan?.command === "npm test", "npm test cmd");
  assert(plan?.language === "javascript", "JS lang");
}

function testDetectTSConfigPromotesToTS(): void {
  const plan = detectBuildCommandFromMarkers(
    new Set(["package.json", "tsconfig.json"]),
    true,
  );
  assert(plan?.language === "typescript", "TS lang when tsconfig present");
}

function testDetectNoMarkersReturnsUndefined(): void {
  const plan = detectBuildCommandFromMarkers(new Set(["README.md"]), false);
  assert(plan === undefined, "no markers → undefined");
}

function testDetectGoBeatsPackageJSON(): void {
  // A monorepo with a Go module + a tooling package.json should
  // still resolve to the Go build, because that's the load-
  // bearing build system for the change set.
  const plan = detectBuildCommandFromMarkers(
    new Set(["go.mod", "package.json"]),
    true,
  );
  assert(plan?.command === "go build ./...", "go takes priority");
}

// ─── buildHealPrompt ──────────────────────────────

function testBuildHealPromptIncludesAllContext(): void {
  const p = buildHealPrompt({
    taskDescription: "Add JWT auth",
    failedCommand: "go build ./...",
    errorOutput: "undefined: jwt.Verify",
    changedFiles: ["auth.go", "middleware.go"],
    language: "go",
    attempt: 2,
  });
  for (const want of [
    "Add JWT auth",
    "go build ./...",
    "undefined: jwt.Verify",
    "auth.go",
    "middleware.go",
    "Attempt: 2 of 3",
    "JSON array",
  ]) {
    assert(p.includes(want), `prompt missing ${want}`);
  }
}

function testMaxAttemptsConstant(): void {
  assert(MAX_HEAL_ATTEMPTS === 3, "spec pins cap at 3");
}

// ─── parseHealResult ──────────────────────────────

function testParseValidJSON(): void {
  const out = parseHealResult(`[
    {"file":"a.ts","content":"export {};"},
    {"file":"b.ts","content":"export const x = 1;"}
  ]`);
  assert(out.length === 2, "two fixes");
  assert(out[0].file === "a.ts", "file 0");
  assert(out[1].content.includes("export const x"), "content 1");
}

function testParseStripsMarkdownFences(): void {
  const out = parseHealResult(
    "```json\n[{\"file\":\"a.go\",\"content\":\"package a\"}]\n```",
  );
  assert(out.length === 1 && out[0].file === "a.go", "fence stripped");
}

function testParseSalvagesFromProse(): void {
  const out = parseHealResult(
    `Sure, here are the fixes:\n\n[{"file":"a.go","content":"package a"}]\n\nThat should compile now.`,
  );
  assert(out.length === 1 && out[0].file === "a.go", "salvaged inner JSON");
}

function testParseHandlesEmptyArray(): void {
  const out = parseHealResult("[]");
  assert(out.length === 0, "empty array yields zero fixes");
}

function testParseDropsIncompleteEntries(): void {
  const out = parseHealResult(`[
    {"file":"good.ts","content":"export {};"},
    {"file":"","content":"x"},
    {"file":"no-content.ts"}
  ]`);
  assert(out.length === 1 && out[0].file === "good.ts", "incomplete dropped");
}

function testParseRejectsInvalidJSON(): void {
  let threw = false;
  try {
    parseHealResult("not json at all");
  } catch (err) {
    threw = true;
    assert(/invalid JSON/i.test(String(err)) || /heal:/i.test(String(err)), "error mentions JSON");
  }
  assert(threw, "should throw on invalid JSON");
}

// ─── state machine ────────────────────────────────

function testHealingTransitionAllowed(): void {
  assert(canTransition("applying", "healing"), "applying → healing");
  assert(canTransition("healing", "completed"), "healing → completed");
  assert(canTransition("healing", "failed"), "healing → failed");
  assert(canTransition("healing", "cancelled"), "healing → cancelled");
  // Healing must not jump backwards into the apply phase.
  assert(!canTransition("healing", "applying"), "healing ↛ applying");
  // The terminal states still have no outbound edges.
  for (const terminal of ["completed", "failed", "cancelled"] as const) {
    assert(allowedTransitions[terminal].length === 0, terminal + " terminal");
  }
}

async function main(): Promise<void> {
  testDetectGoMod();
  testDetectCargo();
  testDetectPython();
  testDetectGemfile();
  testDetectPackageJSONWithoutTestSkips();
  testDetectPackageJSONWithTestPicksJS();
  testDetectTSConfigPromotesToTS();
  testDetectNoMarkersReturnsUndefined();
  testDetectGoBeatsPackageJSON();
  testBuildHealPromptIncludesAllContext();
  testMaxAttemptsConstant();
  testParseValidJSON();
  testParseStripsMarkdownFences();
  testParseSalvagesFromProse();
  testParseHandlesEmptyArray();
  testParseDropsIncompleteEntries();
  testParseRejectsInvalidJSON();
  testHealingTransitionAllowed();
  // eslint-disable-next-line no-console
  console.log("ok (18 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
