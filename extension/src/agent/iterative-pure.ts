// Iterative orchestration — the vscode-free glue that turns a Lens client + a workspace
// root into a running iterative agent: load the semantic retriever (honest-absent),
// build the tool set + the Lens-backed model, and run the observe/act loop. Kept out of
// AgentMode so it is headless-testable (test/iterative.test.ts) with a fake Lens + a
// temp workspace; AgentMode.startIterativeTask is a thin bound wrapper (task-state +
// panel emit) over this.

import { Agent, type Result } from "./loop-pure";
import { defaultTools, lensLoopModel, type CompleteCapable } from "./loop-tools";
import { lensEmbedder, loadRetriever, type LensEmbedCapable } from "./retrieval-pure";

// IterativeDeps injects everything the loop needs — the Lens client is the only
// external, expressed structurally (completeWithUsage + embed) so tests pass a fake.
export interface IterativeDeps {
  lens: CompleteCapable & LensEmbedCapable;
  root: string;
  task: string;
  model: string;
  workspaceId: string;
  issueId: string;
  maxSteps?: number;
  signal?: AbortSignal;
  onUsage?: (inputTokens: number, outputTokens: number) => void;
}

export interface IterativeOutcome {
  result: Result;
  indexed: boolean; // whether a semantic index was present (search was retrieval-backed)
}

// runIterativeLoop loads the retriever for `root` (null → the search tool is note-only,
// reported as indexed:false), wires the confined tool set + the Lens model, and runs
// the loop. Every turn's token usage is forwarded to onUsage for cost tracking. Only
// the query text / the completion leaves the machine — the same trust boundary as chat.
export async function runIterativeLoop(deps: IterativeDeps): Promise<IterativeOutcome> {
  const emb = lensEmbedder(deps.lens, deps.workspaceId, deps.issueId);
  const retriever = await loadRetriever(deps.root, emb); // null when no index on disk (honest)
  const tools = defaultTools(deps.root, retriever);
  const model = lensLoopModel(deps.lens, {
    model: deps.model,
    workspaceId: deps.workspaceId,
    issueId: deps.issueId,
    signal: deps.signal,
    onUsage: deps.onUsage,
  });
  const agent = new Agent(model, tools, { maxSteps: deps.maxSteps });
  const result = await agent.run(deps.task);
  return { result, indexed: retriever !== null };
}
