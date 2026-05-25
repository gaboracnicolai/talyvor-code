// AgentPanel — the webview for multi-file agentic tasks. Lifecycle:
//   - idle: task input form
//   - planning / executing: progress
//   - awaiting_approval: diff list with per-file approve/reject
//   - completed / failed / cancelled: terminal state with summary
//
// The panel forwards user actions to AgentMode and re-renders
// whenever AgentMode emits an update. Single panel per window —
// re-opening reveals the existing one.

import * as vscode from "vscode";
import type { LensClient } from "../lens/client";
import type { LensConfig } from "../lens/types";
import { CostTracker } from "../providers/cost-tracker";
import { AgentMode, type AgentTask } from "../agent/AgentMode";
import type { DiffLine } from "../agent/agent-pure";
import type { IssueContextProvider } from "../track/issue-context";
import { escapeHTML } from "./chat-pure";

type Inbound =
  | { type: "start"; description: string }
  | { type: "approve"; index: number }
  | { type: "reject"; index: number; feedback: string }
  | { type: "regenerate"; index: number; feedback: string }
  | { type: "applyAll" }
  | { type: "applyAndHeal" }
  | { type: "createPR" }
  | { type: "cancel" };

export class AgentPanel {
  private static current: AgentPanel | undefined;
  private readonly disposables: vscode.Disposable[] = [];
  private agent: AgentMode | undefined;
  private prefill = "";

  static createOrShow(
    extensionUri: vscode.Uri,
    lens: LensClient,
    tracker: CostTracker,
    config: LensConfig,
    prefill = "",
    issueContext?: IssueContextProvider,
  ): void {
    if (AgentPanel.current) {
      AgentPanel.current.panel.reveal(vscode.ViewColumn.Beside);
      if (prefill) AgentPanel.current.prefill = prefill;
      AgentPanel.current.panel.webview.html =
        AgentPanel.current.renderHTML(undefined);
      return;
    }
    const panel = vscode.window.createWebviewPanel(
      "talyvorAgent",
      "Talyvor Agent",
      vscode.ViewColumn.Beside,
      {
        enableScripts: true,
        retainContextWhenHidden: true,
        localResourceRoots: [extensionUri],
      },
    );
    AgentPanel.current = new AgentPanel(panel, lens, tracker, config, prefill, issueContext);
  }

  private constructor(
    private readonly panel: vscode.WebviewPanel,
    private readonly lens: LensClient,
    private readonly tracker: CostTracker,
    private config: LensConfig,
    prefill: string,
    private readonly issueContext?: IssueContextProvider,
  ) {
    this.prefill = prefill;
    this.panel.webview.html = this.renderHTML(undefined);
    this.panel.webview.onDidReceiveMessage(
      (raw: Inbound) => void this.handleMessage(raw),
      null,
      this.disposables,
    );
    this.panel.onDidDispose(() => this.dispose(), null, this.disposables);
  }

  private dispose(): void {
    AgentPanel.current = undefined;
    this.panel.dispose();
    while (this.disposables.length) this.disposables.pop()?.dispose();
  }

  private async handleMessage(msg: Inbound): Promise<void> {
    switch (msg.type) {
      case "start":
        await this.startTask(msg.description);
        break;
      case "approve":
        this.agent?.approveChange(msg.index);
        break;
      case "reject":
        this.agent?.rejectChange(msg.index, msg.feedback);
        break;
      case "regenerate":
        if (this.agent) {
          await this.agent.regenerateChange(
            msg.index,
            msg.feedback,
            this.freshConfig(),
            this.workspaceRoot(),
          );
        }
        break;
      case "applyAll":
        if (this.agent) {
          const out = await this.agent.applyApproved(this.freshConfig().workspaceId);
          void vscode.window.showInformationMessage(
            `Applied ${out.applied} change(s)${out.failed ? `, ${out.failed} failed` : ""}.`,
          );
        }
        break;
      case "applyAndHeal":
        if (this.agent) {
          const cfg = this.freshConfig();
          const out = await this.agent.applyAndHeal(
            cfg.workspaceId,
            this.workspaceRoot(),
            cfg,
          );
          const tail = out.healed
            ? "build passes."
            : "build still failing — see the Heal output channel.";
          void vscode.window.showInformationMessage(
            `Applied ${out.applied} change(s); ${tail}`,
          );
        }
        break;
      case "createPR":
        await this.handleCreatePR();
        break;
      case "cancel":
        this.agent?.cancel();
        break;
    }
  }

  // handleCreatePR drives the QuickPick UX for opening a PR from
  // the completed-state action. Confirms title + draft choice
  // before handing off to AgentMode.createPR, which runs the
  // preflight and the GitHub API call.
  private async handleCreatePR(): Promise<void> {
    if (!this.agent) return;
    const task = this.agent.currentTask();
    if (!task) return;
    const cfg = this.freshConfig();
    const defaultTitle = task.description.trim().slice(0, 70);
    const title = await vscode.window.showInputBox({
      title: "PR title",
      prompt: "Confirm or edit the pull-request title",
      value: defaultTitle,
      ignoreFocusOut: true,
    });
    if (!title) return;
    const draftChoice = await vscode.window.showQuickPick(
      [
        { label: "$(git-pull-request) Open PR", draft: false },
        { label: "$(git-pull-request-draft) Open as Draft", draft: true },
      ],
      { title: "Pull request kind", ignoreFocusOut: true },
    );
    if (!draftChoice) return;
    const url = await this.agent.createPR({
      workspaceRoot: this.workspaceRoot(),
      title: title.trim(),
      draft: draftChoice.draft,
      config: cfg,
    });
    if (!url) return;
    const action = await vscode.window.showInformationMessage(
      `✅ PR opened: ${url}`,
      "Open in browser",
    );
    if (action === "Open in browser") {
      await vscode.env.openExternal(vscode.Uri.parse(url));
    }
  }

  private async startTask(description: string): Promise<void> {
    if (!description.trim()) {
      void vscode.window.showWarningMessage("Describe what you want first.");
      return;
    }
    this.agent = new AgentMode(
      this.lens,
      this.tracker,
      (task) => {
        this.panel.webview.html = this.renderHTML(task);
      },
      this.issueContext,
    );
    await this.agent.startTask(
      description.trim(),
      this.freshConfig(),
      this.workspaceRoot(),
    );
  }

  private freshConfig(): LensConfig {
    // Re-read on every send so /setActiveIssue mid-task takes
    // effect immediately.
    const cfg = vscode.workspace.getConfiguration("talyvor");
    return {
      ...this.config,
      activeIssue: cfg.get<string>("activeIssue", this.config.activeIssue),
      model: cfg.get<string>("model", this.config.model),
    };
  }

  private workspaceRoot(): string {
    return vscode.workspace.workspaceFolders?.[0]?.uri.fsPath ?? "";
  }

  // ─── HTML ─────────────────────────────────────────

  private renderHTML(task: AgentTask | undefined): string {
    const cfg = this.freshConfig();
    const body = task ? this.renderTask(task) : this.renderIdle();
    return `<!doctype html><html><head><meta charset="utf-8">
<style>${this.css()}</style>
</head><body>
<header>
  <span class="brand">Talyvor Agent</span>
  <span class="chip">${escapeHTML(cfg.activeIssue || "(no issue)")}</span>
  <span class="status" id="status">${task ? task.status : "idle"}</span>
</header>
<main>${body}</main>
<script>${this.script()}</script>
</body></html>`;
  }

  private renderIdle(): string {
    return `<div class="idle">
  <p class="hint">Describe what you want the agent to build or change. The plan + per-file diffs land here for review before any file is touched.</p>
  <textarea id="taskInput" rows="6" placeholder="e.g. Add JWT authentication to the Express API">${escapeHTML(this.prefill)}</textarea>
  <div class="examples">
    <span>Examples:</span>
    <ul>
      <li>Refactor the database layer to use async/await</li>
      <li>Add unit tests for every exported function in auth.ts</li>
      <li>Migrate the logging library to pino</li>
    </ul>
  </div>
  <button id="startBtn">Start Task</button>
</div>`;
  }

  private renderTask(task: AgentTask): string {
    if (task.status === "planning") {
      return `<div class="progress"><div class="dots"><i></i><i></i><i></i></div><p>Planning…</p></div>`;
    }
    if (task.status === "executing") {
      return `<div class="progress"><div class="dots"><i></i><i></i><i></i></div>
        <p>Generating ${task.changes.length}/${task.plan.length || "?"} files…</p></div>`;
    }
    if (task.status === "failed") {
      return `<div class="error">Failed: ${escapeHTML(task.error ?? "unknown error")}</div>`;
    }
    if (task.status === "cancelled") {
      return `<div class="muted">Cancelled. No files changed.</div>`;
    }
    if (task.status === "completed") {
      const approved = task.changes.filter((c) => c.approved).length;
      const healedBadge = (task.healAttempts ?? []).some((a) => a.success)
        ? `<p class="muted">🔧 Self-healed across ${task.healAttempts!.length} build attempt${task.healAttempts!.length === 1 ? "" : "s"}.</p>`
        : "";
      return `<div class="completed">
        <h2>✅ Task completed</h2>
        <p>Applied ${approved} file change${approved === 1 ? "" : "s"}.</p>
        ${healedBadge}
        <p>Total cost: $${task.totalCostUSD.toFixed(4)}</p>
        ${task.issueId ? `<p>Active issue: <code>${escapeHTML(task.issueId)}</code></p>` : ""}
        <div class="actions">
          <button id="createPRBtn">Create PR</button>
        </div>
      </div>`;
    }
    if (task.status === "healing") {
      return this.renderHealing(task);
    }
    // awaiting_approval / applying
    return this.renderReview(task);
  }

  private renderHealing(task: AgentTask): string {
    const attempts = task.healAttempts ?? [];
    const cards = attempts.map((a) => this.renderHealAttempt(a)).join("");
    return `<div class="healing">
  <h2>🔧 Self-healing</h2>
  <p class="muted">Running the project's build command and asking the model to fix any failures. Streaming output appears in the <code>Talyvor Agent — Heal</code> output channel.</p>
  ${attempts.length > 0 ? `<div class="attempts">${cards}</div>` : `<div class="progress"><div class="dots"><i></i><i></i><i></i></div></div>`}
</div>`;
  }

  private renderHealAttempt(a: AgentTask["healAttempts"] extends Array<infer T> | undefined ? T : never): string {
    const stateClass = a.success ? "ok" : "fail";
    const fixesBlock = a.fixes.length > 0
      ? `<ul class="heal-fixes">${a.fixes.map((f) => `<li><code>${escapeHTML(f.file)}</code></li>`).join("")}</ul>`
      : "";
    return `<article class="heal-attempt ${stateClass}">
  <header>
    <span class="op">attempt ${a.attempt}</span>
    <code>${escapeHTML(a.command)}</code>
    <span class="status-pill">${a.success ? "passed" : `exit ${a.exitCode}`}</span>
  </header>
  ${!a.success ? `<pre class="heal-err">${escapeHTML(a.stderrTail || a.stdoutTail || "(no output)")}</pre>` : ""}
  ${a.appliedCount > 0 ? `<p class="muted">Applied ${a.appliedCount} fix${a.appliedCount === 1 ? "" : "es"}</p>` : ""}
  ${fixesBlock}
</article>`;
  }

  private renderReview(task: AgentTask): string {
    const planList = task.plan
      .map((s) => `<li>${escapeHTML(s)}</li>`)
      .join("");
    const changes = task.changes
      .map((c, i) => this.renderChange(c, i))
      .join("");
    return `<div class="review">
  <section class="plan">
    <h2>Plan</h2>
    <ul>${planList}</ul>
  </section>
  <section class="changes">
    <h2>${task.changes.length} file change${task.changes.length === 1 ? "" : "s"} (cost so far: $${task.totalCostUSD.toFixed(4)})</h2>
    ${changes}
  </section>
  <footer class="actions">
    <button id="applyBtn">Apply approved changes</button>
    <button id="applyAndHealBtn">Run &amp; Heal</button>
    <button id="cancelBtn" class="ghost">Cancel task</button>
  </footer>
</div>`;
  }

  private renderChange(
    c: AgentTask["changes"][number],
    idx: number,
  ): string {
    const badge = c.operation.toUpperCase();
    const approvedClass = c.approved === true
      ? "approved"
      : c.approved === false
        ? "rejected"
        : "pending";
    const diff = c.diff
      .map((line) => this.renderDiffLine(line))
      .join("");
    return `<article class="change ${approvedClass}" data-idx="${idx}">
  <header>
    <span class="op op-${c.operation}">${badge}</span>
    <code>${escapeHTML(c.filePath)}</code>
    <button data-action="approve">Approve</button>
    <button data-action="reject" class="ghost">Reject…</button>
  </header>
  <pre class="diff">${diff || '<span class="muted">No diff — file matches request output.</span>'}</pre>
  ${c.rejectionFeedback
        ? `<div class="feedback">Feedback: ${escapeHTML(c.rejectionFeedback)} <button data-action="regenerate">Regenerate</button></div>`
        : ""}
</article>`;
  }

  private renderDiffLine(line: DiffLine): string {
    switch (line.kind) {
      case "header":
        return `<span class="dh">${escapeHTML(line.text)}</span>\n`;
      case "context":
        return `<span class="dc"> ${escapeHTML(line.text)}</span>\n`;
      case "add":
        return `<span class="da">+${escapeHTML(line.text)}</span>\n`;
      case "remove":
        return `<span class="dr">-${escapeHTML(line.text)}</span>\n`;
    }
  }

  // ─── CSS + script ───

  private css(): string {
    return `body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#d4d8e2;background:#1e1e1e;margin:0;display:flex;flex-direction:column;height:100vh;line-height:1.4;font-size:13px}
header{display:flex;align-items:center;gap:8px;padding:8px 12px;border-bottom:1px solid #2a2a2a;background:#191919}
header .brand{font-weight:600;color:#fff}
.chip{font-size:11px;background:#2a2a2a;color:#f0a030;padding:2px 6px;border-radius:4px;font-family:monospace}
header .status{margin-left:auto;font-size:11px;color:#888;text-transform:uppercase;letter-spacing:0.05em}
main{flex:1;overflow-y:auto;padding:12px}
.idle{display:flex;flex-direction:column;gap:8px;max-width:680px}
.hint{color:#888;font-size:12px;margin:0}
textarea{background:#0c0e12;color:#d4d8e2;border:1px solid #2a2a2a;border-radius:6px;padding:8px;font-family:inherit;font-size:13px;resize:vertical;box-sizing:border-box}
textarea:focus{outline:none;border-color:#f0a030}
.examples{color:#666;font-size:11px}
.examples ul{margin:4px 0 0 16px;padding:0}
button{background:#f0a030;color:#1e1e1e;border:0;border-radius:4px;padding:6px 16px;font-weight:600;cursor:pointer;font-size:12px}
button:hover{opacity:0.9}
button.ghost{background:#2a2a2a;color:#d4d8e2;border:1px solid #333}
.progress{display:flex;flex-direction:column;align-items:center;padding:32px;color:#888}
.dots{display:flex;gap:6px;margin-bottom:12px}
.dots i{width:8px;height:8px;border-radius:50%;background:#888;animation:bounce 1.2s infinite}
.dots i:nth-child(2){animation-delay:0.15s}
.dots i:nth-child(3){animation-delay:0.3s}
@keyframes bounce{0%,80%,100%{transform:scale(0.6);opacity:0.4}40%{transform:scale(1);opacity:1}}
.error{padding:12px;background:#3a1a1a;color:#ff7070;border-radius:6px}
.completed{padding:12px}
.completed h2{color:#5cd187;margin:0 0 8px}
.review h2{font-size:12px;text-transform:uppercase;letter-spacing:0.05em;color:#aaa;margin:16px 0 6px}
.review .plan ul{margin:0;padding-left:18px}
.change{border:1px solid #2a2a2a;border-radius:6px;margin:8px 0;overflow:hidden}
.change.approved{border-color:#5cd187}
.change.rejected{border-color:#ff7070;opacity:0.6}
.change header{background:#13161c;padding:6px 8px;display:flex;align-items:center;gap:8px;border-bottom:1px solid #2a2a2a}
.op{font-size:10px;font-weight:600;padding:2px 6px;border-radius:3px}
.op-create{background:#1a3a1a;color:#5cd187}
.op-modify{background:#3a2a1a;color:#f0a030}
.op-delete{background:#3a1a1a;color:#ff7070}
.change code{font-family:"SF Mono",Menlo,monospace;font-size:12px;color:#d4d8e2;flex:1}
.change header button{padding:3px 10px;font-size:11px}
pre.diff{margin:0;padding:8px;background:#0c0e12;overflow:auto;font-family:"SF Mono",Menlo,Consolas,monospace;font-size:12px;line-height:1.5;color:#d4d8e2;white-space:pre}
.dh{color:#888;display:block}
.dc{color:#888;display:block}
.da{color:#5cd187;display:block}
.dr{color:#ff7070;display:block}
.feedback{padding:8px;background:#13161c;font-size:11px;color:#888;border-top:1px solid #2a2a2a}
.feedback button{margin-left:8px;padding:2px 8px;font-size:11px}
.actions{padding:12px 0;display:flex;gap:8px}
.muted{color:#666}
.healing h2{font-size:13px;color:#aaa;text-transform:uppercase;letter-spacing:0.05em;margin:8px 0}
.healing .attempts{display:flex;flex-direction:column;gap:8px;margin-top:12px}
.heal-attempt{border:1px solid #2a2a2a;border-radius:6px;overflow:hidden}
.heal-attempt.ok{border-color:#5cd187}
.heal-attempt.fail{border-color:#f0a030}
.heal-attempt header{background:#13161c;padding:6px 8px;display:flex;align-items:center;gap:8px;border-bottom:1px solid #2a2a2a}
.heal-attempt .op{font-size:10px;font-weight:600;padding:2px 6px;border-radius:3px;background:#2a2a2a;color:#f0a030}
.heal-attempt .status-pill{margin-left:auto;font-size:10px;text-transform:uppercase;letter-spacing:0.05em;color:#aaa}
.heal-attempt.ok .status-pill{color:#5cd187}
.heal-attempt.fail .status-pill{color:#ff7070}
pre.heal-err{margin:0;padding:8px;background:#0c0e12;color:#ff9090;font-family:"SF Mono",Menlo,Consolas,monospace;font-size:11px;line-height:1.5;white-space:pre-wrap;max-height:200px;overflow:auto}
.heal-fixes{list-style:none;padding:6px 10px;margin:0;font-size:11px;color:#aaa}
.heal-fixes li{padding:2px 0}`;
  }

  private script(): string {
    return `(() => {
const vscode = acquireVsCodeApi();
const start = document.getElementById('startBtn');
if (start) {
  start.addEventListener('click', () => {
    const desc = document.getElementById('taskInput').value;
    vscode.postMessage({type:'start', description: desc});
  });
}
document.querySelectorAll('.change').forEach((node) => {
  const idx = Number(node.dataset.idx);
  node.querySelector('[data-action="approve"]')?.addEventListener('click', () => {
    vscode.postMessage({type:'approve', index: idx});
  });
  node.querySelector('[data-action="reject"]')?.addEventListener('click', () => {
    const feedback = prompt('What needs to change?');
    if (feedback) vscode.postMessage({type:'reject', index: idx, feedback});
  });
  node.querySelector('[data-action="regenerate"]')?.addEventListener('click', () => {
    const fb = prompt('Refine the feedback (optional):') || '';
    vscode.postMessage({type:'regenerate', index: idx, feedback: fb});
  });
});
const applyBtn = document.getElementById('applyBtn');
if (applyBtn) applyBtn.addEventListener('click', () => vscode.postMessage({type:'applyAll'}));
const applyAndHealBtn = document.getElementById('applyAndHealBtn');
if (applyAndHealBtn) applyAndHealBtn.addEventListener('click', () => vscode.postMessage({type:'applyAndHeal'}));
const createPRBtn = document.getElementById('createPRBtn');
if (createPRBtn) createPRBtn.addEventListener('click', () => vscode.postMessage({type:'createPR'}));
const cancelBtn = document.getElementById('cancelBtn');
if (cancelBtn) cancelBtn.addEventListener('click', () => vscode.postMessage({type:'cancel'}));
})();`;
  }
}
