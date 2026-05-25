// Talyvor Code — VS Code extension entry point.
//
// Phase 2 adds the InlineCompletionItemProvider and wires the
// session cost tracker into the status bar + dashboard. The
// status bar shows a running spend ("$0.03") that updates after
// every completion.

import * as vscode from "vscode";
import { TalyvorConfig } from "./config";
import { LensClient } from "./lens/client";
import { TrackClient } from "./track/client";
import type { LensConfig } from "./lens/types";
import { CostTracker, formatDuration } from "./providers/cost-tracker";
import { TalyvorCompletionProvider } from "./providers/completions";
import { ChatPanel } from "./panels/ChatPanel";
import { TestGenerator } from "./providers/test-generator";
import { TestPanel } from "./panels/TestPanel";

export function activate(context: vscode.ExtensionContext): void {
  let config = TalyvorConfig.getLensConfig();
  let lensClient = new LensClient(config.url, config.apiKey);
  let trackClient = new TrackClient(config.trackUrl, config.trackApiKey);
  const tracker = new CostTracker();

  const statusBar = vscode.window.createStatusBarItem(
    vscode.StatusBarAlignment.Right,
    100,
  );
  context.subscriptions.push(statusBar);
  updateStatusBar(statusBar, config, tracker);
  statusBar.show();

  // Completion provider registered for every file. setOnUpdate
  // lets the provider refresh the status bar after each successful
  // completion without holding a reference to the bar itself.
  const completionProvider = new TalyvorCompletionProvider(
    lensClient,
    () => TalyvorConfig.getLensConfig(),
    tracker,
  );
  completionProvider.setOnUpdate(() =>
    updateStatusBar(statusBar, TalyvorConfig.getLensConfig(), tracker),
  );
  context.subscriptions.push(
    vscode.languages.registerInlineCompletionItemProvider(
      { pattern: "**" },
      completionProvider,
    ),
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("talyvor.setActiveIssue", () =>
      setActiveIssueCommand(trackClient, TalyvorConfig.getLensConfig()),
    ),
    vscode.commands.registerCommand("talyvor.testConnection", () =>
      testConnectionCommand(lensClient),
    ),
    vscode.commands.registerCommand("talyvor.showCostDashboard", () =>
      showCostDashboard(lensClient, TalyvorConfig.getLensConfig(), tracker),
    ),
    vscode.commands.registerCommand("talyvor.openChat", () =>
      ChatPanel.createOrShow(
        context.extensionUri,
        lensClient,
        tracker,
        TalyvorConfig.getLensConfig(),
      ),
    ),
    vscode.commands.registerCommand("talyvor.explainCode", () =>
      runContextPrompt(
        context.extensionUri,
        lensClient,
        tracker,
        "Explain this code:",
      ),
    ),
    vscode.commands.registerCommand("talyvor.fixError", () =>
      runFixErrorCommand(context.extensionUri, lensClient, tracker),
    ),
    vscode.commands.registerCommand("talyvor.refactorCode", () =>
      runContextPrompt(
        context.extensionUri,
        lensClient,
        tracker,
        "Refactor this code to be cleaner and more maintainable:",
      ),
    ),
    vscode.commands.registerCommand("talyvor.generateTests", () =>
      runGenerateTestsCommand(context.extensionUri, lensClient, tracker, false),
    ),
    vscode.commands.registerCommand("talyvor.generateTestsForFile", () =>
      runGenerateTestsCommand(context.extensionUri, lensClient, tracker, true),
    ),
  );

  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (!e.affectsConfiguration("talyvor")) return;
      config = TalyvorConfig.getLensConfig();
      lensClient = new LensClient(config.url, config.apiKey);
      trackClient = new TrackClient(config.trackUrl, config.trackApiKey);
      updateStatusBar(statusBar, config, tracker);
    }),
  );

  if (!config.url || !config.apiKey) {
    vscode.window
      .showInformationMessage(
        "Talyvor Code installed! Configure your Lens URL and API key to get started.",
        "Open Settings",
      )
      .then((action) => {
        if (action === "Open Settings") {
          void vscode.commands.executeCommand(
            "workbench.action.openSettings",
            "talyvor",
          );
        }
      });
  }
}

export function deactivate(): void {
  // Disposables registered on context.subscriptions are cleaned up
  // by VS Code automatically. Nothing else to tear down today.
}

// ─── Status bar ─────────────────────────────────────

// Status bar template now includes the running session cost:
//   "$(warning) Talyvor: Not configured"
//   "$(sparkle) Talyvor | No issue | $0.00"
//   "$(sparkle) Talyvor | ENG-42 | $0.03"
function updateStatusBar(
  item: vscode.StatusBarItem,
  config: LensConfig,
  tracker: CostTracker,
): void {
  if (!config.url || !config.apiKey) {
    item.text = "$(warning) Talyvor: Not configured";
    item.tooltip = "Click to set up Talyvor Code";
    item.command = "workbench.action.openSettings";
    return;
  }
  const summary = tracker.getSessionSummary();
  const costStr = `$${summary.totalCostUSD.toFixed(2)}`;
  if (!config.activeIssue) {
    item.text = `$(sparkle) Talyvor | No issue | ${costStr}`;
    item.tooltip = `Session cost: ${costStr}. Click to set an active issue.`;
    item.command = "talyvor.setActiveIssue";
    return;
  }
  item.text = `$(sparkle) Talyvor | ${config.activeIssue} | ${costStr}`;
  item.tooltip = `Active issue: ${config.activeIssue}. Session cost: ${costStr}. Click to change.`;
  item.command = "talyvor.setActiveIssue";
}

// ─── Commands ──────────────────────────────────────

async function setActiveIssueCommand(
  track: TrackClient,
  config: LensConfig,
): Promise<void> {
  const input = await vscode.window.showInputBox({
    title: "Set active Talyvor issue",
    prompt: "Enter the Track issue identifier (e.g. ENG-42)",
    value: config.activeIssue,
    placeHolder: "ENG-42",
  });
  if (input === undefined) return;
  const id = input.trim();
  if (id === "") {
    await TalyvorConfig.setActiveIssue("");
    void vscode.window.showInformationMessage("Active issue cleared.");
    return;
  }
  const issue = await track.getIssue(config.workspaceId, id);
  await TalyvorConfig.setActiveIssue(id);
  if (issue) {
    void vscode.window.showInformationMessage(
      `Active issue: ${issue.identifier} — ${issue.title}`,
    );
  } else {
    void vscode.window.showWarningMessage(
      `Active issue set to ${id} (Track lookup failed — costs will still attribute).`,
    );
  }
}

async function testConnectionCommand(lens: LensClient): Promise<void> {
  if (!lens.isConfigured()) {
    void vscode.window.showErrorMessage(
      "Lens is not configured. Set talyvor.lensUrl and talyvor.lensApiKey.",
    );
    return;
  }
  const status = await lens.getStatus();
  if (status.available) {
    void vscode.window.showInformationMessage(
      `✅ Connected to Lens v${status.version}`,
    );
  } else {
    void vscode.window.showErrorMessage(
      "❌ Cannot connect to Lens — check the URL and your network.",
    );
  }
}

async function showCostDashboard(
  lens: LensClient,
  config: LensConfig,
  tracker: CostTracker,
): Promise<void> {
  const panel = vscode.window.createWebviewPanel(
    "talyvorCost",
    "Talyvor: AI Cost",
    vscode.ViewColumn.Beside,
    { enableScripts: false, retainContextWhenHidden: true },
  );
  const issue = config.activeIssue || "(no active issue)";
  const issueCost = await lens.getCostForIssue(
    config.workspaceId,
    config.activeIssue,
  );
  const summary = tracker.getSessionSummary();
  panel.webview.html = renderCostHTML(
    issue,
    issueCost.totalCostUSD,
    summary,
    config.model,
  );
}

function renderCostHTML(
  issue: string,
  issueTotalUsd: number,
  session: ReturnType<CostTracker["getSessionSummary"]>,
  model: string,
): string {
  const byIssueRows = Object.entries(session.byIssue)
    .sort((a, b) => b[1] - a[1])
    .map(
      ([k, v]) =>
        `<tr><td>${escapeHTML(k)}</td><td>$${v.toFixed(4)}</td></tr>`,
    )
    .join("");
  return `<!doctype html><html><head><meta charset="utf-8">
<style>
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#ddd;background:#1e1e1e;padding:16px;line-height:1.45}
h1{font-size:14px;color:#fff;border-bottom:1px solid #333;padding-bottom:8px;margin-top:0}
h2{font-size:12px;color:#aaa;text-transform:uppercase;letter-spacing:0.05em;margin-top:24px}
.kv{display:grid;grid-template-columns:160px 1fr;gap:6px}
.kv dt{color:#888}
.kv dd{margin:0;color:#ddd}
.cost{font-size:24px;color:#f0a030}
table{width:100%;border-collapse:collapse;margin-top:8px;font-size:12px}
td{padding:4px 8px;border-bottom:1px solid #2a2a2a}
.muted{color:#666;font-size:11px}
</style></head><body>
<h1>Talyvor Code — Session Cost Dashboard</h1>

<h2>🎯 Active issue</h2>
<dl class="kv">
  <dt>Issue</dt><dd>${escapeHTML(issue)}</dd>
  <dt>Total AI cost</dt><dd class="cost">$${issueTotalUsd.toFixed(2)}</dd>
</dl>

<h2>This session</h2>
<dl class="kv">
  <dt>Completions</dt><dd>${session.completionCount}</dd>
  <dt>Tokens used</dt><dd>${session.totalTokens.toLocaleString()}</dd>
  <dt>Cost</dt><dd>$${session.totalCostUSD.toFixed(4)}</dd>
  <dt>Duration</dt><dd>${formatDuration(session.sessionStart)}</dd>
  <dt>Model</dt><dd>${escapeHTML(model)}</dd>
</dl>

${
  Object.keys(session.byIssue).length > 1
    ? `<h2>By issue</h2><table>${byIssueRows}</table>`
    : ""
}

<p class="muted">Active-issue total comes from Lens analytics; session figures are estimated locally.</p>
</body></html>`;
}

function escapeHTML(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

// ─── Chat-launching helpers ─────────────────────────

// runContextPrompt grabs the active editor selection (falling back
// to the current line) and seeds the chat with the supplied
// instruction. The chat panel handles the actual Lens round-trip.
async function runContextPrompt(
  extensionUri: vscode.Uri,
  lens: LensClient,
  tracker: CostTracker,
  instruction: string,
): Promise<void> {
  const editor = vscode.window.activeTextEditor;
  if (!editor) {
    void vscode.window.showWarningMessage(
      "Open a file to use this command.",
    );
    return;
  }
  const selected = editor.document.getText(editor.selection);
  const snippet = selected.trim().length > 0
    ? selected
    : editor.document.lineAt(editor.selection.active.line).text;
  const lang = editor.document.languageId;
  const prompt = `${instruction}\n\n\`\`\`${lang}\n${snippet}\n\`\`\``;
  await ChatPanel.sendPrompt(
    extensionUri,
    lens,
    tracker,
    TalyvorConfig.getLensConfig(),
    prompt,
  );
}

// runGenerateTestsCommand drives the test-generation flow. When
// `wholeFile` is true (the "for File" command) we ignore any
// selection and use the entire document; otherwise we prefer the
// selection if non-empty, falling back to the file. A progress
// notification covers the 5-10s Sonnet round-trip.
async function runGenerateTestsCommand(
  extensionUri: vscode.Uri,
  lens: LensClient,
  tracker: CostTracker,
  wholeFile: boolean,
): Promise<void> {
  const editor = vscode.window.activeTextEditor;
  if (!editor) {
    void vscode.window.showWarningMessage(
      "Open a file to generate tests.",
    );
    return;
  }
  const useSelection =
    !wholeFile && !editor.selection.isEmpty;
  const code = useSelection
    ? editor.document.getText(editor.selection)
    : editor.document.getText();
  if (code.trim().length === 0) {
    void vscode.window.showWarningMessage("Nothing to test.");
    return;
  }
  const cfg = TalyvorConfig.getLensConfig();
  if (!cfg.url || !cfg.apiKey) {
    void vscode.window.showErrorMessage(
      "Talyvor is not configured. Set talyvor.lensUrl and talyvor.lensApiKey.",
    );
    return;
  }
  const gen = new TestGenerator(lens, tracker);
  await vscode.window.withProgress(
    {
      location: vscode.ProgressLocation.Notification,
      title: "Generating tests…",
      cancellable: false,
    },
    async () => {
      try {
        const tests = await gen.generateTests(
          code,
          editor.document.languageId,
          editor.document.uri.fsPath,
          cfg,
        );
        TestPanel.show(extensionUri, tests, editor.document.uri);
      } catch (err) {
        const msg = err instanceof Error ? err.message : String(err);
        void vscode.window.showErrorMessage(
          "Test generation failed: " + msg,
        );
      }
    },
  );
}

// runFixErrorCommand reads VS Code diagnostics at the cursor and
// builds a "fix this error" prompt around the surrounding code
// context. When no diagnostics are present, falls back to a
// generic "what's wrong here" prompt.
async function runFixErrorCommand(
  extensionUri: vscode.Uri,
  lens: LensClient,
  tracker: CostTracker,
): Promise<void> {
  const editor = vscode.window.activeTextEditor;
  if (!editor) {
    void vscode.window.showWarningMessage(
      "Open a file to use this command.",
    );
    return;
  }
  const diagnostics = vscode.languages.getDiagnostics(editor.document.uri);
  const cursor = editor.selection.active;
  const here = diagnostics.find((d) => d.range.contains(cursor));
  const errorMsg = here?.message ?? "Unknown error";
  // Pull a tight context window (5 lines before, 5 after) so the
  // model can see surrounding code without bloating the prompt.
  const startLine = Math.max(0, cursor.line - 5);
  const endLine = Math.min(editor.document.lineCount - 1, cursor.line + 5);
  const context = editor.document.getText(
    new vscode.Range(startLine, 0, endLine, editor.document.lineAt(endLine).text.length),
  );
  const lang = editor.document.languageId;
  const prompt = here
    ? `Fix this error: ${errorMsg}\n\n\`\`\`${lang}\n${context}\n\`\`\``
    : `Something looks wrong around here. What's going on?\n\n\`\`\`${lang}\n${context}\n\`\`\``;
  await ChatPanel.sendPrompt(
    extensionUri,
    lens,
    tracker,
    TalyvorConfig.getLensConfig(),
    prompt,
  );
}
