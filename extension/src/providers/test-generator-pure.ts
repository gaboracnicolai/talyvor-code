// Pure helpers for the test generator. vscode-free so the test
// runner can exercise the language-specific prompt + file-naming
// rules without an Electron host.

export interface FrameworkInfo {
  language: string;     // canonical language id
  framework: string;    // jest / pytest / go-testing / etc.
  outputName: (sourceFile: string) => string;
}

// suggestTestFileName maps a source file path to the conventional
// test-file companion for its language. The path part is preserved
// (only the basename changes) so the new file lands next to the
// source in the same directory.
export function suggestTestFileName(
  sourcePath: string,
  languageId: string,
): string {
  const { dir, base, ext } = splitPath(sourcePath);
  const stem = base.endsWith(ext) ? base.substring(0, base.length - ext.length) : base;
  switch (languageId) {
    case "typescript":
      return joinPath(dir, `${stem}.test.ts`);
    case "typescriptreact":
      return joinPath(dir, `${stem}.test.tsx`);
    case "javascript":
      return joinPath(dir, `${stem}.test.js`);
    case "javascriptreact":
      return joinPath(dir, `${stem}.test.jsx`);
    case "go":
      return joinPath(dir, `${stem}_test.go`);
    case "python":
      return joinPath(dir, `test_${stem}.py`);
    case "ruby":
      return joinPath(dir, `${stem}_spec.rb`);
    case "rust":
      // Rust convention is in-file #[cfg(test)] modules; we surface
      // a sibling file when the source lives in src/ so the suggested
      // path still makes sense to users.
      return joinPath(dir, `${stem}_test.rs`);
    case "java":
      // PascalCase -> PascalCaseTest
      return joinPath(dir, `${pascal(stem)}Test.java`);
    case "kotlin":
      return joinPath(dir, `${pascal(stem)}Test.kt`);
    case "swift":
      return joinPath(dir, `${pascal(stem)}Tests.swift`);
    case "c":
      return joinPath(dir, `${stem}_test.c`);
    case "cpp":
      return joinPath(dir, `${stem}_test.cpp`);
    default:
      return joinPath(dir, `${stem}.test${ext || ""}`);
  }
}

// frameworkFor returns the canonical framework label per language.
// Used for the TestPanel header + the agent's "wrote N lines"
// summary so the user knows what they got.
export function frameworkFor(languageId: string): string {
  switch (languageId) {
    case "typescript":
    case "typescriptreact":
    case "javascript":
    case "javascriptreact":
      return "Jest";
    case "go":
      return "Go testing";
    case "python":
      return "pytest";
    case "ruby":
      return "RSpec";
    case "rust":
      return "Rust #[cfg(test)]";
    case "java":
      return "JUnit";
    case "kotlin":
      return "JUnit (Kotlin)";
    case "swift":
      return "XCTest";
    case "c":
    case "cpp":
      return "Generic test harness";
  }
  return "Generic";
}

// systemPromptFor returns the language-tailored system prompt the
// generator sends to Lens. We deliberately keep them short — the
// model already knows the basics; the prompt's job is to pin the
// framework + "return only the code" expectation.
export function systemPromptFor(languageId: string): string {
  switch (languageId) {
    case "typescript":
    case "typescriptreact":
    case "javascript":
    case "javascriptreact":
      return (
        "Generate Jest tests for the following code. Use describe/it blocks. " +
        "Include happy-path, edge-case, and error-case tests. Use TypeScript " +
        "syntax when the source is TypeScript. Import the module correctly. " +
        "Return ONLY the test code — no prose, no fences, no explanation."
      );
    case "go":
      return (
        "Generate Go tests using the standard `testing` package. Prefer " +
        "table-driven tests when there are multiple cases. Name tests " +
        "Test<FunctionName>. Use t.Helper() in shared fixtures. Return " +
        "ONLY the test code — no prose, no fences, no explanation."
      );
    case "python":
      return (
        "Generate pytest tests. Use descriptive test_* function names + " +
        "fixtures where they make the suite cleaner. Cover happy path, " +
        "edge cases, and error cases. Return ONLY the test code — no prose, " +
        "no fences, no explanation."
      );
    case "ruby":
      return (
        "Generate RSpec tests. Use describe/context/it blocks. Return ONLY " +
        "the test code — no prose, no fences, no explanation."
      );
    case "rust":
      return (
        "Generate a Rust #[cfg(test)] module covering the supplied code. " +
        "Use #[test] functions and assert!/assert_eq!. Return ONLY the " +
        "test code — no prose, no fences, no explanation."
      );
    case "java":
    case "kotlin":
      return (
        "Generate JUnit 5 tests. One @Test method per scenario; use " +
        "@DisplayName when the method name doesn't convey intent. Return " +
        "ONLY the test code — no prose, no fences, no explanation."
      );
    case "swift":
      return (
        "Generate XCTest tests. Inherit from XCTestCase; use test* method " +
        "names. Return ONLY the test code — no prose, no fences, no " +
        "explanation."
      );
  }
  return (
    "Generate a thorough test suite for the following code using the " +
    "idiomatic testing framework for the language. Return ONLY the test " +
    "code — no prose, no fences, no explanation."
  );
}

// buildTestPrompt is the user-side payload that follows the system
// prompt. The language fence helps the model lock onto syntax.
export function buildTestPrompt(
  code: string,
  languageId: string,
  fileName: string,
): string {
  return [
    `Generate tests for this ${languageId} file:`,
    `File: ${fileName}`,
    "",
    "```" + languageId,
    code,
    "```",
  ].join("\n");
}

// sanitiseGenerated strips the boilerplate models like to add even
// when told not to: leading code fences, trailing fences,
// "Here are the tests:" preludes. Preserves internal whitespace so
// indentation survives.
export function sanitiseGenerated(text: string): string {
  let out = text;
  // Strip "Here are the tests:" / "Tests:" preambles. Optional
  // trailing colon + flexible whitespace so the regex catches
  // both "Here are the tests:\n…" and "Tests\n…".
  out = out.replace(/^\s*(here (are|is) (the )?tests?|tests?)\s*:?\s*\n+/i, "");
  // Drop a leading code fence (with optional language).
  const open = out.match(/^\s*```[a-zA-Z0-9_+-]*\n/);
  if (open) out = out.substring(open[0].length);
  // Drop a trailing closing fence.
  const close = out.match(/\n```\s*$/);
  if (close) out = out.substring(0, out.length - close[0].length);
  return out.trimStart().replace(/\s+$/, "\n");
}

// ─── tiny path helpers ─────────────────────────────

// splitPath returns directory, basename, and extension. Works for
// both POSIX and Windows separators so the suggested test file
// lands next to the source on either platform.
function splitPath(p: string): { dir: string; base: string; ext: string } {
  const sepIdx = Math.max(p.lastIndexOf("/"), p.lastIndexOf("\\"));
  const dir = sepIdx >= 0 ? p.substring(0, sepIdx) : "";
  const base = sepIdx >= 0 ? p.substring(sepIdx + 1) : p;
  const dot = base.lastIndexOf(".");
  const ext = dot > 0 ? base.substring(dot) : "";
  return { dir, base, ext };
}

function joinPath(dir: string, file: string): string {
  if (!dir) return file;
  const sep = dir.includes("\\") && !dir.includes("/") ? "\\" : "/";
  return dir + sep + file;
}

function pascal(s: string): string {
  return s
    .split(/[-_]+/)
    .filter(Boolean)
    .map((w) => w.charAt(0).toUpperCase() + w.substring(1))
    .join("");
}
