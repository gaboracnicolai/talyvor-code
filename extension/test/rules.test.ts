// Smoke tests for the rules pure helpers. Validates the parser,
// section combinators, and prompt envelope without needing a
// vscode runtime.

import {
  forAgent,
  forLanguage,
  forReview,
  forTesting,
  MAX_RULES_FILE_BYTES,
  parseRules,
  promptPrefix,
  RULES_FILE_NAME,
  type Rules,
} from "../src/rules/rules-pure";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

const sample = `[general]
Write clean, idiomatic code.
Prefer explicit error handling.

[go]
Use table-driven tests.

[typescript]
No 'any' types without comment.

[testing]
Write tests first.

[agent]
Make atomic changes.

[review]
Flag hardcoded secrets.
`;

function makeRules(): Rules {
  return { raw: sample, filePath: "/repo/.talyvor-rules", sections: parseRules(sample) };
}

// ─── parseRules ─────────────────────────────────────

function testParseExtractsGeneral(): void {
  const s = parseRules(sample);
  assert(s.general.includes("idiomatic"), "general: " + s.general);
  assert(s.general.includes("error handling"), "general second line");
}

function testParseExtractsLanguageSections(): void {
  const s = parseRules(sample);
  assert(s.languages["go"].includes("table-driven"), "go: " + s.languages["go"]);
  assert(s.languages["typescript"].includes("any"), "ts: " + s.languages["typescript"]);
}

function testParseCaseInsensitive(): void {
  const s = parseRules("[GO]\nrule\n[TypeScript]\nx\n");
  assert(s.languages["go"]?.includes("rule"), "lowercased go");
  assert(s.languages["typescript"]?.includes("x"), "lowercased typescript");
}

function testParseSkipsCommentsAndEmpty(): void {
  const s = parseRules("# top comment\n[general]\n# inner comment\n\nbody line\n[go]\n   \n");
  assert(s.general === "body line", "general body: " + s.general);
  assert(!("go" in s.languages), "empty [go] should be dropped");
}

function testParseUnknownSectionGoesToLanguages(): void {
  // Any non-built-in section name lands in the languages bucket
  // since that's the open-ended slot.
  const s = parseRules("[python]\nuse type hints\n");
  assert(s.languages["python"]?.includes("type hints"), "python kept");
}

function testParseEmptyReturnsEmptySections(): void {
  const s = parseRules("");
  assert(s.general === "" && Object.keys(s.languages).length === 0, "all empty");
}

// ─── combinators ────────────────────────────────────

function testForLanguageCombines(): void {
  const r = makeRules();
  const out = forLanguage(r, "go");
  assert(out.includes("idiomatic"), "general included");
  assert(out.includes("table-driven"), "go included");
}

function testForLanguageNilReturnsEmpty(): void {
  assert(forLanguage(undefined, "go") === "", "undefined → empty");
}

function testForLanguageUnknownFallsBackToGeneral(): void {
  const r = makeRules();
  const out = forLanguage(r, "haskell");
  assert(out.includes("idiomatic"), "general still included");
  assert(!out.includes("table-driven"), "go not included");
}

function testForAgentCombines(): void {
  const out = forAgent(makeRules());
  assert(out.includes("idiomatic") && out.includes("atomic changes"), "both sections");
}

function testForReviewCombines(): void {
  const out = forReview(makeRules());
  assert(out.includes("Flag hardcoded secrets"), "review included");
}

function testForTestingCombines(): void {
  const out = forTesting(makeRules(), "go");
  for (const want of ["idiomatic", "tests first", "table-driven"]) {
    assert(out.includes(want), "missing " + want);
  }
}

// ─── promptPrefix ───────────────────────────────────

function testPromptPrefixFormats(): void {
  const out = promptPrefix("Rule one.\nRule two.");
  assert(out.startsWith("Project rules"), "starts with header");
  assert(out.includes("---"), "has delimiter");
  assert(out.endsWith("\n\n"), "trailing blank line");
}

function testPromptPrefixEmpty(): void {
  assert(promptPrefix("") === "", "empty → empty");
  assert(promptPrefix("   \n  ") === "", "whitespace-only → empty");
}

// ─── constants ──────────────────────────────────────

function testConstants(): void {
  assert(RULES_FILE_NAME === ".talyvor-rules", "file name");
  assert(MAX_RULES_FILE_BYTES === 32 * 1024, "size cap");
}

async function main(): Promise<void> {
  testParseExtractsGeneral();
  testParseExtractsLanguageSections();
  testParseCaseInsensitive();
  testParseSkipsCommentsAndEmpty();
  testParseUnknownSectionGoesToLanguages();
  testParseEmptyReturnsEmptySections();
  testForLanguageCombines();
  testForLanguageNilReturnsEmpty();
  testForLanguageUnknownFallsBackToGeneral();
  testForAgentCombines();
  testForReviewCombines();
  testForTestingCombines();
  testPromptPrefixFormats();
  testPromptPrefixEmpty();
  testConstants();
  // eslint-disable-next-line no-console
  console.log("ok (15 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
