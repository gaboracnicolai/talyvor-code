// Talyvor Code — VS Code extension entry point.
//
// Phase 6 promotes Track integration to a first-class concern:
//   - IssueContextProvider holds the active issue and feeds it
//     into every AI prompt (completions, chat, agent, tests).
//   - TalyvorStatusBar shows issue + session cost and drives the
//     5-minute cost-sync to Track.
//   - issue-commands.ts owns the QuickPick + "show issue" flows.

import * as vscode from "vscode";
import { TalyvorConfig } from "./config";
import { LensClient } from "./lens/client";
import { TrackClient } from "./track/client";
import type { LensConfig } from "./lens/types";
import {
  CostTracker,
  formatDuration,
  type FeatureUsage,
} from "./providers/cost-tracker";
import { TalyvorCompletionProvider } from "./providers/completions";
import { ChatPanel } from "./panels/ChatPanel";
import { TestGenerator } from "./providers/test-generator";
import { TestPanel } from "./panels/TestPanel";
import { AgentPanel } from "./panels/AgentPanel";
import { IssueContextProvider } from "./track/issue-context";
import { TalyvorStatusBar } from "./track/status-bar";
import {
  createIssueFromCodeCommand,
  setActiveIssueCommand,
  showIssueCommand,
} from "./commands/issue-commands";
import { DocsClient } from "./docs/docs-client";
import { DocsHoverProvider } from "./docs/docs-hover";
import { SpecWatcher } from "./docs/spec-watcher";
import { RulesLoader } from "./rules/rules-loader";
import { ContextLoader } from "./context/context-loader";
import { generateContextCommand } from "./commands/context-command";
import { buildIndexCommand } from "./commands/index-command";
import { ScopeManager } from "./scope/scope-manager";
import { setScopeCommand } from "./commands/scope-command";
import {
  askDocsCommand,
  linkDocToIssueCommand,
  openDocsCommand,
  searchDocsCommand,
} from "./commands/docs-commands";
import { generateShellCommand } from "./commands/shell-command";
import { selectModelCommand } from "./commands/model-selector";
import { reviewPRCommand, reviewSelectionCommand } from "./commands/pr-review";

export async function activate(context: vscode.ExtensionContext): Promise<void> {
  // Move any plaintext API keys into SecretStorage and populate the credential cache BEFORE any client
  // is built, so every synchronous getLensConfig() below sees the migrated keys. One-time notice when
  // something was actually migrated.
  const migratedCount = await TalyvorConfig.initSecrets(context.secrets);
  if (migratedCount > 0) {
    void vscode.window.showInformationMessage(
      `Talyvor: moved ${migratedCount} API credential(s) from plaintext settings into the OS keychain (SecretStorage). Your settings.json no longer contains them.`,
    );
  }

  let config = TalyvorConfig.getLensConfig();
  let lensClient = new LensClient(config.url, config.apiKey);
  let trackClient = new TrackClient(config.trackUrl, config.trackApiKey);
  let docsClient = new DocsClient(config.docsUrl, config.docsApiKey);
  const tracker = new CostTracker();
  const issueProvider = new IssueContextProvider(trackClient, lensClient);
  const specWatcher = new SpecWatcher(docsClient);
  const rulesLoader = new RulesLoader();
  void rulesLoader.initialize();
  const contextLoader = new ContextLoader();
  void contextLoader.initialize();
  const scopeManager = new ScopeManager();
  void scopeManager.initialize();
  context.subscriptions.push(specWatcher, rulesLoader, contextLoader, scopeManager);

  // Listen for every Lens call's cost and roll it into the
  // per-issue session bucket so the sync timer has something to
  // push.
  context.subscriptions.push(
    tracker.onRecord((e) => issueProvider.recordCost(e.issueId, e.costUSD)),
  );

  const statusBar = new TalyvorStatusBar(context);
  const refreshStatusBar = () => {
    const s = tracker.getSessionSummary();
    statusBar.update(TalyvorConfig.getLensConfig(), s.totalCostUSD, s.totalTokens);
  };
  refreshStatusBar();
  issueProvider.onUpdate(refreshStatusBar);
  context.subscriptions.push(
    tracker.onRecord(refreshStatusBar),
    statusBar.bindScopeManager(scopeManager),
  );

  // Resolve the persisted active issue on startup so prompts pick
  // up the title/description without waiting for the first
  // setActiveIssue call.
  if (config.activeIssue && config.workspaceId) {
    void issueProvider.setActiveIssue(config.activeIssue, config.workspaceId);
  }

  if (config.workspaceId) {
    statusBar.startCostSync(issueProvider, config.workspaceId);
  }

  // Completion provider registered for every file. setOnUpdate
  // lets the provider refresh the status bar after each successful
  // completion without holding a reference to the bar itself.
  const completionProvider = new TalyvorCompletionProvider(
    lensClient,
    () => TalyvorConfig.getLensConfig(),
    tracker,
    issueProvider,
    rulesLoader,
    contextLoader,
    scopeManager,
  );
  completionProvider.setOnUpdate(refreshStatusBar);
  context.subscriptions.push(
    vscode.languages.registerInlineCompletionItemProvider(
      { pattern: "**" },
      completionProvider,
    ),
  );

  // Hover provider only registers when Docs is configured — most
  // teams will not have a Docs instance running, and we don't want
  // to add a no-op hover handler to every file's hover path.
  if (docsClient.isConfigured()) {
    const hoverProvider = new DocsHoverProvider(
      docsClient,
      () => TalyvorConfig.getLensConfig(),
    );
    context.subscriptions.push(
      vscode.languages.registerHoverProvider(
        { pattern: "**" },
        hoverProvider,
      ),
    );
  }

  context.subscriptions.push(
    vscode.commands.registerCommand("talyvor.setActiveIssue", () =>
      setActiveIssueCommand(issueProvider, trackClient, TalyvorConfig.getLensConfig()),
    ),
    vscode.commands.registerCommand("talyvor.showActiveIssue", () =>
      showIssueCommand(issueProvider, TalyvorConfig.getLensConfig()),
    ),
    vscode.commands.registerCommand("talyvor.createIssueFromCode", () =>
      createIssueFromCodeCommand(TalyvorConfig.getLensConfig()),
    ),
    vscode.commands.registerCommand("talyvor.testConnection", () =>
      testConnectionCommand(lensClient),
    ),
    vscode.commands.registerCommand("talyvor.showCostDashboard", () =>
      showCostDashboard(lensClient, TalyvorConfig.getLensConfig(), tracker, issueProvider),
    ),
    vscode.commands.registerCommand("talyvor.openChat", () =>
      ChatPanel.createOrShow(
        context.extensionUri,
        lensClient,
        tracker,
        TalyvorConfig.getLensConfig(),
        issueProvider,
        rulesLoader,
        contextLoader,
        scopeManager,
      ),
    ),
    vscode.commands.registerCommand("talyvor.generateContext", () =>
      generateContextCommand(lensClient, tracker, TalyvorConfig.getLensConfig()),
    ),
    vscode.commands.registerCommand("talyvor.buildIndex", () =>
      buildIndexCommand(lensClient, TalyvorConfig.getLensConfig()),
    ),
    vscode.commands.registerCommand("talyvor.setScope", () =>
      setScopeCommand(scopeManager),
    ),
    vscode.commands.registerCommand("talyvor.explainCode", () =>
      runContextPrompt(
        context.extensionUri,
        lensClient,
        tracker,
        issueProvider,
        "Explain this code:",
        rulesLoader,
        contextLoader,
        scopeManager,
      ),
    ),
    vscode.commands.registerCommand("talyvor.fixError", () =>
      runFixErrorCommand(context.extensionUri, lensClient, tracker, issueProvider, rulesLoader, contextLoader, scopeManager),
    ),
    vscode.commands.registerCommand("talyvor.refactorCode", () =>
      runContextPrompt(
        context.extensionUri,
        lensClient,
        tracker,
        issueProvider,
        "Refactor this code to be cleaner and more maintainable:",
        rulesLoader,
        contextLoader,
        scopeManager,
      ),
    ),
    vscode.commands.registerCommand("talyvor.generateTests", () =>
      runGenerateTestsCommand(context.extensionUri, lensClient, tracker, false),
    ),
    vscode.commands.registerCommand("talyvor.generateTestsForFile", () =>
      runGenerateTestsCommand(context.extensionUri, lensClient, tracker, true),
    ),
    vscode.commands.registerCommand("talyvor.startAgentTask", () =>
      runStartAgentCommand(context.extensionUri, lensClient, tracker, issueProvider, false),
    ),
    vscode.commands.registerCommand("talyvor.agentFromIssue", () =>
      runStartAgentCommand(context.extensionUri, lensClient, tracker, issueProvider, true),
    ),
    vscode.commands.registerCommand("talyvor.openDocs", () =>
      openDocsCommand(context.extensionUri, docsClient, TalyvorConfig.getLensConfig()),
    ),
    vscode.commands.registerCommand("talyvor.searchDocs", () =>
      searchDocsCommand(context.extensionUri, docsClient, TalyvorConfig.getLensConfig()),
    ),
    vscode.commands.registerCommand("talyvor.askDocs", () =>
      askDocsCommand(context.extensionUri, docsClient, TalyvorConfig.getLensConfig()),
    ),
    vscode.commands.registerCommand("talyvor.linkDocToIssue", () =>
      linkDocToIssueCommand(
        context.extensionUri,
        docsClient,
        TalyvorConfig.getLensConfig(),
        issueProvider,
      ),
    ),
    vscode.commands.registerCommand("talyvor.generateShellCommand", () =>
      generateShellCommand(lensClient, tracker, TalyvorConfig.getLensConfig()),
    ),
    vscode.commands.registerCommand("talyvor.selectModel", () =>
      selectModelCommand(),
    ),
    vscode.commands.registerCommand("talyvor.reviewPR", () =>
      reviewPRCommand(lensClient, tracker, TalyvorConfig.getLensConfig(), issueProvider),
    ),
    vscode.commands.registerCommand("talyvor.reviewSelection", () =>
      reviewSelectionCommand(
        context.extensionUri,
        lensClient,
        tracker,
        TalyvorConfig.getLensConfig(),
        issueProvider,
      ),
    ),
  );

  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration(async (e) => {
      if (!e.affectsConfiguration("talyvor")) return;
      // If the user re-typed a credential into settings.json, migrate it back out and refresh the
      // cache before rebuilding clients (idempotent; a no-op when no plaintext key changed).
      if (TalyvorConfig.secretKeys.some((k) => e.affectsConfiguration(`talyvor.${k}`))) {
        await TalyvorConfig.initSecrets(context.secrets);
      }
      config = TalyvorConfig.getLensConfig();
      lensClient = new LensClient(config.url, config.apiKey);
      trackClient = new TrackClient(config.trackUrl, config.trackApiKey);
      docsClient = new DocsClient(config.docsUrl, config.docsApiKey);
      refreshStatusBar();
      // Restart sync with the (possibly new) workspaceId.
      if (config.workspaceId) {
        statusBar.startCostSync(issueProvider, config.workspaceId);
      } else {
        statusBar.stopCostSync();
      }
    }),
    statusBar,
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

// ─── Commands ──────────────────────────────────────

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
  provider: IssueContextProvider,
): Promise<void> {
  const panel = vscode.window.createWebviewPanel(
    "talyvorCost",
    "Talyvor: AI Cost",
    vscode.ViewColumn.Beside,
    { enableScripts: false, retainContextWhenHidden: true },
  );
  const issueCost = await lens.getCostForIssue(
    config.workspaceId,
    config.activeIssue,
  );
  const summary = tracker.getSessionSummary();
  const current = provider.getCurrentIssue();
  panel.webview.html = renderCostHTML(
    config.activeIssue || "(no active issue)",
    current?.title ?? "",
    current?.status ?? "",
    issueCost.totalCostUSD,
    summary,
    config.model,
  );
}

function renderCostHTML(
  issueId: string,
  issueTitle: string,
  issueStatus: string,
  issueTotalUsd: number,
  session: ReturnType<CostTracker["getSessionSummary"]>,
  model: string,
): string {
  const byIssueEntries = Object.entries(session.byIssue).sort(
    (a, b) => b[1] - a[1],
  );
  const byIssueRows = byIssueEntries
    .map(([k, v]) => {
      const pct = session.totalCostUSD > 0
        ? ((v / session.totalCostUSD) * 100).toFixed(0)
        : "0";
      return `<tr><td>${escapeHTML(k)}</td><td>$${v.toFixed(4)}</td><td class="muted">${pct}%</td></tr>`;
    })
    .join("");
  const byFeatureRows = Object.entries(session.byFeature)
    .sort((a, b) => b[1].costUSD - a[1].costUSD)
    .map(([k, v]: [string, FeatureUsage]) =>
      `<tr><td>${escapeHTML(featureLabel(k))}</td><td>$${v.costUSD.toFixed(4)}</td><td class="muted">${v.calls} call${v.calls === 1 ? "" : "s"} · ${v.tokens.toLocaleString()} tokens</td></tr>`,
    )
    .join("");
  const sessionContribution =
    session.byIssue[issueId] ?? session.byIssue["(no issue)"] ?? 0;
  return `<!doctype html><html><head><meta charset="utf-8">
<style>
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#ddd;background:#1e1e1e;padding:16px;line-height:1.45}
h1{font-size:14px;color:#fff;border-bottom:1px solid #333;padding-bottom:8px;margin-top:0}
h2{font-size:12px;color:#aaa;text-transform:uppercase;letter-spacing:0.05em;margin-top:24px}
.kv{display:grid;grid-template-columns:200px 1fr;gap:6px}
.kv dt{color:#888}
.kv dd{margin:0;color:#ddd}
.cost{font-size:24px;color:#f0a030}
.delta{font-size:12px;color:#5cd187;margin-left:8px}
table{width:100%;border-collapse:collapse;margin-top:8px;font-size:12px}
td{padding:4px 8px;border-bottom:1px solid #2a2a2a}
.muted{color:#666;font-size:11px}
.status-chip{display:inline-block;background:#2a2a2a;color:#aaa;padding:1px 6px;border-radius:3px;font-size:10px;text-transform:uppercase;letter-spacing:0.05em;margin-left:8px}
</style></head><body>
<h1>Talyvor Code — Cost Dashboard</h1>

<h2>🎯 Current issue</h2>
<dl class="kv">
  <dt>Issue</dt><dd>${escapeHTML(issueId)}${issueTitle ? " — " + escapeHTML(issueTitle) : ""}${issueStatus ? `<span class="status-chip">${escapeHTML(issueStatus)}</span>` : ""}</dd>
  <dt>Track total AI cost</dt><dd class="cost">$${issueTotalUsd.toFixed(2)}<span class="delta">+$${sessionContribution.toFixed(4)} this session</span></dd>
</dl>

<h2>Session summary</h2>
<dl class="kv">
  <dt>Total cost</dt><dd>$${session.totalCostUSD.toFixed(4)}</dd>
  <dt>Total tokens</dt><dd>${session.totalTokens.toLocaleString()}</dd>
  <dt>AI calls</dt><dd>${session.completionCount}</dd>
  <dt>Duration</dt><dd>${formatDuration(session.sessionStart)}</dd>
  <dt>Model</dt><dd>${escapeHTML(model)}</dd>
</dl>

${
  Object.keys(session.byFeature).length > 0
    ? `<h2>By feature</h2><table>${byFeatureRows}</table>`
    : ""
}

${
  byIssueEntries.length > 0
    ? `<h2>By issue (cost attribution)</h2><table>${byIssueRows}</table>`
    : ""
}

<p class="muted">Active-issue total comes from Lens analytics; session figures are estimated locally. Cost syncs to Track every 5 minutes.</p>
</body></html>`;
}

// featureLabel turns the internal feature tag into a friendlier
// dashboard label.
function featureLabel(tag: string): string {
  switch (tag) {
    case "completion":
      return "Completions";
    case "chat":
      return "Chat";
    case "test-generation":
      return "Test generation";
    case "agent-plan":
      return "Agent planning";
    case "agent-execute":
      return "Agent execution";
    default:
      return tag;
  }
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
  provider: IssueContextProvider,
  instruction: string,
  rulesLoader?: RulesLoader,
  contextLoader?: ContextLoader,
  scopeManager?: ScopeManager,
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
    provider,
    rulesLoader,
    contextLoader,
    scopeManager,
  );
}

// runStartAgentCommand opens the agentic panel. When `fromIssue`
// is true we fetch the active issue from Track and seed the
// description with "issue title — description"; otherwise we
// prefill with any current selection (rare but handy for
// "refactor this hairy block").
async function runStartAgentCommand(
  extensionUri: vscode.Uri,
  lens: LensClient,
  tracker: CostTracker,
  provider: IssueContextProvider,
  fromIssue: boolean,
): Promise<void> {
  const cfg = TalyvorConfig.getLensConfig();
  if (!cfg.url || !cfg.apiKey) {
    void vscode.window.showErrorMessage(
      "Talyvor is not configured. Set talyvor.lensUrl and talyvor.lensApiKey.",
    );
    return;
  }
  let prefill = "";
  if (fromIssue) {
    if (!cfg.activeIssue) {
      void vscode.window.showWarningMessage(
        "Set an active issue first (Talyvor: Set Active Issue).",
      );
      return;
    }
    const issue =
      provider.getCurrentIssue() ??
      (await provider.setActiveIssue(cfg.activeIssue, cfg.workspaceId)) ??
      undefined;
    if (issue) {
      prefill = `${issue.title}\n\n${issue.description}`.trim();
    } else {
      prefill = `Implement ${cfg.activeIssue}`;
    }
  } else {
    const editor = vscode.window.activeTextEditor;
    if (editor && !editor.selection.isEmpty) {
      prefill = editor.document.getText(editor.selection);
    }
  }
  AgentPanel.createOrShow(extensionUri, lens, tracker, cfg, prefill, provider);
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
  provider: IssueContextProvider,
  rulesLoader?: RulesLoader,
  contextLoader?: ContextLoader,
  scopeManager?: ScopeManager,
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
    provider,
    rulesLoader,
    contextLoader,
    scopeManager,
  );
}
