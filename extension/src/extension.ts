// Talyvor Code — VS Code extension entry point.
//
// Phase 1 ships the connective tissue: status bar, three commands
// (set issue, test connection, cost dashboard), config-change
// listener, and a first-install welcome nudge. AI features
// (completions, chat) land in later phases on top of this scaffold.

import * as vscode from "vscode";
import { TalyvorConfig } from "./config";
import { LensClient } from "./lens/client";
import { TrackClient } from "./track/client";
import type { LensConfig } from "./lens/types";

export function activate(context: vscode.ExtensionContext): void {
  let config = TalyvorConfig.getLensConfig();
  let lensClient = new LensClient(config.url, config.apiKey);
  let trackClient = new TrackClient(config.trackUrl, config.trackApiKey);

  const statusBar = vscode.window.createStatusBarItem(
    vscode.StatusBarAlignment.Right,
    100,
  );
  statusBar.command = "talyvor.setActiveIssue";
  context.subscriptions.push(statusBar);
  updateStatusBar(statusBar, config);
  statusBar.show();

  context.subscriptions.push(
    vscode.commands.registerCommand("talyvor.setActiveIssue", () =>
      setActiveIssueCommand(trackClient, TalyvorConfig.getLensConfig()),
    ),
    vscode.commands.registerCommand("talyvor.testConnection", () =>
      testConnectionCommand(lensClient),
    ),
    vscode.commands.registerCommand("talyvor.showCostDashboard", () =>
      showCostDashboard(context, lensClient, TalyvorConfig.getLensConfig()),
    ),
    vscode.commands.registerCommand("talyvor.openChat", () =>
      // Phase 1 stub. Phase 2 wires the side-panel chat surface.
      vscode.window.showInformationMessage(
        "AI Chat ships in Phase 2. Use Test Connection to verify Lens is reachable.",
      ),
    ),
  );

  // Re-build the clients + refresh the status bar whenever the
  // user changes any talyvor.* setting. Cheaper than restarting
  // the extension and keeps the live status accurate.
  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (!e.affectsConfiguration("talyvor")) return;
      config = TalyvorConfig.getLensConfig();
      lensClient = new LensClient(config.url, config.apiKey);
      trackClient = new TrackClient(config.trackUrl, config.trackApiKey);
      updateStatusBar(statusBar, config);
    }),
  );

  // First-install welcome — nudge the user toward settings if
  // Lens isn't wired up yet. We deliberately only fire when both
  // URL + key are missing so re-opening a project with a working
  // config doesn't pop the toast.
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

function updateStatusBar(
  item: vscode.StatusBarItem,
  config: LensConfig,
): void {
  if (!config.url || !config.apiKey) {
    item.text = "$(warning) Talyvor: Not configured";
    item.tooltip = "Click to set up Talyvor Code";
    item.command = "workbench.action.openSettings";
    return;
  }
  if (!config.activeIssue) {
    item.text = "$(sparkle) Talyvor | No issue";
    item.tooltip = "Click to set the active Track issue";
    item.command = "talyvor.setActiveIssue";
    return;
  }
  item.text = `$(sparkle) Talyvor | ${config.activeIssue}`;
  item.tooltip = `Active issue: ${config.activeIssue}. Click to change.`;
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
  // Track lookup is best-effort: if Track isn't reachable we still
  // commit the identifier so cost attribution works (Lens only
  // needs the X-Talyvor-Issue header value).
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

// showCostDashboard renders a minimal webview with the active
// issue + the latest cost figures. Phase 1 keeps the panel small
// and read-only; richer charts land alongside the chat surface.
async function showCostDashboard(
  _context: vscode.ExtensionContext,
  lens: LensClient,
  config: LensConfig,
): Promise<void> {
  const panel = vscode.window.createWebviewPanel(
    "talyvorCost",
    "Talyvor: AI Cost",
    vscode.ViewColumn.Beside,
    { enableScripts: false, retainContextWhenHidden: true },
  );
  const issue = config.activeIssue || "(no active issue)";
  const cost = await lens.getCostForIssue(
    config.workspaceId,
    config.activeIssue,
  );
  panel.webview.html = renderCostHTML(issue, cost.totalCostUSD, cost.tokens);
}

function renderCostHTML(
  issue: string,
  totalUsd: number,
  tokens: number,
): string {
  // Webview content is fully local + inert (scripts disabled), so
  // we don't need a CSP nonce or a sanitiser here.
  return `<!doctype html><html><head><meta charset="utf-8">
<style>
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#ddd;background:#1e1e1e;padding:16px}
h1{font-size:14px;color:#fff;border-bottom:1px solid #333;padding-bottom:8px}
.kv{display:grid;grid-template-columns:140px 1fr;gap:6px;margin-top:12px}
.kv dt{color:#888}
.kv dd{margin:0}
.cost{font-size:28px;color:#f0a030;margin-top:8px}
</style></head><body>
<h1>AI Cost — Talyvor Code</h1>
<dl class="kv">
  <dt>Active issue</dt><dd>${escapeHTML(issue)}</dd>
  <dt>Total spend</dt><dd class="cost">$${totalUsd.toFixed(2)}</dd>
  <dt>Tokens used</dt><dd>${tokens.toLocaleString()}</dd>
</dl>
</body></html>`;
}

function escapeHTML(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}
