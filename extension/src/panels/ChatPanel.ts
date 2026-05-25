// ChatPanel — the AI chat surface. One panel per editor window;
// reveals when already open, creates otherwise. Conversation
// history rides in the panel instance so Cmd+Shift+L re-opens to
// the same thread.

import * as vscode from "vscode";
import type { LensClient } from "../lens/client";
import type { LensConfig, Message } from "../lens/types";
import { CostTracker, estimateCostUSD, formatDuration } from "../providers/cost-tracker";
import type { IssueContextProvider } from "../track/issue-context";
import type { RulesLoader } from "../rules/rules-loader";
import { forLanguage, promptPrefix } from "../rules/rules-pure";
import {
  buildSystemPrompt,
  escapeHTML,
  splitBlocks,
  trimHistory,
} from "./chat-pure";

// Webview-side message envelopes. The HTML posts these via
// acquireVsCodeApi().postMessage; the extension responds with the
// shapes below.
type WebviewInbound =
  | { type: "sendMessage"; text: string; context?: string }
  | { type: "clearHistory" }
  | { type: "setIssue"; issue: string }
  | { type: "insertCode"; code: string };

type WebviewOutbound =
  | { type: "userMessage"; text: string }
  | { type: "assistantMessage"; html: string; costUSD: number }
  | { type: "thinking" }
  | { type: "error"; message: string }
  | { type: "cleared" }
  | {
      type: "session";
      issue: string;
      model: string;
      totalCostUSD: number;
      duration: string;
    };

export class ChatPanel {
  public static currentPanel: ChatPanel | undefined;
  private readonly panel: vscode.WebviewPanel;
  private readonly disposables: vscode.Disposable[] = [];
  private history: Message[] = [];

  static createOrShow(
    extensionUri: vscode.Uri,
    lens: LensClient,
    tracker: CostTracker,
    config: LensConfig,
    issueContext?: IssueContextProvider,
    rulesLoader?: RulesLoader,
  ): void {
    const column =
      vscode.window.activeTextEditor?.viewColumn ?? vscode.ViewColumn.One;
    if (ChatPanel.currentPanel) {
      ChatPanel.currentPanel.panel.reveal(vscode.ViewColumn.Beside);
      ChatPanel.currentPanel.refreshSession(config, tracker);
      return;
    }
    const panel = vscode.window.createWebviewPanel(
      "talyvorChat",
      "Talyvor Chat",
      vscode.ViewColumn.Beside,
      {
        enableScripts: true,
        retainContextWhenHidden: true,
        localResourceRoots: [extensionUri],
      },
    );
    void column;
    ChatPanel.currentPanel = new ChatPanel(panel, lens, tracker, config, issueContext, rulesLoader);
  }

  // sendPrompt is the entry point context-menu commands use to
  // seed the chat with a pre-built question. The panel is shown
  // first; we then dispatch as if the user had typed the text.
  static async sendPrompt(
    extensionUri: vscode.Uri,
    lens: LensClient,
    tracker: CostTracker,
    config: LensConfig,
    prompt: string,
    issueContext?: IssueContextProvider,
    rulesLoader?: RulesLoader,
  ): Promise<void> {
    ChatPanel.createOrShow(extensionUri, lens, tracker, config, issueContext, rulesLoader);
    await ChatPanel.currentPanel?.sendMessage(prompt);
  }

  private constructor(
    panel: vscode.WebviewPanel,
    private readonly lens: LensClient,
    private readonly tracker: CostTracker,
    private config: LensConfig,
    private readonly issueContext?: IssueContextProvider,
    private readonly rulesLoader?: RulesLoader,
  ) {
    this.panel = panel;
    this.panel.webview.html = this.renderHTML();
    // Re-send the session metadata when the webview becomes
    // visible so the footer shows current cost without a manual
    // refresh.
    this.panel.onDidChangeViewState(() => {
      if (this.panel.visible) this.refreshSession(this.config, this.tracker);
    }, null, this.disposables);

    this.panel.webview.onDidReceiveMessage(
      (raw: WebviewInbound) => this.handleMessage(raw),
      null,
      this.disposables,
    );

    this.panel.onDidDispose(() => this.dispose(), null, this.disposables);

    // Initial session snapshot.
    this.refreshSession(config, tracker);
  }

  private dispose(): void {
    ChatPanel.currentPanel = undefined;
    this.panel.dispose();
    while (this.disposables.length) {
      this.disposables.pop()?.dispose();
    }
  }

  // ─── Inbound webview messages ─────────────────────

  private handleMessage(msg: WebviewInbound): void {
    switch (msg.type) {
      case "sendMessage":
        void this.sendMessage(msg.text, msg.context);
        break;
      case "clearHistory":
        this.history = [];
        this.post({ type: "cleared" });
        break;
      case "setIssue":
        // Persist + refresh session footer. The session is
        // synthesised from the latest config so we read back via
        // TalyvorConfig elsewhere; here we just nudge the user
        // to use the command-palette flow.
        void vscode.commands.executeCommand("talyvor.setActiveIssue", msg.issue);
        break;
      case "insertCode":
        void this.insertAtCursor(msg.code);
        break;
    }
  }

  private async insertAtCursor(code: string): Promise<void> {
    const editor = vscode.window.activeTextEditor;
    if (!editor) {
      void vscode.window.showWarningMessage(
        "Open an editor before inserting code.",
      );
      return;
    }
    await editor.edit((edit) => {
      const sel = editor.selection;
      if (sel.isEmpty) edit.insert(sel.active, code);
      else edit.replace(sel, code);
    });
  }

  // ─── Send message ────────────────────────────────

  async sendMessage(userText: string, codeContext?: string): Promise<void> {
    const trimmed = userText.trim();
    if (trimmed.length === 0) return;

    // Reload config each send so /setActiveIssue mid-chat takes
    // effect immediately.
    const cfg = vscode.workspace.getConfiguration("talyvor");
    this.config = {
      ...this.config,
      activeIssue: cfg.get<string>("activeIssue", this.config.activeIssue),
      model: cfg.get<string>("model", this.config.model),
    };

    const userContent =
      codeContext && codeContext.trim().length > 0
        ? `${trimmed}\n\nContext:\n\`\`\`\n${codeContext}\n\`\`\``
        : trimmed;
    this.history.push({ role: "user", content: userContent });
    this.history = trimHistory(this.history);

    this.post({ type: "userMessage", text: trimmed });
    this.post({ type: "thinking" });

    try {
      // Project rules ride at the very start of the system prompt
      // so the model attends to them before its baseline behaviour.
      const rulesPrefix = promptPrefix(
        forLanguage(this.rulesLoader?.get(), ""),
      );
      const systemContent = rulesPrefix
        + buildSystemPrompt(
          this.config.activeIssue,
          this.issueContext?.getIssueContext() ?? "",
        );
      const messages: Message[] = [
        { role: "system", content: systemContent },
        ...this.history,
      ];
      const res = await this.lens.completeWithUsage(
        messages,
        this.config.model,
        "chat",
        this.config.workspaceId,
        this.config.activeIssue,
        2048,
      );
      const cost = estimateCostUSD(res.inputTokens, res.outputTokens);
      this.tracker.recordCompletion(
        res.inputTokens + res.outputTokens,
        cost,
        this.config.activeIssue,
        "chat",
      );
      this.history.push({ role: "assistant", content: res.text });
      this.history = trimHistory(this.history);
      this.post({
        type: "assistantMessage",
        html: this.renderAssistant(res.text),
        costUSD: cost,
      });
      this.refreshSession(this.config, this.tracker);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      this.post({ type: "error", message });
    }
  }

  private renderAssistant(text: string): string {
    const segments = splitBlocks(text);
    return segments
      .map((seg) => {
        if (seg.kind === "text") {
          // Preserve paragraph whitespace by emitting <p> per
          // blank-line block.
          return seg.text
            .split(/\n{2,}/)
            .map((para) => `<p>${escapeHTML(para)}</p>`)
            .join("");
        }
        const lang = seg.code.language || "code";
        return `<div class="code">
  <div class="code-head"><span>${escapeHTML(lang)}</span>
    <button data-action="copy">Copy</button>
    <button data-action="insert">Insert</button>
  </div>
  <pre data-code="${escapeHTML(seg.code.code)}">${escapeHTML(seg.code.code)}</pre>
</div>`;
      })
      .join("");
  }

  private refreshSession(config: LensConfig, tracker: CostTracker): void {
    const s = tracker.getSessionSummary();
    const current = this.issueContext?.getCurrentIssue();
    const issueLabel = current
      ? `${current.identifier} — ${current.title}`
      : config.activeIssue || "(no issue)";
    this.post({
      type: "session",
      issue: issueLabel,
      model: config.model,
      totalCostUSD: s.totalCostUSD,
      duration: formatDuration(s.sessionStart),
    });
  }

  private post(msg: WebviewOutbound): void {
    void this.panel.webview.postMessage(msg);
  }

  // ─── HTML ─────────────────────────────────────────

  // renderHTML returns a single-file webview document. We use
  // retainContextWhenHidden=true so the user keeps their thread
  // when they switch tabs; the message history lives on the
  // class instance, so re-renders are cheap.
  private renderHTML(): string {
    return `<!doctype html><html><head><meta charset="utf-8">
<style>${this.css()}</style>
</head><body>
<header>
  <span class="brand">Talyvor Chat</span>
  <span id="issueChip" class="chip"></span>
  <button id="clearBtn">Clear</button>
</header>
<main id="messages"></main>
<form id="composer" autocomplete="off">
  <textarea id="input" placeholder="Ask anything about the code…" rows="3"></textarea>
  <div class="composer-row">
    <label><input type="checkbox" id="includeFile"> Include current file</label>
    <label><input type="checkbox" id="includeSelection"> Include selection</label>
    <button type="submit" id="sendBtn">Send</button>
  </div>
</form>
<footer>
  <span id="sessionStat">Session $0.00 · 0s · —</span>
</footer>
<script>${this.script()}</script>
</body></html>`;
  }

  // CSS lives inline because the webview blocks external requests
  // by default and the bundle stays under 4KB. Colors mirror the
  // VS Code dark palette + the Talyvor amber accent.
  private css(): string {
    return `body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#d4d8e2;background:#1e1e1e;margin:0;display:flex;flex-direction:column;height:100vh;line-height:1.4}
header{display:flex;align-items:center;gap:8px;padding:8px 12px;border-bottom:1px solid #2a2a2a;background:#191919}
header .brand{font-weight:600;color:#fff}
.chip{font-size:11px;background:#2a2a2a;color:#f0a030;padding:2px 6px;border-radius:4px;font-family:monospace}
header button{margin-left:auto;background:#2a2a2a;color:#aaa;border:1px solid #333;border-radius:4px;padding:4px 10px;cursor:pointer}
header button:hover{color:#fff}
main{flex:1;overflow-y:auto;padding:12px;display:flex;flex-direction:column;gap:10px}
.msg{padding:8px 12px;border-radius:6px;max-width:90%;font-size:13px}
.msg.user{align-self:flex-end;background:#1a3a5c;color:#e6f0fa}
.msg.assistant{align-self:flex-start;background:#1a1d24}
.msg.assistant p{margin:0 0 8px}
.msg.assistant p:last-child{margin-bottom:0}
.code{background:#0c0e12;border:1px solid #1f242c;border-radius:6px;margin:6px 0;overflow:hidden}
.code-head{display:flex;align-items:center;gap:6px;padding:4px 8px;background:#13161c;font-size:11px;color:#888}
.code-head span{flex:1;font-family:monospace}
.code-head button{background:transparent;color:#888;border:1px solid #2a2a2a;border-radius:3px;font-size:10px;padding:2px 6px;cursor:pointer}
.code-head button:hover{color:#f0a030;border-color:#f0a030}
.code pre{margin:0;padding:8px;font-family:"SF Mono",Menlo,Consolas,monospace;font-size:12px;white-space:pre-wrap;overflow-x:auto;color:#d4d8e2}
.thinking{display:flex;gap:4px;align-self:flex-start;padding:8px 12px}
.thinking i{width:6px;height:6px;border-radius:50%;background:#888;animation:bounce 1.2s infinite}
.thinking i:nth-child(2){animation-delay:0.15s}
.thinking i:nth-child(3){animation-delay:0.3s}
@keyframes bounce{0%,80%,100%{transform:scale(0.6);opacity:0.4}40%{transform:scale(1);opacity:1}}
.error{color:#ff7070;font-size:12px;padding:8px 12px;background:#3a1a1a;border-radius:6px;align-self:stretch}
form{padding:8px 12px;border-top:1px solid #2a2a2a;background:#191919}
textarea{width:100%;background:#0c0e12;color:#d4d8e2;border:1px solid #2a2a2a;border-radius:6px;padding:8px;font-family:inherit;font-size:13px;resize:none;box-sizing:border-box}
textarea:focus{outline:none;border-color:#f0a030}
.composer-row{display:flex;align-items:center;gap:10px;margin-top:6px;font-size:11px;color:#888}
.composer-row button{margin-left:auto;background:#f0a030;color:#1e1e1e;border:0;border-radius:4px;padding:6px 16px;font-weight:600;cursor:pointer}
.composer-row button:hover{opacity:0.9}
.composer-row button:disabled{opacity:0.5;cursor:not-allowed}
.shake{animation:shake 0.3s}
@keyframes shake{0%,100%{transform:translateX(0)}25%{transform:translateX(-3px)}75%{transform:translateX(3px)}}
footer{padding:6px 12px;border-top:1px solid #2a2a2a;background:#191919;font-size:11px;color:#666}`;
  }

  // The script handles all webview-side wiring: message routing,
  // composer submission, code-block button delegation. Keeps the
  // host (extension.ts) free of webview innards.
  private script(): string {
    return `(() => {
const vscode = acquireVsCodeApi();
const messages = document.getElementById('messages');
const input = document.getElementById('input');
const sendBtn = document.getElementById('sendBtn');
const issueChip = document.getElementById('issueChip');
const sessionStat = document.getElementById('sessionStat');
const includeFile = document.getElementById('includeFile');
const includeSelection = document.getElementById('includeSelection');
const composer = document.getElementById('composer');
const clearBtn = document.getElementById('clearBtn');

let thinkingEl = null;
let session = {model: '', totalCostUSD: 0};

function appendMsg(cls, html) {
  const div = document.createElement('div');
  div.className = 'msg ' + cls;
  div.innerHTML = html;
  messages.appendChild(div);
  messages.scrollTop = messages.scrollHeight;
  return div;
}

function showThinking() {
  if (thinkingEl) return;
  thinkingEl = document.createElement('div');
  thinkingEl.className = 'thinking';
  thinkingEl.innerHTML = '<i></i><i></i><i></i>';
  messages.appendChild(thinkingEl);
  messages.scrollTop = messages.scrollHeight;
}
function hideThinking() {
  thinkingEl?.remove();
  thinkingEl = null;
}

composer.addEventListener('submit', (e) => {
  e.preventDefault();
  const text = input.value.trim();
  if (!text) {
    input.classList.add('shake');
    setTimeout(() => input.classList.remove('shake'), 350);
    return;
  }
  vscode.postMessage({
    type: 'sendMessage',
    text,
    includeFile: includeFile.checked,
    includeSelection: includeSelection.checked,
  });
  input.value = '';
  input.focus();
});

input.addEventListener('keydown', (e) => {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault();
    composer.dispatchEvent(new Event('submit'));
  }
});

clearBtn.addEventListener('click', () => {
  vscode.postMessage({type: 'clearHistory'});
});

// Code block buttons — delegated so dynamic appendMsg works.
messages.addEventListener('click', (e) => {
  const btn = e.target.closest('button[data-action]');
  if (!btn) return;
  const pre = btn.closest('.code')?.querySelector('pre');
  if (!pre) return;
  const code = pre.dataset.code || pre.textContent || '';
  if (btn.dataset.action === 'copy') {
    navigator.clipboard?.writeText(code);
    btn.textContent = 'Copied';
    setTimeout(() => (btn.textContent = 'Copy'), 1200);
  } else if (btn.dataset.action === 'insert') {
    vscode.postMessage({type: 'insertCode', code});
  }
});

window.addEventListener('message', (e) => {
  const m = e.data;
  switch (m.type) {
    case 'userMessage':
      appendMsg('user', escapeHTML(m.text).replace(/\\n/g, '<br>'));
      break;
    case 'thinking':
      showThinking();
      break;
    case 'assistantMessage':
      hideThinking();
      appendMsg('assistant', m.html);
      break;
    case 'error':
      hideThinking();
      appendMsg('error', escapeHTML(m.message));
      break;
    case 'cleared':
      messages.innerHTML = '';
      hideThinking();
      break;
    case 'session':
      session = m;
      issueChip.textContent = m.issue;
      sessionStat.textContent =
        'Session $' + m.totalCostUSD.toFixed(4) + ' · ' + m.duration + ' · ' + m.model;
      break;
  }
});

function escapeHTML(s) {
  return s.replace(/[&<>"']/g, (ch) =>
    ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[ch]),
  );
}
})();`;
  }
}
