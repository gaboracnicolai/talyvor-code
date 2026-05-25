// Smoke tests for chat-pure helpers — message-history trimming,
// system-prompt construction, code-block splitting.

import {
  MAX_HISTORY,
  buildSystemPrompt,
  escapeHTML,
  splitBlocks,
  trimHistory,
} from "../src/panels/chat-pure";
import type { Message } from "../src/lens/types";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

function pair(n: number): Message[] {
  const out: Message[] = [];
  for (let i = 0; i < n; i++) {
    out.push({ role: "user", content: `q${i}` });
    out.push({ role: "assistant", content: `a${i}` });
  }
  return out;
}

// ─── trimHistory ───────────────────────────────────

function testTrimUnderLimitPassThrough(): void {
  const h = pair(5); // 10 messages
  const out = trimHistory(h, 20);
  assert(out.length === 10, "expected 10, got " + out.length);
}

function testTrimAtLimitPassThrough(): void {
  const h = pair(10); // 20 messages
  const out = trimHistory(h, 20);
  assert(out.length === 20, "expected 20, got " + out.length);
}

function testTrimOverLimitDropsOldestPair(): void {
  const h = pair(11); // 22 messages, over by 2
  const out = trimHistory(h, 20);
  assert(out.length === 20, "expected 20, got " + out.length);
  // Oldest pair (q0/a0) should be gone; q1 is the new head.
  assert(out[0].content === "q1", "head wrong: " + out[0].content);
}

function testTrimOverLimitOddDropsPair(): void {
  // 21 messages → overflow 1, but we drop in pairs so we end at 19.
  const h: Message[] = [...pair(10), { role: "user", content: "trailing" }];
  const out = trimHistory(h, 20);
  // Drop count = 1 + (1 % 2) = 2 → length 19.
  assert(out.length === 19, "expected 19, got " + out.length);
}

function testMaxHistoryConstantMatchesSpec(): void {
  assert(MAX_HISTORY === 20, "MAX_HISTORY should be 20");
}

// ─── buildSystemPrompt ─────────────────────────────

function testSystemPromptIncludesIssue(): void {
  const p = buildSystemPrompt("ENG-42");
  assert(p.includes("ENG-42"), "missing issue: " + p);
}

function testSystemPromptHandlesEmptyIssue(): void {
  const p = buildSystemPrompt("");
  assert(p.includes("No active issue"), "missing fallback: " + p);
}

// ─── splitBlocks ───────────────────────────────────

function testSplitBlocksTextOnly(): void {
  const out = splitBlocks("just some prose");
  assert(out.length === 1 && out[0].kind === "text", "wrong: " + JSON.stringify(out));
}

function testSplitBlocksDetectsFenced(): void {
  const out = splitBlocks("here is code:\n```ts\nconst x = 1;\n```\nthat's it");
  assert(out.length === 3, "want 3 segments, got " + out.length);
  assert(out[0].kind === "text", "first should be text");
  assert(out[1].kind === "code", "second should be code");
  if (out[1].kind === "code") {
    assert(out[1].code.language === "ts", "lang = " + out[1].code.language);
    assert(out[1].code.code === "const x = 1;", "body = " + out[1].code.code);
  }
  assert(out[2].kind === "text", "third should be text");
}

function testSplitBlocksMultipleFences(): void {
  const out = splitBlocks("```js\nA\n```\nmiddle\n```py\nB\n```");
  const codes = out.filter((s) => s.kind === "code");
  assert(codes.length === 2, "want 2 code blocks, got " + codes.length);
}

function testSplitBlocksUnterminatedFenceCapturesTail(): void {
  // Unterminated fence — the trailing text should still surface
  // so the user sees the partial answer.
  const out = splitBlocks("```go\nfunc main() {\n  fmt.Println");
  const codes = out.filter((s) => s.kind === "code");
  assert(codes.length === 1, "expected 1 code block from unterminated fence");
  if (codes[0].kind === "code") {
    assert(
      codes[0].code.code.includes("fmt.Println"),
      "tail missing from unterminated fence",
    );
  }
}

// ─── escapeHTML ────────────────────────────────────

function testEscapeHTML(): void {
  assert(
    escapeHTML(`<script>alert("x")</script>`) ===
      "&lt;script&gt;alert(&quot;x&quot;)&lt;/script&gt;",
    "escape failed",
  );
}

async function main(): Promise<void> {
  testTrimUnderLimitPassThrough();
  testTrimAtLimitPassThrough();
  testTrimOverLimitDropsOldestPair();
  testTrimOverLimitOddDropsPair();
  testMaxHistoryConstantMatchesSpec();
  testSystemPromptIncludesIssue();
  testSystemPromptHandlesEmptyIssue();
  testSplitBlocksTextOnly();
  testSplitBlocksDetectsFenced();
  testSplitBlocksMultipleFences();
  testSplitBlocksUnterminatedFenceCapturesTail();
  testEscapeHTML();
  // eslint-disable-next-line no-console
  console.log("ok (12 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
