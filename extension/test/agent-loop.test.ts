// Iterative agent-loop tests — the TS twin of the Go internal/agentloop suite
// (loop_test.go / termination_test.go). Runs headlessly under `npm test` (the CI
// harness): a scripted Model stub + stub Tools drive the observe/act loop with NO
// vscode and NO filesystem, so every orchestration property is proven in CI.
//
// Self-contained runner convention (matches test/agent.test.ts): plain functions +
// main() + process.exit(1) on failure.

import {
  Agent,
  Registry,
  StopReason,
  parseToolCall,
  type Message,
  type Model,
  type Tool,
} from "../src/agent/loop-pure";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

// scriptModel replays a fixed sequence of replies and RECORDS the messages it saw
// each turn — so a test can prove the loop fed a tool observation back into the next
// turn's context. After the script is exhausted it emits `done`.
class ScriptModel implements Model {
  seen: Message[][] = [];
  private i = 0;
  constructor(private readonly replies: string[]) {}
  async complete(messages: Message[]): Promise<string> {
    this.seen.push(messages.map((m) => ({ ...m })));
    if (this.i >= this.replies.length) {
      return `{"thought":"finished","tool":"done","args":{"summary":"done"}}`;
    }
    return this.replies[this.i++];
  }
}

// funcModel adapts a per-turn function to Model (for budget/no-progress loops).
class FuncModel implements Model {
  private n = 0;
  constructor(private readonly fn: (n: number) => string) {}
  async complete(): Promise<string> {
    this.n++;
    return this.fn(this.n);
  }
}

// stubTool returns a fixed observation (or one derived from its raw args), with no
// filesystem — enough to exercise the loop's dispatch/observe/track mechanics.
function stubTool(
  name: string,
  run: (argsRaw: string) => Promise<string> | string,
): Tool {
  return {
    name: () => name,
    description: () => `${name} {…}`,
    run: async (argsRaw: string) => run(argsRaw),
  };
}

function stubTools(...tools: Tool[]): Registry {
  const reg = new Registry();
  for (const t of tools) reg.register(t);
  return reg;
}

// ─── parseToolCall ─────────────────────────────────────

function testParseToolCall_PlainJSON(): void {
  const tc = parseToolCall(`{"thought":"x","tool":"read_file","args":{"path":"a.go"}}`);
  assert(tc.tool === "read_file", "tool parsed");
  assert(tc.argsRaw.includes("a.go"), "args captured as raw JSON");
}

function testParseToolCall_StripsFencesAndProse(): void {
  const tc = parseToolCall("Sure!\n```json\n{\"tool\":\"done\",\"args\":{\"summary\":\"s\"}}\n```");
  assert(tc.tool === "done", "tool parsed through fence + prose");
}

function testParseToolCall_ThrowsOnNoTool(): void {
  let threw = false;
  try {
    parseToolCall(`{"thought":"x","args":{}}`);
  } catch {
    threw = true;
  }
  assert(threw, "a reply with no tool field must throw");
}

// ─── core observe/act ──────────────────────────────────

// The core mechanic: a tool's result becomes an OBSERVATION in the next turn's
// context and the loop advances. Proven by the model SEEING the observation on turn 2.
async function testLoop_DispatchesObservesAdvances(): Promise<void> {
  const model = new ScriptModel([
    `{"thought":"read it","tool":"read_file","args":{"path":"x.go"}}`,
    `{"thought":"done","tool":"done","args":{"summary":"read x.go"}}`,
  ]);
  const tools = stubTools(stubTool("read_file", () => "package x\n// OBSERVED_CONTENT\n"));
  const res = await new Agent(model, tools, { maxSteps: 10 }).run("look at x.go");

  assert(res.done && res.stop === StopReason.Done, "expected clean done");
  assert(res.steps === 2, `expected 2 steps (read → done); got ${res.steps}`);
  assert(model.seen.length >= 2, "model called ≥2 times");
  const turn2 = model.seen[1];
  const sawObs = turn2.some((m) => m.content.includes("OBSERVED_CONTENT"));
  assert(sawObs, "turn-2 context must contain the read_file observation (loop feeds results back)");
}

// Bounded: a model that never finishes stops at MaxSteps (distinct args each turn so
// the no-progress detector doesn't pre-empt the budget path).
async function testLoop_StopsOnBudget(): Promise<void> {
  const model = new FuncModel((n) => `{"tool":"read_file","args":{"path":"missing_${n}.go"}}`);
  const tools = stubTools(stubTool("read_file", () => "not found"));
  const res = await new Agent(model, tools, { maxSteps: 4 }).run("loop");
  assert(res.stop === StopReason.Budget && !res.done, `expected budget stop; got ${res.stop}`);
  assert(res.steps === 4, `expected exactly MaxSteps=4; got ${res.steps}`);
}

// A malformed reply is fed back as an error observation WITH a format hint so the
// model can correct itself; the loop does not crash.
async function testLoop_RecoversFromBadFormat(): Promise<void> {
  const model = new ScriptModel([
    `I will now read the file.`, // not JSON
    `{"tool":"done","args":{"summary":"ok"}}`,
  ]);
  const res = await new Agent(model, stubTools(), { maxSteps: 10 }).run("task");
  assert(res.done, "loop must recover from a bad-format turn and still finish");
  const turn2 = model.seen[1];
  assert(turn2.some((m) => m.content.includes("JSON")), "malformed reply must be fed back with a JSON hint");
}

// ─── termination / no-progress ─────────────────────────

// THE overnight-safety property: the same edit every turn aborts via no-progress at
// the 3rd identical call (MaxRepeat=2), NOT after burning the whole budget.
async function testLoop_NoProgress_IdenticalEdit(): Promise<void> {
  const model = new FuncModel(() => `{"tool":"edit_file","args":{"path":"a.go","content":"package a\n"}}`);
  const tools = stubTools(stubTool("edit_file", () => "edited a.go"));
  const res = await new Agent(model, tools, { maxSteps: 30, maxRepeat: 2 }).run("loop");
  assert(res.stop === StopReason.NoProgress, `identical edit must stop as no-progress; got ${res.stop}`);
  assert(res.steps === 3, `identical edit must abort at step 3; got ${res.steps}`);
}

// edit → failing run → IDENTICAL edit … must abort as no-progress at step 5.
async function testLoop_NoProgress_EditFailCycle(): Promise<void> {
  const model = new FuncModel((n) =>
    n % 2 === 1
      ? `{"tool":"edit_file","args":{"path":"x.go","content":"package x\nfunc Broken(){}\n"}}`
      : `{"tool":"run","args":{"cmd":"exit 1"}}`,
  );
  const tools = stubTools(
    stubTool("edit_file", () => "edited x.go"),
    stubTool("run", () => "$ exit 1\nexit 1\n"),
  );
  const res = await new Agent(model, tools, { maxSteps: 50, maxRepeat: 2 }).run("fix the build");
  assert(res.stop === StopReason.NoProgress, `edit→fail→identical-edit must stop as no-progress; got ${res.stop}`);
  assert(res.steps === 5, `expected abort at step 5; got ${res.steps}`);
  assert(res.steps < 50, "no-progress must abort BEFORE the budget");
}

// A model that never emits valid JSON can't loop forever either.
async function testLoop_NoProgress_Garbage(): Promise<void> {
  const model = new FuncModel(() => "sorry, I cannot do that");
  const res = await new Agent(model, stubTools(), { maxSteps: 30, maxRepeat: 2 }).run("loop");
  assert(res.stop === StopReason.NoProgress, `endless garbage must stop as no-progress; got ${res.stop}`);
  assert(res.steps < 30, `garbage must abort before the budget; got ${res.steps}`);
}

// A clean finish returns done + its summary.
async function testLoop_CleanDoneCarriesSummary(): Promise<void> {
  const model = new ScriptModel([
    `{"tool":"run","args":{"cmd":"echo ok"}}`,
    `{"tool":"done","args":{"summary":"verified: build passes"}}`,
  ]);
  const tools = stubTools(stubTool("run", () => "$ echo ok\nexit 0\nok\n"));
  const res = await new Agent(model, tools, { maxSteps: 10 }).run("task");
  assert(res.done && res.stop === StopReason.Done, "expected clean done");
  assert(res.summary === "verified: build passes", `done summary not captured; got ${res.summary}`);
}

// A hard model-call error stops the loop as StopError and surfaces the message —
// run() never throws (TS fork: the Go (Result, error) pair folds into Result.error).
async function testLoop_ModelErrorReported(): Promise<void> {
  const model: Model = { complete: async () => { throw new Error("lens down"); } };
  const res = await new Agent(model, stubTools(), { maxSteps: 5 }).run("task");
  assert(res.stop === StopReason.Error, `model error must stop as StopError; got ${res.stop}`);
  assert((res.error ?? "").includes("lens down"), "the model error message must be surfaced");
}

// A tool that throws is an OBSERVATION (the model re-plans), never a loop-killer.
async function testLoop_ToolErrorIsObservation(): Promise<void> {
  const model = new ScriptModel([
    `{"tool":"read_file","args":{"path":"nope.go"}}`,
    `{"tool":"done","args":{"summary":"recovered"}}`,
  ]);
  const tools = stubTools(stubTool("read_file", () => { throw new Error("ENOENT nope.go"); }));
  const res = await new Agent(model, tools, { maxSteps: 10 }).run("task");
  assert(res.done, "a tool error must not kill the loop");
  const turn2 = model.seen[1];
  assert(turn2.some((m) => m.content.includes("ENOENT")), "the tool error must be fed back as an observation");
}

// An unknown tool name is dispatched to a clear error observation; the model re-plans.
async function testLoop_UnknownToolIsObservation(): Promise<void> {
  const model = new ScriptModel([
    `{"tool":"teleport","args":{}}`,
    `{"tool":"done","args":{"summary":"ok"}}`,
  ]);
  const res = await new Agent(model, stubTools(stubTool("read_file", () => "x")), { maxSteps: 10 }).run("task");
  assert(res.done, "an unknown tool must not kill the loop");
  const turn2 = model.seen[1];
  assert(turn2.some((m) => m.content.includes("unknown tool")), "unknown tool must be fed back as an observation");
}

// edit_file calls are recorded (uniquely) in editedFiles.
async function testLoop_TracksEditedFilesUniquely(): Promise<void> {
  const model = new ScriptModel([
    `{"tool":"edit_file","args":{"path":"a.go","content":"1"}}`,
    `{"tool":"edit_file","args":{"path":"b.go","content":"2"}}`,
    `{"tool":"edit_file","args":{"path":"a.go","content":"3"}}`, // same path again
    `{"tool":"done","args":{"summary":"done"}}`,
  ]);
  const tools = stubTools(stubTool("edit_file", () => "edited"));
  const res = await new Agent(model, tools, { maxSteps: 10 }).run("task");
  assert(res.editedFiles.length === 2, `expected 2 unique edited files; got ${JSON.stringify(res.editedFiles)}`);
  assert(res.editedFiles.includes("a.go") && res.editedFiles.includes("b.go"), "both edited paths tracked");
}

// The transcript is trimmed to the context cap, always KEEPING the system message.
async function testLoop_TrimsTranscriptKeepingSystem(): Promise<void> {
  const model = new FuncModel((n) => `{"tool":"read_file","args":{"path":"f_${n}.go"}}`);
  const tools = stubTools(stubTool("read_file", () => "x"));
  const res = await new Agent(model, tools, { maxSteps: 20, maxTranscript: 8 }).run("loop");
  assert(res.transcript.length <= 8, `transcript must be trimmed to ≤ maxTranscript; got ${res.transcript.length}`);
  assert(res.transcript[0].role === "system", "the system message must be preserved at the head after trimming");
}

async function main(): Promise<void> {
  testParseToolCall_PlainJSON();
  testParseToolCall_StripsFencesAndProse();
  testParseToolCall_ThrowsOnNoTool();
  await testLoop_DispatchesObservesAdvances();
  await testLoop_StopsOnBudget();
  await testLoop_RecoversFromBadFormat();
  await testLoop_NoProgress_IdenticalEdit();
  await testLoop_NoProgress_EditFailCycle();
  await testLoop_NoProgress_Garbage();
  await testLoop_CleanDoneCarriesSummary();
  await testLoop_ModelErrorReported();
  await testLoop_ToolErrorIsObservation();
  await testLoop_UnknownToolIsObservation();
  await testLoop_TracksEditedFilesUniquely();
  await testLoop_TrimsTranscriptKeepingSystem();
  // eslint-disable-next-line no-console
  console.log("ok (16 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
