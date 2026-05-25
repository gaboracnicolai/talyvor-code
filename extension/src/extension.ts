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
      vscode.window.showInformationMessage(
        "AI Chat ships in Phase 3. Inline completions are live; use the status bar to set the active issue.",
      ),
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
