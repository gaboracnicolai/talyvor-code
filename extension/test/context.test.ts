// Smoke tests for the context pure helpers. Validates the JSON
// parser, the tiny YAML subset parser, prompt rendering, the
// validator, and the rules+context combinator. Mirrors the
// Go-side coverage in agent/internal/projectctx/loader_test.go.

import {
  CONTEXT_FILE_NAME,
  MAX_CONTEXT_FILE_BYTES,
  MAX_CONTEXT_PROMPT_BYTES,
  combinedPrefix,
  parseContext,
  toPromptSection,
  validate,
  type ProjectContext,
} from "../src/context/context-pure";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

const sampleJSON = `{
  "name": "MyApp",
  "description": "B2B e-commerce platform serving enterprise customers",
  "stack": ["Go", "PostgreSQL", "Redis", "React", "TypeScript"],
  "architecture": "Microservices with gRPC internal communication",
  "conventions": {
    "database": "Use pgx driver, never database/sql",
    "errors": "Always wrap errors with fmt.Errorf('context: %w', err)"
  },
  "key_files": ["cmd/server/main.go", "internal/api/router.go"],
  "team_size": 5,
  "links": { "docs": "https://docs.myapp.com" }
}`;

const sampleYAML = `name: MyApp
description: B2B e-commerce platform serving enterprise customers
stack:
  - Go
  - PostgreSQL
  - React
architecture: Microservices with gRPC
conventions:
  database: Use pgx driver
  errors: Wrap with fmt.Errorf
key_files:
  - cmd/server/main.go
  - internal/api/router.go
team_size: 5
links:
  docs: https://docs.myapp.com
`;

// ─── parseContext ─────────────────────────────────

function testParseJSON(): void {
  const pc = parseContext(sampleJSON);
  assert(pc !== undefined, "json parse returned undefined");
  assert(pc!.name === "MyApp", "name");
  assert(pc!.stack.length === 5 && pc!.stack[0] === "Go", "stack");
  assert(pc!.conventions["database"].includes("pgx"), "convention");
  assert(pc!.team_size === 5, "team_size");
}

function testParseYAML(): void {
  const pc = parseContext(sampleYAML);
  assert(pc !== undefined, "yaml parse returned undefined");
  assert(pc!.name === "MyApp", "name");
  assert(pc!.stack.length === 3 && pc!.stack[1] === "PostgreSQL", "stack");
  assert(pc!.key_files[0] === "cmd/server/main.go", "key_files");
  assert(pc!.conventions["database"].includes("pgx"), "convention");
}

function testParseEmptyReturnsUndefined(): void {
  assert(parseContext("") === undefined, "empty");
  assert(parseContext("   \n  ") === undefined, "whitespace");
}

function testParseInvalidJSONReturnsUndefined(): void {
  // Starts with { but malformed — fall through to undefined.
  assert(parseContext("{ not json") === undefined, "malformed json");
}

function testParseYAMLSubset(): void {
  // Minimal YAML — covers the documented schema.
  const out = parseContext("name: Bare\nstack:\n  - Go\n");
  assert(out !== undefined, "subset parsed");
  assert(out!.name === "Bare", "name");
  assert(out!.stack[0] === "Go", "stack[0]");
}

// ─── toPromptSection ─────────────────────────────

function testToPromptSectionRenders(): void {
  const pc = parseContext(sampleJSON)!;
  const out = toPromptSection(pc);
  for (const want of [
    "Project context",
    "Name: MyApp",
    "Stack: Go, PostgreSQL",
    "Architecture: Microservices",
    "database: Use pgx",
    "Key files:",
    "cmd/server/main.go",
  ]) {
    assert(out.includes(want), "missing " + want);
  }
}

function testToPromptSectionNilSafe(): void {
  assert(toPromptSection(undefined) === "", "undefined → empty");
}

function testToPromptSectionRespectsCap(): void {
  const huge: ProjectContext = {
    name: "X",
    description: "a".repeat(5000),
    stack: ["Go"],
    architecture: "",
    conventions: {},
    key_files: [],
  };
  const out = toPromptSection(huge);
  assert(out.length <= MAX_CONTEXT_PROMPT_BYTES, `len ${out.length}`);
}

// ─── validate ────────────────────────────────────

function testValidateFlagsMissingName(): void {
  const pc: ProjectContext = {
    name: "",
    description: "a long enough description here ok",
    stack: ["Go"],
    architecture: "",
    conventions: {},
    key_files: [],
  };
  const warns = validate(pc);
  assert(warns.some((w) => w.toLowerCase().includes("name")), "name warn");
}

function testValidateFlagsShortDescription(): void {
  const pc: ProjectContext = {
    name: "X",
    description: "too short",
    stack: ["Go"],
    architecture: "",
    conventions: {},
    key_files: [],
  };
  const warns = validate(pc);
  assert(warns.some((w) => w.toLowerCase().includes("description")), "desc warn");
}

function testValidateFlagsEmptyStack(): void {
  const pc: ProjectContext = {
    name: "X",
    description: "long enough description here OK",
    stack: [],
    architecture: "",
    conventions: {},
    key_files: [],
  };
  const warns = validate(pc);
  assert(warns.some((w) => w.toLowerCase().includes("stack")), "stack warn");
}

function testValidateAcceptsGoodContext(): void {
  const pc = parseContext(sampleJSON)!;
  assert(validate(pc).length === 0, "good context should validate cleanly");
}

// ─── combinedPrefix ──────────────────────────────

function testCombinedPrefixEmpty(): void {
  assert(combinedPrefix("", undefined) === "", "both empty");
}

function testCombinedPrefixOrdersRulesBeforeContext(): void {
  const pc = parseContext(sampleJSON);
  const out = combinedPrefix("RULES PREFIX BLOCK\n", pc);
  const rulesAt = out.indexOf("RULES PREFIX");
  const ctxAt = out.indexOf("Project context");
  assert(rulesAt >= 0 && ctxAt >= 0, "both present");
  assert(rulesAt < ctxAt, "rules before context");
}

// ─── constants ───────────────────────────────────

function testConstants(): void {
  assert(CONTEXT_FILE_NAME === ".talyvor-context", "file name");
  assert(MAX_CONTEXT_FILE_BYTES === 64 * 1024, "file cap");
  assert(MAX_CONTEXT_PROMPT_BYTES === 2000, "prompt cap");
}

async function main(): Promise<void> {
  testParseJSON();
  testParseYAML();
  testParseEmptyReturnsUndefined();
  testParseInvalidJSONReturnsUndefined();
  testParseYAMLSubset();
  testToPromptSectionRenders();
  testToPromptSectionNilSafe();
  testToPromptSectionRespectsCap();
  testValidateFlagsMissingName();
  testValidateFlagsShortDescription();
  testValidateFlagsEmptyStack();
  testValidateAcceptsGoodContext();
  testCombinedPrefixEmpty();
  testCombinedPrefixOrdersRulesBeforeContext();
  testConstants();
  // eslint-disable-next-line no-console
  console.log("ok (15 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
