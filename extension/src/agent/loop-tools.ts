// Real backends for the pure iterative loop (loop-pure.ts): the node-backed tools the
// agent drives (read_file / edit_file / run / search_codebase) plus the Lens Model
// adapter. Deliberately vscode-free — file ops use node:fs confined by confine-pure
// (S11), `run` uses child_process, search uses the Retriever seam — so the whole set is
// headless-testable (test/loop-tools.test.ts) and mirrors the Go agentloop DefaultTools.
//
// FORK (documented in BUILD_STATE): these tools operate on DISK (node:fs), exactly like
// the CLI loop — not through vscode.workspace.fs. On-disk is what a subsequent build/run
// sees; the trade-off is they don't reflect unsaved editor buffers (save first).

import * as fs from "fs";
import * as path from "path";
import { spawn } from "child_process";
import { absolutise } from "./confine-pure";
import { renderUnifiedDiff, type DiffLine } from "./agent-pure";
import { Registry, type Message, type Model, type Tool } from "./loop-pure";
import type { RetrievedChunk } from "./retrieval-pure";

const MAX_OBS_BYTES = 8 * 1024;
const DEFAULT_RUN_TIMEOUT_MS = 120_000;

function truncate(s: string): string {
  return s.length <= MAX_OBS_BYTES ? s : s.slice(0, MAX_OBS_BYTES) + "\n… (truncated)";
}

function sliceLines(content: string, start: number, end: number): string {
  const lines = content.split("\n");
  if (start < 1) start = 1;
  if (end < 1 || end > lines.length) end = lines.length;
  if (start > lines.length) return "";
  return lines.slice(start - 1, end).join("\n");
}

function diffLinesToText(lines: DiffLine[]): string {
  return lines
    .map((l) => {
      switch (l.kind) {
        case "add":
          return "+" + l.text;
        case "remove":
          return "-" + l.text;
        case "header":
          return l.text;
        default:
          return " " + l.text;
      }
    })
    .join("\n");
}

// ── read_file ──────────────────────────────────────────

export function newReadTool(root: string): Tool {
  return {
    name: () => "read_file",
    description: () =>
      `read_file {"path":"rel/path.ts","start":1,"end":40} — read a file (optional 1-based line span). Confined to the workspace root.`,
    run: async (argsRaw: string): Promise<string> => {
      const a = JSON.parse(argsRaw) as { path?: string; start?: number; end?: number };
      const abs = absolutise(a.path ?? "", root); // throws on escape (S11)
      let content = fs.readFileSync(abs, "utf8");
      if ((a.start ?? 0) > 0 || (a.end ?? 0) > 0) {
        content = sliceLines(content, a.start ?? 0, a.end ?? 0);
      }
      return truncate(`${a.path}:\n${content}`);
    },
  };
}

// ── edit_file ──────────────────────────────────────────

export function newEditTool(root: string): Tool {
  return {
    name: () => "edit_file",
    description: () =>
      `edit_file {"path":"rel/path.ts","content":"<full new file content>"} — write a file's COMPLETE new content. Confined to the workspace root; returns a unified diff.`,
    run: async (argsRaw: string): Promise<string> => {
      const a = JSON.parse(argsRaw) as { path?: string; content?: string };
      const abs = absolutise(a.path ?? "", root); // throws on escape (S11)
      let original = "";
      try {
        original = fs.readFileSync(abs, "utf8");
      } catch {
        original = ""; // new file
      }
      const content = a.content ?? "";
      fs.mkdirSync(path.dirname(abs), { recursive: true });
      fs.writeFileSync(abs, content);
      const d = diffLinesToText(renderUnifiedDiff(original, content));
      return truncate(`edited ${a.path}\n${d.trim() === "" ? "(no change)" : d}`);
    },
  };
}

// ── run ────────────────────────────────────────────────

interface RunResult {
  exitCode: number;
  output: string;
}

// runCommand executes a build/test/shell command through `sh -c` (POSIX) / powershell
// (Windows) in the workspace root. The command IS the agent tool's explicit payload —
// running the model's chosen build/test command is the whole point of the `run` tool —
// so a non-literal spawn is by design, NOT an injection sink: `cmd` is the ONLY thing
// placed in the shell (no path/filename is interpolated into the command string), the
// cwd is the confined workspace root, and there is a hard timeout. This mirrors the CLI
// loop's `internal/runner` (which chose `sh -c` over arg-vector exec so pipes/&& work)
// and the extension's existing heal.ts runBuild. The nosemgrep below documents that
// intent for the dangerous-spawn-shell rule.
function runCommand(cmd: string, cwd: string, timeoutMs: number): Promise<RunResult> {
  return new Promise((resolve) => {
    const child =
      process.platform === "win32"
        // nosemgrep: javascript.lang.security.audit.dangerous-spawn-shell.dangerous-spawn-shell -- agent run tool: cmd is the intended payload, cwd-confined + timeout
        ? spawn("powershell", ["-NoProfile", "-Command", cmd], { cwd })
        // nosemgrep: javascript.lang.security.audit.dangerous-spawn-shell.dangerous-spawn-shell -- agent run tool: cmd is the intended payload, cwd-confined + timeout
        : spawn("sh", ["-c", cmd], { cwd });
    let out = "";
    const cap = (b: Buffer): void => {
      out += b.toString();
      if (out.length > MAX_OBS_BYTES * 2) out = out.slice(0, MAX_OBS_BYTES * 2);
    };
    child.stdout?.on("data", cap);
    child.stderr?.on("data", cap);
    const timer = setTimeout(() => {
      out += `\n… (timed out after ${timeoutMs}ms)`;
      child.kill("SIGKILL");
    }, timeoutMs);
    child.on("close", (code) => {
      clearTimeout(timer);
      resolve({ exitCode: code ?? -1, output: out });
    });
    child.on("error", (err) => {
      clearTimeout(timer);
      resolve({ exitCode: -1, output: out + String(err) });
    });
  });
}

export function newRunTool(root: string, timeoutMs = DEFAULT_RUN_TIMEOUT_MS): Tool {
  return {
    name: () => "run",
    description: () =>
      `run {"cmd":"npm test"} — run a build/test/shell command in the workspace root; returns exit code + captured output.`,
    run: async (argsRaw: string): Promise<string> => {
      const a = JSON.parse(argsRaw) as { cmd?: string };
      const cmd = (a.cmd ?? "").trim();
      if (cmd === "") throw new Error("run: empty command");
      // A non-zero exit is a normal OBSERVATION (the model re-plans on it), never a throw.
      const res = await runCommand(cmd, root, timeoutMs);
      return truncate(`$ ${cmd}\nexit ${res.exitCode}\n${res.output}`);
    },
  };
}

// ── search_codebase ────────────────────────────────────

// RetrieverLike is the structural slice the search tool needs — the Retriever class
// satisfies it, and tests can pass a fake.
export interface RetrieverLike {
  retrieve(query: string, k: number): Promise<RetrievedChunk[]>;
}

export function newSearchTool(ret: RetrieverLike | null, k = 6): Tool {
  if (k <= 0) k = 6;
  return {
    name: () => "search_codebase",
    description: () =>
      `search_codebase {"query":"how auth works"} — semantic retrieval over the codebase index; returns the most relevant chunks (file:span + content).`,
    run: async (argsRaw: string): Promise<string> => {
      const a = JSON.parse(argsRaw) as { query?: string; k?: number };
      if (ret === null) {
        return "(no semantic index — run `talyvor-code index`; use read_file/run instead)";
      }
      const chunks = await ret.retrieve(a.query ?? "", (a.k ?? 0) > 0 ? (a.k as number) : k);
      if (chunks.length === 0) return "(no relevant chunks found)";
      const body = chunks
        .map((c) => `// ${c.file}:${c.startLine}-${c.endLine} (score ${c.score.toFixed(3)})\n${c.content}\n`)
        .join("\n");
      return truncate(body);
    },
  };
}

// defaultTools builds the standard agent tool set for a workspace root + (optional)
// retriever. A null retriever leaves search available but note-only (no index built).
export function defaultTools(root: string, ret: RetrieverLike | null): Registry {
  const reg = new Registry();
  reg.register(newSearchTool(ret, 6));
  reg.register(newReadTool(root));
  reg.register(newEditTool(root));
  reg.register(newRunTool(root));
  return reg;
}

// ── Lens Model adapter ─────────────────────────────────

// CompleteCapable is the structural slice of LensClient the loop's Model needs.
export interface CompleteCapable {
  completeWithUsage(
    messages: Message[],
    model: string,
    feature: string,
    workspaceId: string,
    issueId: string,
    maxTokens?: number,
    signal?: AbortSignal,
  ): Promise<{ text: string; inputTokens: number; outputTokens: number }>;
}

export interface LensModelOptions {
  model: string;
  workspaceId: string;
  issueId: string;
  maxTokens?: number;
  signal?: AbortSignal;
  onUsage?: (inputTokens: number, outputTokens: number) => void;
}

// lensLoopModel adapts a Lens client to the loop's Model seam. Every turn is a Lens
// completion carrying the SAME issue/workspace attribution headers as any other agent
// call (feature "agent-loop" → tagged code-agent-loop) — the cost moat is preserved,
// nothing new leaves the machine beyond the completion that already happens.
export function lensLoopModel(lens: CompleteCapable, opts: LensModelOptions): Model {
  return {
    complete: async (messages: Message[]): Promise<string> => {
      const res = await lens.completeWithUsage(
        messages.map((m) => ({ role: m.role, content: m.content })),
        opts.model,
        "agent-loop",
        opts.workspaceId,
        opts.issueId,
        opts.maxTokens ?? 4096,
        opts.signal,
      );
      opts.onUsage?.(res.inputTokens, res.outputTokens);
      return res.text;
    },
  };
}
