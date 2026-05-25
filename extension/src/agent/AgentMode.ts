// AgentMode — orchestrates multi-file tasks. Three phases:
//   1. plan() asks Lens for a JSON file list (Sonnet).
//   2. execute() generates per-file new content (Sonnet each).
//   3. user reviews diffs, then applyApproved() writes changes.
//
// The class is intentionally vscode-thin: only fs operations and
// path joining touch vscode. The Lens orchestration + state
// machine live in pure helpers (agent-pure.ts) so most of the
// logic is unit-testable.

import * as vscode from "vscode";
import type { LensClient } from "../lens/client";
import type { LensConfig } from "../lens/types";
import { CostTracker, estimateCostUSD } from "../providers/cost-tracker";
import type { IssueContextProvider } from "../track/issue-context";
import {
  AgentPlan,
  AgentStatus,
  PlannedFile,
  canTransition,
  parsePlan,
  renderUnifiedDiff,
  type DiffLine,
} from "./agent-pure";

const AGENT_MODEL = "claude-sonnet-4-6";

export interface FileChange {
  filePath: string;
  operation: "create" | "modify" | "delete";
  originalContent: string;
  newContent: string;
  diff: DiffLine[];
  approved?: boolean;
  rejectionFeedback?: string;
}

export interface AgentTask {
  id: string;
  description: string;
  issueId: string;
  status: AgentStatus;
  plan: string[];
  changes: FileChange[];
  totalCostUSD: number;
  createdAt: Date;
  completedAt?: Date;
  error?: string;
}

export type UpdateListener = (task: AgentTask) => void;

const PLANNER_SYSTEM =
  "You are an expert software engineer agent. Given a task description, " +
  "create a detailed plan and list the files that need to be created or " +
  "modified. Reply with a JSON object ONLY — no prose, no markdown fences. " +
  "Schema: {\"plan\":[\"step 1\",\"step 2\"], " +
  "\"files\":[{\"path\":\"src/foo.ts\",\"operation\":\"modify\",\"description\":\"…\"}]}. " +
  "Valid operations: create, modify, delete. Paths relative to the workspace root.";

const EXECUTOR_SYSTEM =
  "You are an expert software engineer. Make the specified change to this " +
  "file. Return ONLY the complete new file content. No explanations, no " +
  "markdown fences. The file must be syntactically correct.";

export class AgentMode {
  private task: AgentTask | undefined;

  constructor(
    private readonly lens: LensClient,
    private readonly tracker: CostTracker,
    private readonly onUpdate: UpdateListener,
    private readonly issueContext?: IssueContextProvider,
  ) {}

  currentTask(): AgentTask | undefined {
    return this.task;
  }

  // startTask runs phases 1+2 sequentially and leaves the task in
  // `awaiting_approval` when both succeed. Failures along the way
  // set status=failed + populate `error` so the panel can render
  // a meaningful message.
  async startTask(
    description: string,
    config: LensConfig,
    workspaceRoot: string,
  ): Promise<AgentTask> {
    const task: AgentTask = {
      id: "task-" + Date.now().toString(36),
      description,
      issueId: config.activeIssue,
      status: "planning",
      plan: [],
      changes: [],
      totalCostUSD: 0,
      createdAt: new Date(),
    };
    this.task = task;
    this.emit();

    try {
      const plan = await this.plan(description, config, workspaceRoot);
      task.plan = plan.plan;
      this.transition("executing");

      for (const pf of plan.files) {
        const change = await this.executeOne(pf, description, config, workspaceRoot);
        task.changes.push(change);
        this.emit();
      }
      this.transition("awaiting_approval");
    } catch (err) {
      task.error = err instanceof Error ? err.message : String(err);
      task.status = "failed";
      this.emit();
    }
    return task;
  }

  // approveChange + rejectChange manage per-file approval. The
  // user can flip any change either way before invoking apply.
  approveChange(index: number): void {
    if (!this.task) return;
    if (!this.task.changes[index]) return;
    this.task.changes[index].approved = true;
    this.task.changes[index].rejectionFeedback = undefined;
    this.emit();
  }

  rejectChange(index: number, feedback: string): void {
    if (!this.task) return;
    if (!this.task.changes[index]) return;
    this.task.changes[index].approved = false;
    this.task.changes[index].rejectionFeedback = feedback;
    this.emit();
  }

  // regenerateChange asks Lens for another attempt at one file
  // using the user's feedback as additional context. Status stays
  // at awaiting_approval — only the single change updates.
  async regenerateChange(
    index: number,
    feedback: string,
    config: LensConfig,
    workspaceRoot: string,
  ): Promise<void> {
    if (!this.task) return;
    const existing = this.task.changes[index];
    if (!existing) return;
    const pf: PlannedFile = {
      path: existing.filePath,
      operation: existing.operation,
      description:
        existing.operation === "create"
          ? "(see feedback)"
          : "(see feedback)",
    };
    const fresh = await this.executeOne(
      pf,
      this.task.description + "\n\nUser feedback: " + feedback,
      config,
      workspaceRoot,
    );
    fresh.approved = undefined;
    this.task.changes[index] = fresh;
    this.emit();
  }

  // applyApproved writes every approved change to disk. Failures
  // along the way are accumulated rather than aborting — partial
  // success is the honest outcome of a multi-file task.
  async applyApproved(
    workspaceId = "",
  ): Promise<{ applied: number; failed: number }> {
    if (!this.task) return { applied: 0, failed: 0 };
    if (!canTransition(this.task.status, "applying")) {
      return { applied: 0, failed: 0 };
    }
    this.transition("applying");
    let applied = 0;
    let failed = 0;
    const errors: string[] = [];
    for (const c of this.task.changes) {
      if (!c.approved) continue;
      try {
        await this.writeChange(c);
        applied++;
      } catch (err) {
        failed++;
        errors.push(`${c.filePath}: ${err instanceof Error ? err.message : String(err)}`);
      }
    }
    if (failed > 0) {
      this.task.error = errors.join("\n");
      this.task.status = "failed";
    } else {
      this.task.status = "completed";
      this.task.completedAt = new Date();
    }
    this.emit();

    // Push a completion comment to Track — best-effort, never
    // raise. Runs after the state transition so the panel updates
    // immediately and the comment posts in the background.
    if (
      applied > 0 &&
      this.issueContext &&
      workspaceId &&
      this.task.issueId
    ) {
      void this.issueContext.pushAgentCompletionComment(
        workspaceId,
        this.task.issueId,
        this.task.description,
        applied,
        this.task.totalCostUSD,
        AGENT_MODEL,
      );
    }

    return { applied, failed };
  }

  cancel(): void {
    if (!this.task) return;
    if (canTransition(this.task.status, "cancelled")) {
      this.task.status = "cancelled";
      this.emit();
    }
  }

  // ─── internals ────────────────────────────────────

  private async plan(
    description: string,
    config: LensConfig,
    workspaceRoot: string,
  ): Promise<AgentPlan> {
    const issueCtx = this.issueContext?.getIssueContext() ?? "";
    const issueBlock = issueCtx
      ? `\nActive issue context:\n${issueCtx}\n`
      : `\nActive issue: ${config.activeIssue || "(none)"}\n`;
    const user = `${PLANNER_SYSTEM}\n\nTask: ${description}\nWorkspace: ${workspaceRoot}${issueBlock}`;
    const res = await this.lens.completeWithUsage(
      [{ role: "user", content: user }],
      AGENT_MODEL,
      "agent-plan",
      config.workspaceId,
      config.activeIssue,
      2048,
    );
    this.recordCost(res.inputTokens, res.outputTokens, config.activeIssue, "agent-plan");
    return parsePlan(res.text);
  }

  private async executeOne(
    pf: PlannedFile,
    description: string,
    config: LensConfig,
    workspaceRoot: string,
  ): Promise<FileChange> {
    const abs = absolutise(pf.path, workspaceRoot);
    let originalContent = "";
    if (pf.operation === "modify" || pf.operation === "delete") {
      try {
        const bytes = await vscode.workspace.fs.readFile(vscode.Uri.file(abs));
        originalContent = new TextDecoder().decode(bytes);
      } catch {
        if (pf.operation === "modify") {
          throw new Error(`cannot read existing file ${pf.path}`);
        }
      }
    }
    let newContent = "";
    if (pf.operation !== "delete") {
      const user =
        `${EXECUTOR_SYSTEM}\n\nTask: ${description}\nFile: ${pf.path}\n` +
        `Operation: ${pf.operation}\n` +
        (pf.operation === "modify"
          ? `\nCurrent content:\n\`\`\`\n${originalContent}\n\`\`\`\n`
          : "") +
        `\nChange to make: ${pf.description}`;
      const res = await this.lens.completeWithUsage(
        [{ role: "user", content: user }],
        AGENT_MODEL,
        "agent-execute",
        config.workspaceId,
        config.activeIssue,
        4096,
      );
      this.recordCost(res.inputTokens, res.outputTokens, config.activeIssue, "agent-execute");
      newContent = stripFences(res.text);
    }
    return {
      filePath: pf.path,
      operation: pf.operation,
      originalContent,
      newContent,
      diff: renderUnifiedDiff(originalContent, newContent),
    };
  }

  private async writeChange(c: FileChange): Promise<void> {
    const root =
      vscode.workspace.workspaceFolders?.[0]?.uri.fsPath ?? "";
    const abs = absolutise(c.filePath, root);
    if (c.operation === "delete") {
      await vscode.workspace.fs.delete(vscode.Uri.file(abs));
      return;
    }
    const enc = new TextEncoder();
    await vscode.workspace.fs.writeFile(
      vscode.Uri.file(abs),
      enc.encode(c.newContent),
    );
  }

  private recordCost(
    inputTokens: number,
    outputTokens: number,
    issue: string,
    feature: string,
  ): void {
    const cost = estimateCostUSD(inputTokens, outputTokens);
    this.tracker.recordCompletion(inputTokens + outputTokens, cost, issue, feature);
    if (this.task) {
      this.task.totalCostUSD += cost;
    }
  }

  // transition is the small guard that enforces our allowed
  // state-machine edges. Logs (but doesn't throw) on an illegal
  // transition so a bug in the panel can't wedge the agent in an
  // impossible state.
  private transition(to: AgentStatus): void {
    if (!this.task) return;
    if (!canTransition(this.task.status, to)) {
      // eslint-disable-next-line no-console
      console.warn(`agent: ignoring transition ${this.task.status} → ${to}`);
      return;
    }
    this.task.status = to;
    this.emit();
  }

  private emit(): void {
    if (this.task) this.onUpdate(this.task);
  }
}

function absolutise(p: string, root: string): string {
  // POSIX-only absolute check is fine here — VS Code on Windows
  // still resolves backslash paths consistently through
  // vscode.Uri.file.
  if (p.startsWith("/") || /^[a-zA-Z]:[\\/]/.test(p)) return p;
  return root ? `${root}/${p}` : p;
}

function stripFences(text: string): string {
  let out = text.trim();
  if (out.startsWith("```")) {
    const i = out.indexOf("\n");
    if (i >= 0) out = out.substring(i + 1);
  }
  if (out.endsWith("```")) {
    out = out.substring(0, out.lastIndexOf("```")).replace(/\n+$/, "");
  }
  if (!out.endsWith("\n")) out += "\n";
  return out;
}
