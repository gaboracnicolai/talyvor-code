// Smoke tests for the agent pure helpers. Exercises the plan
// parser, status state-machine matrix, and unified-diff renderer
// without needing a vscode runtime.

import {
  MAX_FILES_PER_TASK,
  allowedTransitions,
  canTransition,
  parsePlan,
  renderUnifiedDiff,
  type AgentStatus,
} from "../src/agent/agent-pure";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

// ─── parsePlan ─────────────────────────────────────

function testParsePlanParsesValidJSON(): void {
  const raw = JSON.stringify({
    plan: ["read auth.ts", "add JWT middleware"],
    files: [
      { path: "src/middleware/jwt.ts", operation: "create", description: "jwt verifier" },
      { path: "src/server.ts", operation: "modify", description: "wire middleware" },
    ],
  });
  const got = parsePlan(raw);
  assert(got.plan.length === 2, "plan length");
  assert(got.files.length === 2, "files length");
  assert(got.files[0].operation === "create", "op preserved");
  assert(got.files[1].path === "src/server.ts", "path preserved");
}

function testParsePlanStripsMarkdownFence(): void {
  const raw =
    "```json\n" +
    JSON.stringify({ plan: ["a"], files: [{ path: "x", operation: "delete", description: "" }] }) +
    "\n```";
  const got = parsePlan(raw);
  assert(got.plan[0] === "a", "plan parsed through fence");
  assert(got.files[0].operation === "delete", "op parsed through fence");
}

function testParsePlanDropsUnknownOperations(): void {
  // The planner might hallucinate an op like "rename"; we silently
  // drop those files rather than crashing.
  const raw = JSON.stringify({
    plan: ["x"],
    files: [
      { path: "good.ts", operation: "modify", description: "" },
      { path: "bad.ts", operation: "rename", description: "" },
    ],
  });
  const got = parsePlan(raw);
  assert(got.files.length === 1, "kept only the valid op");
  assert(got.files[0].path === "good.ts", "kept the right file");
}

function testParsePlanRejectsOverCap(): void {
  const files = Array.from({ length: MAX_FILES_PER_TASK + 1 }, (_, i) => ({
    path: `f${i}.ts`,
    operation: "modify" as const,
    description: "",
  }));
  const raw = JSON.stringify({ plan: [], files });
  let threw = false;
  try {
    parsePlan(raw);
  } catch (err) {
    threw = true;
    assert(/exceeds MAX_FILES_PER_TASK/i.test(String(err)), "error mentions cap");
  }
  assert(threw, "parsePlan should reject over-cap payloads");
}

function testParsePlanRejectsInvalidJSON(): void {
  let threw = false;
  try {
    parsePlan("not json");
  } catch (err) {
    threw = true;
    assert(/invalid JSON/i.test(String(err)), "error mentions invalid JSON");
  }
  assert(threw, "parsePlan should reject non-JSON");
}

// ─── canTransition ─────────────────────────────────

function testTransitionsHappyPath(): void {
  // The advertised lifecycle the agent walks in the success case.
  const path: AgentStatus[] = [
    "idle",
    "planning",
    "executing",
    "awaiting_approval",
    "applying",
    "completed",
  ];
  for (let i = 0; i < path.length - 1; i++) {
    assert(
      canTransition(path[i], path[i + 1]),
      `${path[i]} → ${path[i + 1]} should be allowed`,
    );
  }
}

function testTransitionsRejectsIllegal(): void {
  assert(!canTransition("idle", "applying"), "cannot skip planning");
  assert(!canTransition("completed", "planning"), "terminal completed");
  assert(!canTransition("failed", "planning"), "terminal failed");
  assert(!canTransition("cancelled", "applying"), "terminal cancelled");
  assert(!canTransition("applying", "cancelled"), "no cancel mid-write");
}

function testTransitionsTerminalStatesHaveNoExits(): void {
  for (const terminal of ["completed", "failed", "cancelled"] as const) {
    assert(
      allowedTransitions[terminal].length === 0,
      `${terminal} should have no outbound transitions`,
    );
  }
}

// ─── renderUnifiedDiff ─────────────────────────────

function testDiffEmptyForIdenticalInputs(): void {
  const out = renderUnifiedDiff("same\nlines\n", "same\nlines\n");
  assert(out.length === 0, "identical inputs return empty diff");
}

function testDiffEmitsAddAndRemove(): void {
  const original = "a\nb\nc\n";
  const modified = "a\nB\nc\n";
  const out = renderUnifiedDiff(original, modified);
  const adds = out.filter((l) => l.kind === "add").map((l) => l.text);
  const removes = out.filter((l) => l.kind === "remove").map((l) => l.text);
  assert(removes.includes("b"), "missing the - line");
  assert(adds.includes("B"), "missing the + line");
  assert(out.some((l) => l.kind === "header"), "missing @@ header");
}

function testDiffHeaderShape(): void {
  const out = renderUnifiedDiff("a\n", "b\n");
  const hdr = out.find((l) => l.kind === "header");
  assert(hdr !== undefined, "expected a header line");
  // The shape is `@@ -1,N +1,M @@` for a single-hunk change at
  // file start.
  assert(/^@@ -\d+,\d+ \+\d+,\d+ @@$/.test(hdr!.text), "header shape mismatch: " + hdr!.text);
}

function testDiffPureCreationShowsOnlyAdds(): void {
  const out = renderUnifiedDiff("", "alpha\nbeta\n");
  const kinds = new Set(out.map((l) => l.kind));
  assert(kinds.has("add"), "expected add lines");
  assert(!kinds.has("remove"), "creation diff should not show removes");
}

function testDiffPureDeletionShowsOnlyRemoves(): void {
  const out = renderUnifiedDiff("alpha\nbeta\n", "");
  const kinds = new Set(out.map((l) => l.kind));
  assert(kinds.has("remove"), "expected remove lines");
  assert(!kinds.has("add"), "deletion diff should not show adds");
}

async function main(): Promise<void> {
  testParsePlanParsesValidJSON();
  testParsePlanStripsMarkdownFence();
  testParsePlanDropsUnknownOperations();
  testParsePlanRejectsOverCap();
  testParsePlanRejectsInvalidJSON();
  testTransitionsHappyPath();
  testTransitionsRejectsIllegal();
  testTransitionsTerminalStatesHaveNoExits();
  testDiffEmptyForIdenticalInputs();
  testDiffEmitsAddAndRemove();
  testDiffHeaderShape();
  testDiffPureCreationShowsOnlyAdds();
  testDiffPureDeletionShowsOnlyRemoves();
  // eslint-disable-next-line no-console
  console.log("ok (13 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
