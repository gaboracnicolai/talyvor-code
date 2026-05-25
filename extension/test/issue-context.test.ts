// Smoke tests for the issue-context pure helpers. Validates the
// identifier validator, context formatter, session-cost
// accumulator, sync-target filter, and agent comment builder
// without needing a vscode runtime.

import {
  accumulateSessionCost,
  buildAgentCompletionComment,
  CONTEXT_DESCRIPTION_CHARS,
  formatIssueContext,
  isValidIssueIdentifier,
  pickIssuesForSync,
} from "../src/track/issue-context-pure";
import type { TrackIssue } from "../src/track/client";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

function makeIssue(over: Partial<TrackIssue> = {}): TrackIssue {
  return {
    id: "i1",
    identifier: "ENG-42",
    title: "Authentication bug",
    status: "In Progress",
    description: "Users sometimes hit a 500 on login retry.",
    aiCostUsd: 0,
    ...over,
  };
}

// ─── isValidIssueIdentifier ────────────────────────

function testValidIdentifiers(): void {
  for (const s of ["ENG-42", "BUG-7", "FE-103", "A-1", "ABCDEFGH-999999"]) {
    assert(isValidIssueIdentifier(s), `expected valid: ${s}`);
  }
}

function testInvalidIdentifiers(): void {
  for (const s of [
    "",
    "eng-42",       // lowercase prefix
    "ENG42",        // missing dash
    "ENG-",         // missing number
    "-42",          // missing prefix
    "ABCDEFGHI-1",  // prefix too long
    "ENG-1234567",  // number too long
    "ENG-42-foo",   // suffix
    "(no issue)",   // sentinel bucket key
  ]) {
    assert(!isValidIssueIdentifier(s), `expected invalid: ${JSON.stringify(s)}`);
  }
}

// ─── formatIssueContext ────────────────────────────

function testFormatIssueContextWithIssue(): void {
  const out = formatIssueContext(makeIssue());
  assert(out.includes("ENG-42"), "missing identifier");
  assert(out.includes("Authentication bug"), "missing title");
  assert(out.includes("In Progress"), "missing status");
  assert(out.includes("Users sometimes hit a 500"), "missing description");
}

function testFormatIssueContextEmpty(): void {
  assert(formatIssueContext(undefined) === "", "undefined → empty");
  const noId = makeIssue({ identifier: "" });
  assert(formatIssueContext(noId) === "", "no id → empty");
}

function testFormatIssueContextTrimsLongDescription(): void {
  const long = "x".repeat(CONTEXT_DESCRIPTION_CHARS + 200);
  const out = formatIssueContext(makeIssue({ description: long }));
  // Trim point + ellipsis adds one char beyond the cap.
  assert(out.includes("x".repeat(CONTEXT_DESCRIPTION_CHARS)), "trim point");
  assert(out.includes("…"), "ellipsis marker");
  assert(!out.includes("x".repeat(CONTEXT_DESCRIPTION_CHARS + 10)), "extra chars dropped");
}

function testFormatIssueContextNoStatusOrDescription(): void {
  const out = formatIssueContext(
    makeIssue({ status: "", description: "" }),
  );
  assert(out.includes("ENG-42"), "id retained");
  assert(!out.includes("Status:"), "status omitted when blank");
  assert(!out.includes("Description:"), "description omitted when blank");
}

// ─── accumulateSessionCost ─────────────────────────

function testAccumulateSessionCost(): void {
  let m = new Map<string, number>();
  m = accumulateSessionCost(m, "ENG-42", 0.01);
  m = accumulateSessionCost(m, "ENG-42", 0.02);
  m = accumulateSessionCost(m, "ENG-43", 0.005);
  m = accumulateSessionCost(m, "", 0.001);
  assert(Math.abs((m.get("ENG-42") ?? 0) - 0.03) < 1e-9, "ENG-42 sum");
  assert(Math.abs((m.get("ENG-43") ?? 0) - 0.005) < 1e-9, "ENG-43 sum");
  assert(Math.abs((m.get("(no issue)") ?? 0) - 0.001) < 1e-9, "no-issue bucket");
}

function testAccumulateReturnsCopy(): void {
  const a = new Map<string, number>([["ENG-42", 0.01]]);
  const b = accumulateSessionCost(a, "ENG-42", 0.02);
  assert(a !== b, "must return a new map");
  assert(a.get("ENG-42") === 0.01, "original untouched");
  assert(b.get("ENG-42") === 0.03, "new map summed");
}

// ─── pickIssuesForSync ─────────────────────────────

function testPickIssuesForSyncFiltersBucket(): void {
  const m = new Map<string, number>([
    ["ENG-42", 0.05],
    ["(no issue)", 0.10],
    ["ENG-43", 0.0],         // below threshold
    ["bad", 0.20],            // not a valid identifier
  ]);
  const out = pickIssuesForSync(m);
  const ids = out.map((x) => x.issueId);
  assert(ids.includes("ENG-42"), "valid identifier kept");
  assert(!ids.includes("(no issue)"), "(no issue) dropped");
  assert(!ids.includes("ENG-43"), "below-threshold dropped");
  assert(!ids.includes("bad"), "invalid identifier dropped");
}

// ─── buildAgentCompletionComment ──────────────────

function testBuildAgentCompletionComment(): void {
  const out = buildAgentCompletionComment(
    "Add JWT authentication",
    3,
    0.0823,
    "claude-sonnet-4-6",
  );
  assert(out.includes("Talyvor Agent completed task: Add JWT authentication"), "missing header");
  assert(out.includes("Files changed: 3"), "missing file count");
  assert(out.includes("$0.0823"), "missing cost (4 decimals)");
  assert(out.includes("claude-sonnet-4-6"), "missing model");
}

async function main(): Promise<void> {
  testValidIdentifiers();
  testInvalidIdentifiers();
  testFormatIssueContextWithIssue();
  testFormatIssueContextEmpty();
  testFormatIssueContextTrimsLongDescription();
  testFormatIssueContextNoStatusOrDescription();
  testAccumulateSessionCost();
  testAccumulateReturnsCopy();
  testPickIssuesForSyncFiltersBucket();
  testBuildAgentCompletionComment();
  // eslint-disable-next-line no-console
  console.log("ok (10 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
