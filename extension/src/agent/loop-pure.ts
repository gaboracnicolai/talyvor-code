// Pure iterative agent loop — the TS mirror of the Go internal/agentloop (merged in
// #20). An OBSERVE/ACT loop the model drives via tool calls (search → read → edit →
// run → observe → re-plan), replacing the single-pass plan→generate→heal pipeline.
//
// This module is deliberately vscode-free and filesystem-free: the LLM is the `Model`
// seam, every action is a `Tool` in a `Registry`, so the whole orchestration is unit-
// testable headlessly with a scripted model + stub tools (test/agent-loop.test.ts).
// The real tools (read/edit via confine-pure + vscode.fs, run via child_process,
// search via the semantic Retriever) are thin adapters wired in AgentMode.

// Message is one conversation turn. Local to this module so the loop's mechanics don't
// couple to any provider type; the production adapter converts to/from LensClient.
export interface Message {
  role: "system" | "user" | "assistant";
  content: string;
}

// Model is the loop's LLM seam: given the running transcript, return the next reply.
// Provider-agnostic (text in, text out) so the loop is testable with a scripted stub
// and works through the existing Lens client.
export interface Model {
  complete(messages: Message[]): Promise<string>;
}

// Tool is one action the agent can take on a turn. run receives the model's raw JSON
// args string and returns an OBSERVATION (fed back to the model next turn). A thrown
// error means the tool could not act (e.g. a path escaped the root); the loop surfaces
// it as an observation and the model re-plans — a tool error never kills the loop.
export interface Tool {
  name(): string;
  description(): string;
  run(argsRaw: string): Promise<string>;
}

// Registry maps tool names to tools and dispatches by name.
export class Registry {
  private readonly tools = new Map<string, Tool>();
  register(t: Tool): void {
    this.tools.set(t.name(), t);
  }
  async dispatch(name: string, argsRaw: string): Promise<string> {
    const t = this.tools.get(name);
    if (!t) {
      throw new Error(`unknown tool "${name}" (available: ${this.names().join(", ")})`);
    }
    return t.run(argsRaw);
  }
  names(): string[] {
    return [...this.tools.keys()].sort();
  }
  get(name: string): Tool | undefined {
    return this.tools.get(name);
  }
}

// StopReason is why the loop ended (string values mirror the Go String()).
export enum StopReason {
  Done = "done",
  Budget = "budget-exhausted",
  NoProgress = "no-progress",
  Error = "model-error",
}

// Config tunes the loop. Omitted / non-positive fields fall back to safe defaults.
export interface Config {
  maxSteps?: number; // hard cap on tool-call turns (default 20)
  maxRepeat?: number; // abort after an identical tool call recurs more than this (default 2)
  maxTranscript?: number; // messages kept before trimming the oldest (default 40)
}

// Result is the loop outcome. `error` is populated only when stop === Error — a TS
// fork from Go's (Result, error) pair: run() always returns a Result and never throws,
// so a caller (the panel) always has a structured outcome to render.
export interface Result {
  done: boolean;
  summary: string;
  steps: number;
  stop: StopReason;
  editedFiles: string[];
  transcript: Message[];
  error?: string;
}

// ToolCall is one parsed model turn. argsRaw is the canonical JSON of the args object,
// used both for dispatch and for the no-progress signature.
export interface ToolCall {
  thought: string;
  tool: string;
  argsRaw: string;
}

// parseToolCall extracts a ToolCall from a model reply. Defensive against the usual
// ways models wrap JSON: ```json fences and leading/trailing prose.
export function parseToolCall(reply: string): ToolCall {
  let s = reply.trim();
  if (s.startsWith("```")) {
    const nl = s.indexOf("\n");
    if (nl >= 0) s = s.substring(nl + 1);
    const close = s.lastIndexOf("```");
    if (close >= 0) s = s.substring(0, close).replace(/\n+$/, "");
    s = s.trim();
  }
  if (!s.startsWith("{")) {
    const a = s.indexOf("{");
    const b = s.lastIndexOf("}");
    if (a >= 0 && b > a) s = s.substring(a, b + 1);
  }
  let data: unknown;
  try {
    data = JSON.parse(s);
  } catch (err) {
    throw new Error("reply is not a JSON tool call: " + (err instanceof Error ? err.message : String(err)));
  }
  if (!data || typeof data !== "object") {
    throw new Error("reply is not a JSON tool call: not an object");
  }
  const obj = data as Record<string, unknown>;
  const tool = typeof obj.tool === "string" ? obj.tool.trim() : "";
  if (!tool) {
    throw new Error('reply has no "tool" field');
  }
  const thought = typeof obj.thought === "string" ? obj.thought : "";
  const argsRaw = JSON.stringify(obj.args ?? {});
  return { thought, tool, argsRaw };
}

// doneSummary / editPath pull a field out of a call's raw args (best-effort).
export function doneSummary(argsRaw: string): string {
  try {
    const a = JSON.parse(argsRaw) as { summary?: unknown };
    return typeof a.summary === "string" ? a.summary : "";
  } catch {
    return "";
  }
}

export function editPath(argsRaw: string): string {
  try {
    const a = JSON.parse(argsRaw) as { path?: unknown };
    return typeof a.path === "string" ? a.path : "";
  } catch {
    return "";
  }
}

// systemPrompt describes the tools + the one-JSON-object-per-turn protocol.
export function systemPrompt(reg: Registry): string {
  const lines: string[] = [];
  lines.push(
    "You are an ITERATIVE coding agent. Accomplish the task by calling ONE tool per " +
      "turn, observing its result, and deciding the next call — searching, reading, " +
      "editing, and running until the task is complete AND verified.",
  );
  lines.push("");
  lines.push("Tools:");
  for (const name of reg.names()) {
    const t = reg.get(name);
    if (t) lines.push(`- ${t.description()}`);
  }
  lines.push(`- done {"summary":"what you did"} — finish ONLY when the task is complete and the build/tests pass.`);
  lines.push("");
  lines.push("Respond with EXACTLY ONE JSON object and nothing else:");
  lines.push(`{"thought":"brief reasoning","tool":"<tool>","args":{...}}`);
  lines.push("");
  lines.push(
    "Ground every edit in what you READ/SEARCHED/RAN. After changing code, RUN the " +
      "build/tests, read the failure, and fix it — never repeat an identical edit. " +
      "Finish with the done tool.",
  );
  return lines.join("\n") + "\n";
}

// Agent drives the iterative loop over a Model + a tool Registry.
export class Agent {
  private readonly maxSteps: number;
  private readonly maxRepeat: number;
  private readonly maxTranscript: number;

  constructor(
    private readonly model: Model,
    private readonly tools: Registry,
    cfg: Config = {},
  ) {
    this.maxSteps = cfg.maxSteps && cfg.maxSteps > 0 ? cfg.maxSteps : 20;
    this.maxRepeat = cfg.maxRepeat && cfg.maxRepeat > 0 ? cfg.maxRepeat : 2;
    this.maxTranscript = cfg.maxTranscript && cfg.maxTranscript > 0 ? cfg.maxTranscript : 40;
  }

  // run executes the OBSERVE/ACT loop: the model picks a tool, the tool runs, its
  // result is fed back as an observation, and the model re-plans — until done, the
  // step budget is hit, or the no-progress detector trips. Self-heal is native: a
  // failing `run` is just another observation the model re-plans on.
  async run(task: string): Promise<Result> {
    let messages: Message[] = [
      { role: "system", content: systemPrompt(this.tools) },
      { role: "user", content: "Task: " + task + "\n\nBegin. Respond with ONE JSON tool call." },
    ];
    const editedFiles: string[] = [];
    const sigCount = new Map<string, number>();
    const bump = (k: string): number => {
      const n = (sigCount.get(k) ?? 0) + 1;
      sigCount.set(k, n);
      return n;
    };

    for (let step = 1; step <= this.maxSteps; step++) {
      let reply: string;
      try {
        reply = await this.model.complete(messages);
      } catch (err) {
        return {
          done: false,
          summary: "",
          steps: step,
          stop: StopReason.Error,
          editedFiles,
          transcript: messages,
          error: err instanceof Error ? err.message : String(err),
        };
      }

      let call: ToolCall;
      try {
        call = parseToolCall(reply);
      } catch (perr) {
        // Bad format: feed it back so the model can correct — but bound the number of
        // consecutive malformed replies so it can't loop on garbage.
        if (bump("\x00parse") > this.maxRepeat) {
          return this.terminal(StopReason.NoProgress, step, editedFiles, messages);
        }
        const hint =
          "[error] " +
          (perr instanceof Error ? perr.message : String(perr)) +
          ' — reply with EXACTLY ONE JSON object: {"thought":"...","tool":"<tool>","args":{...}}';
        messages = this.appendTurn(messages, reply, hint);
        continue;
      }

      if (call.tool === "done") {
        return {
          done: true,
          summary: doneSummary(call.argsRaw),
          steps: step,
          stop: StopReason.Done,
          editedFiles,
          transcript: messages,
        };
      }

      // No-progress detector: the identical (tool, args) recurring more than maxRepeat
      // times means the agent is spinning (the classic edit→fail→identical-edit cycle).
      // Abort BEFORE the step budget so an unattended run can't burn its budget looping.
      const sig = call.tool + "\x00" + call.argsRaw;
      if (bump(sig) > this.maxRepeat) {
        return this.terminal(StopReason.NoProgress, step, editedFiles, messages);
      }

      let obs: string;
      try {
        obs = await this.tools.dispatch(call.tool, call.argsRaw);
        if (call.tool === "edit_file") {
          const p = editPath(call.argsRaw);
          if (p && !editedFiles.includes(p)) editedFiles.push(p);
        }
      } catch (terr) {
        obs = "[error] " + (terr instanceof Error ? terr.message : String(terr));
      }
      messages = this.appendTurn(messages, reply, `[${call.tool}]\n${obs}`);
    }

    return this.terminal(StopReason.Budget, this.maxSteps, editedFiles, messages);
  }

  private terminal(stop: StopReason, steps: number, editedFiles: string[], transcript: Message[]): Result {
    return { done: false, summary: "", steps, stop, editedFiles, transcript };
  }

  // appendTurn records the assistant reply + the observation, trimming the transcript
  // to the context cap (keeping the system message).
  private appendTurn(messages: Message[], assistantReply: string, observation: string): Message[] {
    const next = [
      ...messages,
      { role: "assistant" as const, content: assistantReply },
      { role: "user" as const, content: observation },
    ];
    return trimTranscript(next, this.maxTranscript);
  }
}

// trimTranscript keeps the system message + the most recent (max-1) turns.
export function trimTranscript(msgs: Message[], max: number): Message[] {
  if (msgs.length <= max) return msgs;
  return [msgs[0], ...msgs.slice(msgs.length - (max - 1))];
}
