// Iterative orchestration — the vscode-free glue that turns a Lens client + a workspace
// root into a running agent: load the retriever (honest-absent), build the model +
// tools, run the loop. Headless-testable with a fake Lens (completeWithUsage + embed)
// and a temp workspace; the bound AgentMode.startIterativeTask is a thin wrapper over
// this (task-state + panel emit), documented for manual verification.

import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { StopReason } from "../src/agent/loop-pure";
import { runIterativeLoop } from "../src/agent/iterative-pure";
import { indexPath, INDEX_VERSION } from "../src/agent/retrieval-pure";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

function tmpRoot(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "tlv-iter-"));
}

// FakeLens scripts completeWithUsage replies and answers embed with a fixed vector.
class FakeLens {
  embedCalls = 0;
  private i = 0;
  constructor(private readonly replies: string[]) {}
  async completeWithUsage(): Promise<{ text: string; inputTokens: number; outputTokens: number }> {
    const text = this.replies[this.i++] ?? `{"tool":"done","args":{"summary":"end"}}`;
    return { text, inputTokens: 5, outputTokens: 3 };
  }
  async embed(texts: string[]): Promise<number[][]> {
    this.embedCalls++;
    return texts.map(() => [1, 0]);
  }
}

function seedIndex(root: string): void {
  const p = indexPath(root);
  fs.mkdirSync(path.dirname(p), { recursive: true });
  fs.writeFileSync(
    p,
    JSON.stringify({
      version: INDEX_VERSION,
      chunks: [{ file: "auth.go", language: "Go", start_line: 1, end_line: 3, content: "func Verify()" }],
      vectors: [[1, 0]],
    }),
  );
}

// No index present → the loop still runs (search is note-only); a scripted edit→done
// produces a real edit + a clean done, and indexed=false is reported honestly.
async function testIterative_NoIndex_StillRunsAndEdits(): Promise<void> {
  const root = tmpRoot();
  try {
    const lens = new FakeLens([
      `{"tool":"edit_file","args":{"path":"out.ts","content":"export const x = 1;\\n"}}`,
      `{"tool":"done","args":{"summary":"added x"}}`,
    ]);
    const { result, indexed } = await runIterativeLoop({
      lens,
      root,
      task: "add x",
      model: "claude-sonnet-4-6",
      workspaceId: "ws",
      issueId: "ENG-1",
      maxSteps: 10,
    });
    assert(indexed === false, "no on-disk index → indexed:false (honest)");
    assert(result.done && result.stop === StopReason.Done, "loop finishes cleanly without an index");
    assert(result.editedFiles.includes("out.ts"), "the edit was tracked");
    assert(fs.readFileSync(path.join(root, "out.ts"), "utf8").includes("export const x"), "the edit hit disk");
    assert(lens.embedCalls === 0, "no index → no query embedding is attempted");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

// With an index present → the retriever is loaded (indexed:true) and a search turn
// actually embeds the query through Lens.
async function testIterative_WithIndex_SearchUsesRetriever(): Promise<void> {
  const root = tmpRoot();
  try {
    seedIndex(root);
    const lens = new FakeLens([
      `{"tool":"search_codebase","args":{"query":"how does verify work"}}`,
      `{"tool":"done","args":{"summary":"looked it up"}}`,
    ]);
    const { result, indexed } = await runIterativeLoop({
      lens,
      root,
      task: "understand verify",
      model: "claude-sonnet-4-6",
      workspaceId: "ws",
      issueId: "ENG-1",
      maxSteps: 10,
    });
    assert(indexed === true, "a present index → indexed:true");
    assert(result.done, "loop finishes");
    assert(lens.embedCalls === 1, "the search turn embedded the query through Lens exactly once");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

// Token usage from every turn is aggregated for cost tracking.
async function testIterative_AggregatesUsage(): Promise<void> {
  const root = tmpRoot();
  try {
    let inTok = 0;
    let outTok = 0;
    const lens = new FakeLens([
      `{"tool":"run","args":{"cmd":"echo hi"}}`,
      `{"tool":"done","args":{"summary":"done"}}`,
    ]);
    await runIterativeLoop({
      lens,
      root,
      task: "t",
      model: "m",
      workspaceId: "ws",
      issueId: "ENG-1",
      maxSteps: 10,
      onUsage: (i, o) => {
        inTok += i;
        outTok += o;
      },
    });
    assert(inTok === 10 && outTok === 6, `usage aggregated across 2 turns; got in=${inTok} out=${outTok}`);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

async function main(): Promise<void> {
  await testIterative_NoIndex_StillRunsAndEdits();
  await testIterative_WithIndex_SearchUsesRetriever();
  await testIterative_AggregatesUsage();
  // eslint-disable-next-line no-console
  console.log("ok (3 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
