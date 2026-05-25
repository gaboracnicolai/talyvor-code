// Smoke tests for the scope pure helpers. Covers JSON parsing,
// glob matching (including `**`), file filtering, and prompt
// rendering. Mirrors the Go-side coverage in
// agent/internal/scope/scope_test.go.

import {
  ACTIVE_SCOPE_FILE_NAME,
  MAX_SCOPES,
  SCOPES_FILE_NAME,
  filterFiles,
  isValidScopeKey,
  matchAny,
  matchGlob,
  parseScopes,
  toPromptSection,
  type Scope,
} from "../src/scope/scope-pure";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

const sampleScopes = `{
  "auth": {
    "name": "Authentication",
    "includes": ["internal/auth/**", "cmd/auth/**"],
    "excludes": ["**/*_test.go"],
    "focus": "JWT authentication and session management"
  },
  "api": {
    "name": "API Layer",
    "includes": ["internal/api/**"],
    "focus": "REST endpoints"
  }
}`;

// ─── parseScopes ──────────────────────────────────

function testParseValidScopes(): void {
  const cat = parseScopes(sampleScopes);
  assert(cat !== undefined, "parse returned undefined");
  assert(Object.keys(cat!).length === 2, "two scopes");
  const auth = cat!["auth"];
  assert(auth.name === "Authentication", "name");
  assert(auth.includes.length === 2, "includes count");
  assert(auth.excludes.includes("**/*_test.go"), "excludes preserved");
  assert(auth.focus.includes("JWT"), "focus");
}

function testParseEmptyReturnsEmptyCatalogue(): void {
  const cat = parseScopes("");
  assert(cat !== undefined && Object.keys(cat).length === 0, "empty → empty catalogue");
}

function testParseInvalidJSONReturnsUndefined(): void {
  assert(parseScopes("not json") === undefined, "bad JSON → undefined");
}

function testParseSkipsInvalidKeys(): void {
  const cat = parseScopes(`{
    "Bad Key!": {"includes":["**"]},
    "good": {"includes":["src/**"]}
  }`);
  assert(cat !== undefined, "parsed");
  assert(cat!["good"] !== undefined, "good kept");
  assert(cat!["Bad Key!"] === undefined, "invalid key dropped");
}

function testParseSkipsScopesWithoutIncludes(): void {
  const cat = parseScopes(`{
    "no-include": {"name": "x"},
    "ok": {"includes": ["**"]}
  }`);
  assert(cat !== undefined && cat!["no-include"] === undefined, "include-less dropped");
  assert(cat!["ok"] !== undefined, "ok kept");
}

function testIsValidScopeKey(): void {
  for (const k of ["auth", "api-v2", "frontend-2024"]) {
    assert(isValidScopeKey(k), "should be valid: " + k);
  }
  for (const k of ["Auth", "with space", "-leading", "", "ümlaut"]) {
    assert(!isValidScopeKey(k), "should be invalid: " + JSON.stringify(k));
  }
}

// ─── matchGlob ────────────────────────────────────

function testMatchGlobLiteral(): void {
  assert(matchGlob("src/auth.go", "src/auth.go"), "literal");
}

function testMatchGlobStarOneSegment(): void {
  assert(matchGlob("src/*.go", "src/auth.go"), "* one segment");
  assert(!matchGlob("src/*.go", "src/sub/x.go"), "* should not cross /");
}

function testMatchGlobDoubleStar(): void {
  const cases: Array<[string, string, boolean]> = [
    ["internal/auth/**", "internal/auth/jwt.go", true],
    ["internal/auth/**", "internal/auth/jwt/handler.go", true],
    ["internal/auth/**", "internal/api/server.go", false],
    ["**/*_test.go", "src/sub/auth_test.go", true],
    ["**/*_test.go", "src/auth.go", false],
    ["**/handlers/**", "src/api/handlers/v1/foo.go", true],
    ["**/handlers/**", "src/api/foo.go", false],
  ];
  for (const [pat, path, want] of cases) {
    const got = matchGlob(pat, path);
    assert(got === want, `matchGlob(${pat}, ${path}) = ${got}, want ${want}`);
  }
}

function testMatchGlobBackslashNormalisation(): void {
  // Patterns are forward-slash; paths can come in with backslashes
  // on Windows. The matcher normalises both sides.
  assert(matchGlob("internal/auth/**", "internal\\auth\\jwt.go"), "backslash path");
}

function testMatchAnyShortcircuits(): void {
  assert(matchAny(["src/*.ts", "internal/auth/**"], "internal/auth/jwt.go"), "second pattern wins");
  assert(!matchAny(["src/*.ts"], "internal/auth/jwt.go"), "no pattern matches");
}

// ─── filterFiles ──────────────────────────────────

function testFilterFiles(): void {
  const scope: Scope = {
    name: "Auth",
    includes: ["internal/auth/**", "cmd/auth/**"],
    excludes: ["**/*_test.go"],
    focus: "",
  };
  const files = [
    "internal/auth/jwt.go",
    "internal/auth/jwt_test.go",
    "cmd/auth/main.go",
    "internal/api/router.go",
  ];
  const got = filterFiles(files, scope);
  assert(got.length === 2, "two should survive (test + non-auth filtered): " + got.join(","));
  assert(got.includes("internal/auth/jwt.go"), "auth/jwt.go kept");
  assert(got.includes("cmd/auth/main.go"), "cmd/auth kept");
  assert(!got.includes("internal/auth/jwt_test.go"), "test excluded");
  assert(!got.includes("internal/api/router.go"), "non-auth dropped");
}

function testFilterFilesPassThroughWhenNoScope(): void {
  const files = ["a.go", "b.go"];
  assert(filterFiles(files, undefined).length === 2, "pass-through");
}

// ─── toPromptSection ──────────────────────────────

function testToPromptSectionEmptyWhenNoScope(): void {
  assert(toPromptSection(undefined) === "", "no scope → empty");
}

function testToPromptSectionRenders(): void {
  const scope: Scope = {
    name: "Authentication",
    includes: ["internal/auth/**"],
    excludes: [],
    focus: "JWT and sessions",
  };
  const out = toPromptSection(scope, "auth");
  for (const want of [
    "Active scope: Authentication",
    "Focus: JWT and sessions",
    "Included files: internal/auth/**",
  ]) {
    assert(out.includes(want), "missing " + want);
  }
}

function testToPromptSectionFallsBackToKeyForName(): void {
  const scope: Scope = {
    name: "",
    includes: ["src/**"],
    excludes: [],
    focus: "",
  };
  const out = toPromptSection(scope, "src");
  assert(out.includes("Active scope: src"), "key fallback: " + out);
}

// ─── constants ────────────────────────────────────

function testConstants(): void {
  assert(SCOPES_FILE_NAME === ".talyvor-scopes", "scopes file name");
  assert(ACTIVE_SCOPE_FILE_NAME === ".talyvor-active-scope", "active scope file name");
  assert(MAX_SCOPES === 20, "max scopes");
}

async function main(): Promise<void> {
  testParseValidScopes();
  testParseEmptyReturnsEmptyCatalogue();
  testParseInvalidJSONReturnsUndefined();
  testParseSkipsInvalidKeys();
  testParseSkipsScopesWithoutIncludes();
  testIsValidScopeKey();
  testMatchGlobLiteral();
  testMatchGlobStarOneSegment();
  testMatchGlobDoubleStar();
  testMatchGlobBackslashNormalisation();
  testMatchAnyShortcircuits();
  testFilterFiles();
  testFilterFilesPassThroughWhenNoScope();
  testToPromptSectionEmptyWhenNoScope();
  testToPromptSectionRenders();
  testToPromptSectionFallsBackToKeyForName();
  testConstants();
  // eslint-disable-next-line no-console
  console.log("ok (17 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
