// Smoke tests for the PR-review pure helpers. Covers verdict
// extraction, finding counts, diff truncation, prompt assembly,
// and badge mapping. Mirrors the Go-side coverage in
// agent/internal/github/review_test.go.

import {
  MAX_DIFF_CHARS,
  buildPRReviewSystemPrompt,
  buildPRReviewUserMessage,
  countFindings,
  extractVerdict,
  truncateDiff,
  verdictBadge,
} from "../src/commands/pr-review-pure";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

// ─── extractVerdict ───────────────────────────────

function testExtractApprove(): void {
  assert(extractVerdict("## Verdict\nAPPROVE") === "APPROVE", "approve");
}

function testExtractRequestChanges(): void {
  assert(extractVerdict("## Verdict\nREQUEST CHANGES") === "REQUEST CHANGES", "rc");
  assert(
    extractVerdict("Verdict: REQUEST CHANGES — two critical issues") === "REQUEST CHANGES",
    "rc inline",
  );
}

function testExtractRequestChangesBeatsApprove(): void {
  // Real blocker beats a polite "approve apart from this"
  const body = "approved overall but REQUEST CHANGES for the SQL injection";
  assert(extractVerdict(body) === "REQUEST CHANGES", "rc beats approve");
}

function testExtractNeedsDiscussion(): void {
  assert(
    extractVerdict("## Verdict\nNEEDS DISCUSSION about the migration") === "NEEDS DISCUSSION",
    "needs disc",
  );
}

function testExtractDefaultsToNeedsDiscussion(): void {
  assert(extractVerdict("nothing matched here") === "NEEDS DISCUSSION", "default");
  assert(extractVerdict("") === "NEEDS DISCUSSION", "empty");
}

// ─── verdictBadge ─────────────────────────────────

function testVerdictBadges(): void {
  assert(verdictBadge("APPROVE").color === "#5cd187", "approve color");
  assert(verdictBadge("REQUEST CHANGES").color === "#ff7070", "rc color");
  assert(verdictBadge("NEEDS DISCUSSION").color === "#f0a030", "needs disc color");
  assert(verdictBadge("APPROVE").emoji === "✅", "approve emoji");
}

// ─── countFindings ────────────────────────────────

function testCountFindings(): void {
  const review = `## Review

### 🔴 Critical Issues
- SQL injection in auth.go
- Hardcoded token

### 🟡 Warnings
- N+1 query
- Missing error handling

### 💡 Suggestions
- rename helper
`;
  const { critical, warning } = countFindings(review);
  assert(critical === 2, "critical = " + critical);
  assert(warning === 2, "warning = " + warning);
}

function testCountFindingsNoneIsZero(): void {
  const review = `### 🔴 Critical Issues
None.
### 🟡 Warnings
- one
`;
  const { critical, warning } = countFindings(review);
  assert(critical === 0, "critical none");
  assert(warning === 1, "warning one");
}

function testCountFindingsHandlesEmojiBullets(): void {
  const review = `### 🔴 Critical Issues
- 🔴 SQL injection
- 🔴 XSS in render
`;
  const { critical } = countFindings(review);
  assert(critical === 2, "emoji bullets counted: " + critical);
}

// ─── truncateDiff ─────────────────────────────────

function testTruncateDiffPassThrough(): void {
  const diff = "a".repeat(500);
  assert(truncateDiff(diff, 1000) === diff, "small diff passthrough");
}

function testTruncateDiffSplitsAndMarks(): void {
  const diff = "a".repeat(1000) + "MIDDLE" + "z".repeat(1000);
  const out = truncateDiff(diff, 200);
  assert(out.includes("[diff truncated"), "marker present");
  assert(out.startsWith("aaa"), "head preserved");
  assert(out.endsWith("z".repeat(50)) || out.endsWith("z".repeat(100)), "tail preserved");
}

function testMaxDiffCharsConstant(): void {
  assert(MAX_DIFF_CHARS === 32000, "spec pins MAX_DIFF_CHARS at 32000");
}

// ─── prompt assembly ──────────────────────────────

function testBuildSystemPromptHasAllSections(): void {
  const p = buildPRReviewSystemPrompt("general");
  for (const want of [
    "PR Summary",
    "🔴 Critical Issues",
    "🟡 Warnings",
    "💡 Suggestions",
    "✅ Good Patterns",
    "Verdict",
    "APPROVE / REQUEST CHANGES / NEEDS DISCUSSION",
  ]) {
    assert(p.includes(want), "missing " + want);
  }
}

function testBuildSystemPromptShiftsFocusForSecurity(): void {
  const p = buildPRReviewSystemPrompt("security");
  assert(p.includes("injection") && p.includes("CSRF"), "security focus");
}

function testBuildUserMessageIncludesCommitsAndFiles(): void {
  const msg = buildPRReviewUserMessage(
    "SYSTEM",
    "DIFF BODY",
    ["feat: add jwt", "fix: lint"],
    ["src/auth.ts", "src/server.ts"],
  );
  assert(msg.includes("Commits:"), "commits heading");
  assert(msg.includes("- feat: add jwt"), "commit line");
  assert(msg.includes("Files changed:"), "files heading");
  assert(msg.includes("- src/auth.ts"), "file line");
  assert(msg.includes("Diff:"), "diff heading");
  assert(msg.endsWith("DIFF BODY"), "diff body at tail");
  assert(msg.startsWith("SYSTEM"), "system at head");
}

function testBuildUserMessageOmitsEmptySections(): void {
  const msg = buildPRReviewUserMessage("SYS", "DIFF", [], []);
  assert(!msg.includes("Commits:"), "omit empty commits");
  assert(!msg.includes("Files changed:"), "omit empty files");
}

async function main(): Promise<void> {
  testExtractApprove();
  testExtractRequestChanges();
  testExtractRequestChangesBeatsApprove();
  testExtractNeedsDiscussion();
  testExtractDefaultsToNeedsDiscussion();
  testVerdictBadges();
  testCountFindings();
  testCountFindingsNoneIsZero();
  testCountFindingsHandlesEmojiBullets();
  testTruncateDiffPassThrough();
  testTruncateDiffSplitsAndMarks();
  testMaxDiffCharsConstant();
  testBuildSystemPromptHasAllSections();
  testBuildSystemPromptShiftsFocusForSecurity();
  testBuildUserMessageIncludesCommitsAndFiles();
  testBuildUserMessageOmitsEmptySections();
  // eslint-disable-next-line no-console
  console.log("ok (16 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
