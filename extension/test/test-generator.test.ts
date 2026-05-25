// Smoke tests for the test-generator pure helpers. Validates the
// filename mapping, system prompt routing, and user-prompt
// assembly without needing a vscode runtime.

import {
  buildTestPrompt,
  frameworkFor,
  sanitiseGenerated,
  suggestTestFileName,
  systemPromptFor,
} from "../src/providers/test-generator-pure";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

// ─── suggestTestFileName ────────────────────────────

function testSuggestTSFile(): void {
  assert(
    suggestTestFileName("src/auth.ts", "typescript") === "src/auth.test.ts",
    "ts",
  );
  assert(
    suggestTestFileName("/repo/src/auth.tsx", "typescriptreact") ===
      "/repo/src/auth.test.tsx",
    "tsx",
  );
}

function testSuggestJSFile(): void {
  assert(
    suggestTestFileName("util.js", "javascript") === "util.test.js",
    "js",
  );
  assert(
    suggestTestFileName("App.jsx", "javascriptreact") === "App.test.jsx",
    "jsx",
  );
}

function testSuggestGoFile(): void {
  assert(
    suggestTestFileName("/x/foo.go", "go") === "/x/foo_test.go",
    "go",
  );
}

function testSuggestPythonFile(): void {
  assert(
    suggestTestFileName("pkg/auth.py", "python") === "pkg/test_auth.py",
    "py",
  );
}

function testSuggestRubyFile(): void {
  assert(
    suggestTestFileName("auth.rb", "ruby") === "auth_spec.rb",
    "rb",
  );
}

function testSuggestRustFile(): void {
  assert(
    suggestTestFileName("src/lib.rs", "rust") === "src/lib_test.rs",
    "rs",
  );
}

function testSuggestJavaFile(): void {
  assert(
    suggestTestFileName("src/Auth.java", "java") === "src/AuthTest.java",
    "java pascal",
  );
  assert(
    suggestTestFileName("auth_service.java", "java") === "AuthServiceTest.java",
    "java snake → pascal",
  );
}

function testSuggestSwiftFile(): void {
  assert(
    suggestTestFileName("auth.swift", "swift") === "AuthTests.swift",
    "swift",
  );
}

function testSuggestFallbackFile(): void {
  // Unknown language — drop a generic .test suffix before the
  // extension so the user at least gets a starting point.
  assert(
    suggestTestFileName("script.sh", "shellscript") === "script.test.sh",
    "fallback",
  );
}

function testSuggestWindowsSeparatorRetained(): void {
  // splitPath handles both separators so the suggestion lands in
  // the same directory regardless of platform style.
  assert(
    suggestTestFileName("C:\\repo\\src\\auth.ts", "typescript") ===
      "C:\\repo\\src\\auth.test.ts",
    "windows",
  );
}

// ─── frameworkFor ───────────────────────────────────

function testFrameworkFor(): void {
  assert(frameworkFor("typescript") === "Jest", "ts → Jest");
  assert(frameworkFor("go") === "Go testing", "go → Go testing");
  assert(frameworkFor("python") === "pytest", "py → pytest");
  assert(frameworkFor("java") === "JUnit", "java → JUnit");
  assert(frameworkFor("unknown") === "Generic", "fallback");
}

// ─── systemPromptFor ────────────────────────────────

function testSystemPromptForKnownLanguages(): void {
  assert(systemPromptFor("typescript").includes("Jest"), "ts mentions Jest");
  assert(systemPromptFor("go").includes("testing"), "go mentions testing");
  assert(systemPromptFor("python").includes("pytest"), "py mentions pytest");
  // Every prompt must include the "no prose, no fences" guard so
  // the sanitiser doesn't have to do all the work.
  for (const lang of ["typescript", "go", "python", "ruby", "rust", "java", "swift"]) {
    assert(
      /ONLY/i.test(systemPromptFor(lang)),
      `${lang} prompt missing ONLY guard`,
    );
  }
}

// ─── buildTestPrompt ────────────────────────────────

function testBuildTestPromptIncludesContext(): void {
  const code = "export function add(a: number, b: number) { return a + b }";
  const p = buildTestPrompt(code, "typescript", "math.ts");
  assert(p.includes("typescript"), "language tag missing");
  assert(p.includes("math.ts"), "file name missing");
  assert(p.includes(code), "code body missing");
  assert(p.includes("```typescript"), "code fence missing");
}

// ─── sanitiseGenerated ──────────────────────────────

function testSanitiseStripsLeadingFence(): void {
  const raw = "```ts\nconst x = 1;\n```";
  const out = sanitiseGenerated(raw);
  assert(!out.includes("```"), "fence still present: " + out);
  assert(out.includes("const x = 1;"), "body lost");
}

function testSanitiseStripsPreamble(): void {
  const raw = "Here are the tests:\n```ts\nconst x = 1;\n```";
  const out = sanitiseGenerated(raw);
  assert(!/here are the tests/i.test(out), "preamble survived");
}

function testSanitiseHandlesNoFences(): void {
  const raw = "const x = 1;\nconst y = 2;";
  const out = sanitiseGenerated(raw);
  assert(out.includes("const x = 1;"), "first line lost");
  assert(out.includes("const y = 2;"), "second line lost");
}

async function main(): Promise<void> {
  testSuggestTSFile();
  testSuggestJSFile();
  testSuggestGoFile();
  testSuggestPythonFile();
  testSuggestRubyFile();
  testSuggestRustFile();
  testSuggestJavaFile();
  testSuggestSwiftFile();
  testSuggestFallbackFile();
  testSuggestWindowsSeparatorRetained();
  testFrameworkFor();
  testSystemPromptForKnownLanguages();
  testBuildTestPromptIncludesContext();
  testSanitiseStripsLeadingFence();
  testSanitiseStripsPreamble();
  testSanitiseHandlesNoFences();
  // eslint-disable-next-line no-console
  console.log("ok (16 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
