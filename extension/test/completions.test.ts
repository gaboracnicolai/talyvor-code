// Smoke tests for the pure helpers in providers/context.ts and
// providers/cost-tracker.ts. We can import them directly because
// neither pulls in the vscode runtime — the vscode-typed
// getCodeContext lives in the same file but is excluded here.

import {
  buildCompletionPrompt,
  isCompletionTrigger,
  type CodeContext,
} from "../src/providers/context-pure";
import {
  CostTracker,
  estimateCostUSD,
  formatDuration,
} from "../src/providers/cost-tracker";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

function fakeCtx(over: Partial<CodeContext> = {}): CodeContext {
  return {
    prefix: "function add(a, b) {\n",
    suffix: "\n}",
    currentLine: "  return ",
    languageId: "typescript",
    fileName: "math.ts",
    filePath: "/repo/math.ts",
    workspaceRoot: "/repo",
    ...over,
  };
}

// ─── buildCompletionPrompt ──────────────────────────

function testPromptIncludesLanguageAndFile(): void {
  const out = buildCompletionPrompt(fakeCtx());
  assert(out.includes("Language: typescript"), "missing language");
  assert(out.includes("File: math.ts"), "missing file");
  assert(out.includes("[CURSOR]"), "missing cursor sentinel");
}

function testPromptOrdersPrefixThenCurrentThenSuffix(): void {
  const out = buildCompletionPrompt(fakeCtx());
  // prefix, currentLine, [CURSOR], suffix should appear in order.
  const a = out.indexOf("function add");
  const b = out.indexOf("return");
  const c = out.indexOf("[CURSOR]");
  const d = out.indexOf("}");
  assert(a < b && b < c && c < d, "out-of-order assembly: " + out);
}

// ─── isCompletionTrigger ────────────────────────────

function testIsCompletionTriggerInsideStringReturnsFalse(): void {
  // Cursor inside an unterminated double-quoted string.
  assert(
    !isCompletionTrigger('const x = "hello ', 17),
    "inside string should not trigger",
  );
}

function testIsCompletionTriggerAllowsPlainCode(): void {
  assert(
    isCompletionTrigger("  return ", 9),
    "plain code should trigger",
  );
}

function testIsCompletionTriggerRejectsAfterImport(): void {
  assert(
    !isCompletionTrigger("import ", 7),
    "after 'import ' should not trigger",
  );
}

function testIsCompletionTriggerInsideCommentReturnsFalse(): void {
  assert(
    !isCompletionTrigger("// just a comment", 17),
    "inside plain comment should not trigger",
  );
}

function testIsCompletionTriggerAllowsTODOComments(): void {
  assert(
    isCompletionTrigger("// TODO: handle the empty case", 30),
    "TODO comment should trigger",
  );
}

// ─── CostTracker ────────────────────────────────────

function testTrackerAccumulates(): void {
  const t = new CostTracker();
  t.recordCompletion(100, 0.01, "ENG-42");
  t.recordCompletion(50, 0.005, "ENG-42");
  const s = t.getSessionSummary();
  assert(s.completionCount === 2, "count = " + s.completionCount);
  assert(s.totalTokens === 150, "tokens = " + s.totalTokens);
  assert(Math.abs(s.totalCostUSD - 0.015) < 1e-9, "cost = " + s.totalCostUSD);
}

function testTrackerBucketsByIssue(): void {
  const t = new CostTracker();
  t.recordCompletion(10, 0.001, "ENG-42");
  t.recordCompletion(20, 0.002, "ENG-43");
  t.recordCompletion(30, 0.003, "ENG-42");
  const s = t.getSessionSummary();
  assert(Math.abs(s.byIssue["ENG-42"] - 0.004) < 1e-9, "42 = " + s.byIssue["ENG-42"]);
  assert(Math.abs(s.byIssue["ENG-43"] - 0.002) < 1e-9, "43 = " + s.byIssue["ENG-43"]);
}

function testTrackerEmptyIssueBucket(): void {
  const t = new CostTracker();
  t.recordCompletion(10, 0.001, "");
  assert(t.getSessionSummary().byIssue["(no issue)"] === 0.001, "missing fallback");
}

function testEstimateCostUSDIsMonotonic(): void {
  assert(estimateCostUSD(0, 0) === 0, "zero base");
  assert(estimateCostUSD(1000, 0) > 0, "input contributes");
  assert(
    estimateCostUSD(1000, 1000) > estimateCostUSD(1000, 0),
    "output contributes",
  );
}

function testFormatDuration(): void {
  const a = new Date(0);
  assert(formatDuration(a, new Date(45_000)) === "45s", "45s");
  assert(formatDuration(a, new Date(95_000)) === "1m", "1m");
  assert(formatDuration(a, new Date(3_600_000)) === "1h", "1h");
  assert(
    formatDuration(a, new Date(3_600_000 + 23 * 60_000)) === "1h 23m",
    "1h 23m",
  );
}

async function main(): Promise<void> {
  testPromptIncludesLanguageAndFile();
  testPromptOrdersPrefixThenCurrentThenSuffix();
  testIsCompletionTriggerInsideStringReturnsFalse();
  testIsCompletionTriggerAllowsPlainCode();
  testIsCompletionTriggerRejectsAfterImport();
  testIsCompletionTriggerInsideCommentReturnsFalse();
  testIsCompletionTriggerAllowsTODOComments();
  testTrackerAccumulates();
  testTrackerBucketsByIssue();
  testTrackerEmptyIssueBucket();
  testEstimateCostUSDIsMonotonic();
  testFormatDuration();
  // eslint-disable-next-line no-console
  console.log("ok (12 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
