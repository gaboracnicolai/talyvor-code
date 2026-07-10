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
import { absolutise } from "./confine-pure";
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
import {
  buildHealPrompt,
  MAX_HEAL_ATTEMPTS,
  parseHealResult,
  type FileFix,
  type HealLanguage,
} from "./heal-pure";
import {
  detectBuildCommand,
  runBuild,
  stitchOutput,
} from "./heal";
import {
  createPR,
  preflight as prPreflight,
  resolveToken,
  promptForToken,
} from "../github/pr-creator";
import { generatePRBody, slugifyBranch } from "../github/pr-pure";

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

export interface HealAttemptInfo {
  attempt: number;
  command: string;
  exitCode: number;
  stdoutTail: string;
  stderrTail: string;
  fixes: FileFix[];
  appliedCount: number;
  success: boolean;
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
  healAttempts?: HealAttemptInfo[];
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

// HEAL_MODEL pins Sonnet for the repair pass per spec — debugging
// is reasoning-heavy and the model-selector consensus puts review
// on Sonnet too.
const HEAL_MODEL = "claude-sonnet-4-6";

export class AgentMode {
  private task: AgentTask | undefined;
  private outputChannel: vscode.OutputChannel | undefined;

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

  // applyAndHeal is the "Run & Heal" entry point. Walks the same
  // file-write loop as applyApproved, but then transitions into
  // the healing state and drives the Lens-powered repair loop
  // until the build passes or we hit MAX_HEAL_ATTEMPTS.
  async applyAndHeal(
    workspaceId: string,
    workspaceRoot: string,
    config: LensConfig,
  ): Promise<{ applied: number; failed: number; healed: boolean }> {
    if (!this.task) return { applied: 0, failed: 0, healed: false };
    if (!canTransition(this.task.status, "applying")) {
      return { applied: 0, failed: 0, healed: false };
    }
    this.transition("applying");

    // Write phase — mirrors applyApproved.
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
      this.emit();
      return { applied, failed, healed: false };
    }

    // Heal phase.
    this.task.healAttempts = [];
    this.transition("healing");
    const healed = await this.runHealCycle(workspaceRoot, config);

    if (healed) {
      this.task.status = "completed";
      this.task.completedAt = new Date();
      this.emit();
      if (this.issueContext && workspaceId && this.task.issueId) {
        void this.issueContext.pushAgentCompletionComment(
          workspaceId,
          this.task.issueId,
          this.task.description,
          applied,
          this.task.totalCostUSD,
          AGENT_MODEL,
        );
      }
    } else {
      this.task.status = "failed";
      this.task.error = "Self-heal exhausted — build still failing.";
      this.emit();
    }
    return { applied, failed, healed };
  }

  // runHealCycle runs the heal loop (detect → run → fix → retry)
  // up to MAX_HEAL_ATTEMPTS times. Returns true when the build
  // passes; false when we give up.
  private async runHealCycle(
    workspaceRoot: string,
    config: LensConfig,
  ): Promise<boolean> {
    if (!this.task) return false;
    const channel = this.ensureOutputChannel();
    channel.show(true);

    const plan = await detectBuildCommand(workspaceRoot);
    if (!plan) {
      channel.appendLine("⚠️  No build system detected — skipping heal.");
      // Treat as success — the changes are on disk; we just can't
      // verify them automatically.
      return true;
    }
    channel.appendLine(`🔧 Build command: ${plan.command}`);

    for (let attempt = 1; attempt <= MAX_HEAL_ATTEMPTS; attempt++) {
      const result = await runBuild(plan.command, workspaceRoot, channel);
      const ok = result.exitCode === 0;
      const fixes: FileFix[] = [];
      let appliedCount = 0;

      if (!ok) {
        channel.appendLine(`\n❌ Build failed (exit ${result.exitCode}). Asking the model for a fix (${attempt}/${MAX_HEAL_ATTEMPTS})…`);
        const errorBody = stitchOutput(result);
        const prompt = buildHealPrompt({
          taskDescription: this.task.description,
          failedCommand: plan.command,
          errorOutput: errorBody,
          changedFiles: this.task.changes.filter((c) => c.approved).map((c) => c.filePath),
          language: plan.language as HealLanguage,
          attempt,
        });
        try {
          const res = await this.lens.completeWithUsage(
            [{ role: "user", content: prompt }],
            HEAL_MODEL,
            "agent-heal",
            config.workspaceId,
            config.activeIssue,
            4096,
          );
          this.recordCost(res.inputTokens, res.outputTokens, config.activeIssue, "agent-heal");
          const parsed = parseHealResult(res.text);
          fixes.push(...parsed);
        } catch (err) {
          channel.appendLine(`! heal: ${err instanceof Error ? err.message : String(err)}`);
        }
        appliedCount = await this.applyHealFixes(fixes, workspaceRoot, channel);
      }

      const info: HealAttemptInfo = {
        attempt,
        command: plan.command,
        exitCode: result.exitCode,
        stdoutTail: tail(result.stdout, 2000),
        stderrTail: tail(result.stderr, 2000),
        fixes,
        appliedCount,
        success: ok,
      };
      this.task.healAttempts = [...(this.task.healAttempts ?? []), info];
      this.emit();

      if (ok) {
        channel.appendLine(attempt === 1 ? "✅ Build passes." : `✅ Fixed on attempt ${attempt - 1}.`);
        return true;
      }
      if (appliedCount === 0) {
        channel.appendLine("No fixes applied — giving up.");
        return false;
      }
    }
    channel.appendLine(`❌ Could not fix automatically after ${MAX_HEAL_ATTEMPTS} attempts.`);
    return false;
  }

  // applyHealFixes writes the model's corrections to disk.
  // Returns the count of files actually written. Mirrors the CLI's
  // applyHealFixes — no per-file prompt because the user already
  // consented when they clicked "Run & Heal".
  private async applyHealFixes(
    fixes: FileFix[],
    workspaceRoot: string,
    channel: vscode.OutputChannel,
  ): Promise<number> {
    let applied = 0;
    for (const fix of fixes) {
      try {
        const abs = absolutise(fix.file, workspaceRoot); // throws if outside workspace root (S11)
        const enc = new TextEncoder();
        await vscode.workspace.fs.writeFile(
          vscode.Uri.file(abs),
          enc.encode(fix.content),
        );
        channel.appendLine(`  ↪ wrote fix to ${fix.file}`);
        applied++;
      } catch (err) {
        channel.appendLine(`! write ${fix.file}: ${err instanceof Error ? err.message : String(err)}`);
      }
    }
    return applied;
  }

  private ensureOutputChannel(): vscode.OutputChannel {
    if (!this.outputChannel) {
      this.outputChannel = vscode.window.createOutputChannel("Talyvor Agent — Heal");
    }
    return this.outputChannel;
  }

  // createPR opens a GitHub pull request for the current task.
  // Returns the PR URL on success, undefined when the workspace
  // isn't a GitHub repo or the user cancels. The caller (the
  // AgentPanel) drives the QuickPick / inputBox UX.
  async createPR(opts: {
    workspaceRoot: string;
    title?: string;
    base?: string;
    draft?: boolean;
    config: LensConfig;
  }): Promise<string | undefined> {
    if (!this.task) return undefined;
    const pre = await prPreflight(opts.workspaceRoot);
    if (!pre) {
      void vscode.window.showWarningMessage(
        "Not a GitHub repository — skipping PR creation.",
      );
      return undefined;
    }
    let token = resolveToken();
    if (!token) {
      const entered = await promptForToken();
      if (!entered) {
        void vscode.window.showWarningMessage(
          "GitHub token required to open a PR.",
        );
        return undefined;
      }
      token = entered;
    }
    const base = opts.base || pre.defaultBranch;
    if (pre.branch === base) {
      void vscode.window.showWarningMessage(
        `Current branch (${pre.branch}) is the base — switch to a feature branch first.`,
      );
      return undefined;
    }
    const title = (opts.title ?? slugifyBranch(this.task.description)).slice(0, 70);
    const body = generatePRBody(
      this.task.issueId,
      "",
      this.task.description,
      this.task.changes.filter((c) => c.approved).map((c) => c.filePath),
      this.task.totalCostUSD,
    );
    try {
      const res = await createPR(pre.owner, pre.repo, token, {
        title,
        body,
        head: pre.branch,
        base,
        draft: opts.draft ?? false,
      });
      return res.url;
    } catch (err) {
      void vscode.window.showErrorMessage(
        "PR creation failed: " + (err instanceof Error ? err.message : String(err)),
      );
      return undefined;
    }
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

// absolutise (S11 workspace-root confinement) now lives in ./confine-pure so it is unit-testable without
// the VS Code harness; imported at the top of this file.

// tail returns the last `max` characters of s with a leading
// "…" marker when the input was truncated. Used to keep heal
// telemetry small in the AgentTask payload (the full output
// still lives in the OutputChannel).
function tail(s: string, max: number): string {
  if (!s) return "";
  if (s.length <= max) return s;
  return "…" + s.slice(-max);
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
