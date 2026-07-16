// Real loop backends — the node-backed tools (read/edit/run/search) + the Lens Model
// adapter that turn the pure loop into a working agent. All vscode-free (node:fs +
// child_process + the Retriever seam + a structural Lens client), so the whole thing
// runs headlessly under `npm test`: real files in a temp dir, a real subprocess for
// `run`, a fake retriever for search, a fake Lens for the model. S11 confinement
// (confine-pure) is exercised on the file tools.

import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { Agent, StopReason, type Message, type Model } from "../src/agent/loop-pure";
import {
  defaultTools,
  lensLoopModel,
  newEditTool,
  newReadTool,
  newRunTool,
  newSearchTool,
} from "../src/agent/loop-tools";
import type { RetrievedChunk } from "../src/agent/retrieval-pure";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

function tmpRoot(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "tlv-tools-"));
}

// ─── read_file ─────────────────────────────────────────

async function testReadTool_ReadsConfined(): Promise<void> {
  const root = tmpRoot();
  try {
    fs.writeFileSync(path.join(root, "x.go"), "package x\n// MARKER\nfunc F(){}\n");
    const obs = await newReadTool(root).run(JSON.stringify({ path: "x.go" }));
    assert(obs.includes("MARKER"), "read_file returns the file content");
    const sliced = await newReadTool(root).run(JSON.stringify({ path: "x.go", start: 2, end: 2 }));
    assert(sliced.includes("MARKER") && !sliced.includes("package x"), "line span honored");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

async function testReadTool_RefusesEscape(): Promise<void> {
  const root = tmpRoot();
  try {
    let threw = false;
    try {
      await newReadTool(root).run(JSON.stringify({ path: "../../etc/passwd" }));
    } catch (e) {
      threw = e instanceof Error && /outside workspace root/.test(e.message);
    }
    assert(threw, "S11: read_file must refuse a path outside the workspace root");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

// ─── edit_file ─────────────────────────────────────────

async function testEditTool_WritesAndDiffs(): Promise<void> {
  const root = tmpRoot();
  try {
    const obs = await newEditTool(root).run(JSON.stringify({ path: "new.go", content: "package n\n" }));
    assert(fs.readFileSync(path.join(root, "new.go"), "utf8") === "package n\n", "edit_file writes the content to disk");
    assert(obs.includes("new.go"), "observation names the edited file");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

async function testEditTool_RefusesEscape(): Promise<void> {
  const root = tmpRoot();
  try {
    let threw = false;
    try {
      await newEditTool(root).run(JSON.stringify({ path: "../evil.go", content: "x" }));
    } catch (e) {
      threw = e instanceof Error && /outside workspace root/.test(e.message);
    }
    assert(threw, "S11: edit_file must refuse a path outside the workspace root");
    assert(!fs.existsSync(path.join(path.dirname(root), "evil.go")), "no file written outside root");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

// ─── run ───────────────────────────────────────────────

async function testRunTool_CapturesExitAndOutput(): Promise<void> {
  const root = tmpRoot();
  try {
    const ok = await newRunTool(root).run(JSON.stringify({ cmd: "echo hello-run" }));
    assert(ok.includes("exit 0") && ok.includes("hello-run"), "run captures exit 0 + stdout");
    // A non-zero exit is an OBSERVATION, not a thrown error (the model re-plans on it).
    const bad = await newRunTool(root).run(JSON.stringify({ cmd: "exit 3" }));
    assert(bad.includes("exit 3"), "run reports a non-zero exit as an observation, not an error");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

// ─── search_codebase ───────────────────────────────────

async function testSearchTool_RealChunksAndHonestAbsent(): Promise<void> {
  const chunks: RetrievedChunk[] = [
    { file: "auth.go", language: "Go", startLine: 1, endLine: 5, content: "func Verify()", score: 0.83 },
  ];
  const fakeRet = { retrieve: async (_q: string, _k: number) => chunks };
  const obs = await newSearchTool(fakeRet, 6).run(JSON.stringify({ query: "auth" }));
  assert(obs.includes("auth.go") && obs.includes("0.83"), "search returns file:span + real score");

  const none = await newSearchTool(null, 6).run(JSON.stringify({ query: "auth" }));
  assert(/no semantic index/i.test(none), "null retriever → honest note, not a fabricated hit");
}

async function testDefaultTools_RegistersFour(): Promise<void> {
  const reg = defaultTools(tmpRoot(), null);
  const names = reg.names();
  for (const n of ["read_file", "edit_file", "run", "search_codebase"]) {
    assert(names.includes(n), `defaultTools must register ${n}`);
  }
}

// ─── Lens Model adapter ────────────────────────────────

async function testLensModel_MapsMessagesAndReportsUsage(): Promise<void> {
  let seenFeature = "";
  let seenModel = "";
  let seenCount = 0;
  const fakeLens = {
    completeWithUsage: async (
      messages: Message[],
      model: string,
      feature: string,
    ) => {
      seenCount = messages.length;
      seenModel = model;
      seenFeature = feature;
      return { text: "the reply", inputTokens: 11, outputTokens: 7 };
    },
  };
  let reportedIn = 0;
  let reportedOut = 0;
  const model: Model = lensLoopModel(fakeLens, {
    model: "claude-sonnet-4-6",
    workspaceId: "ws",
    issueId: "ENG-1",
    onUsage: (i, o) => {
      reportedIn = i;
      reportedOut = o;
    },
  });
  const out = await model.complete([
    { role: "system", content: "sys" },
    { role: "user", content: "hi" },
  ]);
  assert(out === "the reply", "adapter returns the model text");
  assert(seenCount === 2, "adapter forwards the full transcript (incl. system message)");
  assert(seenModel === "claude-sonnet-4-6", "adapter uses the configured model");
  assert(seenFeature === "agent-loop", "adapter tags feature agent-loop for cost attribution");
  assert(reportedIn === 11 && reportedOut === 7, "adapter reports token usage for cost tracking");
}

// ─── integration: pure loop + REAL tools + scripted model ──

async function testIntegration_LoopDrivesRealTools(): Promise<void> {
  const root = tmpRoot();
  try {
    fs.writeFileSync(path.join(root, "seed.go"), "package s\n// SEED\n");
    // Scripted model: read the seed, write a file, run a command, finish.
    const replies = [
      `{"tool":"read_file","args":{"path":"seed.go"}}`,
      `{"tool":"edit_file","args":{"path":"out.go","content":"package o\\nfunc Out(){}\\n"}}`,
      `{"tool":"run","args":{"cmd":"echo built"}}`,
      `{"tool":"done","args":{"summary":"wrote out.go"}}`,
    ];
    let i = 0;
    const model: Model = { complete: async () => replies[i++] ?? `{"tool":"done","args":{"summary":"x"}}` };
    const res = await new Agent(model, defaultTools(root, null), { maxSteps: 10 }).run("build out.go");

    assert(res.done && res.stop === StopReason.Done, "loop finishes cleanly against real tools");
    assert(res.editedFiles.includes("out.go"), "edited file tracked");
    assert(fs.readFileSync(path.join(root, "out.go"), "utf8").includes("func Out()"), "the edit actually hit disk");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

async function main(): Promise<void> {
  await testReadTool_ReadsConfined();
  await testReadTool_RefusesEscape();
  await testEditTool_WritesAndDiffs();
  await testEditTool_RefusesEscape();
  await testRunTool_CapturesExitAndOutput();
  await testSearchTool_RealChunksAndHonestAbsent();
  await testDefaultTools_RegistersFour();
  await testLensModel_MapsMessagesAndReportsUsage();
  await testIntegration_LoopDrivesRealTools();
  // eslint-disable-next-line no-console
  console.log("ok (9 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
