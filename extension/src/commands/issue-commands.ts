// Issue-related commands — the user-facing entry points for
// switching issues, viewing the active issue, and creating a new
// issue from selected code. Lives in its own module so the
// extension.ts host stays readable.

import * as vscode from "vscode";
import type { IssueContextProvider } from "../track/issue-context";
import type { LensConfig } from "../lens/types";
import type { TrackClient, TrackIssue } from "../track/client";
import { isValidIssueIdentifier } from "../track/issue-context-pure";

// QUICK_PICK_DEBOUNCE_MS is the keystroke debounce when the user
// types in the issue picker. Hits the Track search endpoint at
// most once per 250ms so a rapid typist doesn't flood it.
const QUICK_PICK_DEBOUNCE_MS = 250;

interface IssueQuickPickItem extends vscode.QuickPickItem {
  issue?: TrackIssue;
}

// setActiveIssueCommand opens a QuickPick that searches Track
// live as the user types. Selecting an issue persists it via the
// IssueContextProvider.
export async function setActiveIssueCommand(
  provider: IssueContextProvider,
  track: TrackClient,
  config: LensConfig,
): Promise<void> {
  if (!config.workspaceId) {
    void vscode.window.showWarningMessage(
      "Set talyvor.workspaceId in settings before choosing an issue.",
    );
    return;
  }
  const picker = vscode.window.createQuickPick<IssueQuickPickItem>();
  picker.title = "Set active Talyvor issue";
  picker.placeholder = "Type to search Track (e.g. ENG or auth)…";
  picker.matchOnDescription = true;
  picker.matchOnDetail = true;
  picker.busy = true;

  // Seed with recent issues so the first open isn't empty.
  const seed = await track.listIssues(config.workspaceId, 25);
  picker.items = seed.map(toItem);
  picker.busy = false;

  let timer: ReturnType<typeof setTimeout> | undefined;
  let inflight = 0;
  picker.onDidChangeValue((value) => {
    if (timer) clearTimeout(timer);
    const trimmed = value.trim();
    if (trimmed === "") {
      picker.items = seed.map(toItem);
      return;
    }
    timer = setTimeout(async () => {
      const my = ++inflight;
      picker.busy = true;
      const results = isValidIssueIdentifier(trimmed.toUpperCase())
        ? await fetchSingle(track, config.workspaceId, trimmed.toUpperCase())
        : await track.searchIssues(config.workspaceId, trimmed);
      if (my !== inflight) return; // stale
      picker.items = results.map(toItem);
      picker.busy = false;
    }, QUICK_PICK_DEBOUNCE_MS);
  });

  picker.onDidAccept(async () => {
    const sel = picker.selectedItems[0];
    picker.hide();
    if (!sel) return;
    const id = sel.issue?.identifier ?? picker.value.trim();
    if (id === "") return;
    const issue = await provider.setActiveIssue(id, config.workspaceId);
    if (issue) {
      void vscode.window.showInformationMessage(
        `Active issue: ${issue.identifier} — ${issue.title}`,
      );
    } else {
      void vscode.window.showWarningMessage(
        `Active issue set to ${id} (Track lookup failed — costs will still attribute).`,
      );
    }
  });

  picker.onDidHide(() => picker.dispose());
  picker.show();
}

async function fetchSingle(
  track: TrackClient,
  workspaceId: string,
  identifier: string,
): Promise<TrackIssue[]> {
  const issue = await track.getIssue(workspaceId, identifier);
  return issue ? [issue] : [];
}

function toItem(issue: TrackIssue): IssueQuickPickItem {
  return {
    label: `$(circle-filled) ${issue.identifier} — ${issue.title}`,
    description: issue.status,
    detail: `Cost: $${issue.aiCostUsd.toFixed(2)}`,
    issue,
  };
}

// showIssueCommand opens a read-only webview with the current
// issue's metadata. Useful for "what was I working on?" without
// having to leave the editor.
export async function showIssueCommand(
  provider: IssueContextProvider,
  config: LensConfig,
): Promise<void> {
  let issue = provider.getCurrentIssue();
  if (!issue && config.activeIssue) {
    // Cold-start case: provider hasn't fetched yet but the user
    // has an identifier in settings. Resolve it now.
    issue = (await provider.setActiveIssue(config.activeIssue, config.workspaceId)) ?? undefined;
  }
  if (!issue) {
    void vscode.window.showInformationMessage(
      "No active issue. Set one with Talyvor: Set Active Issue.",
    );
    return;
  }
  const panel = vscode.window.createWebviewPanel(
    "talyvorIssue",
    `Issue: ${issue.identifier}`,
    vscode.ViewColumn.Beside,
    { enableScripts: false, retainContextWhenHidden: true },
  );
  panel.webview.html = renderIssueHTML(issue);
}

function renderIssueHTML(issue: TrackIssue): string {
  const esc = (s: string) =>
    s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  return `<!doctype html><html><head><meta charset="utf-8">
<style>
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#ddd;background:#1e1e1e;padding:16px;line-height:1.5}
h1{font-size:18px;color:#fff;margin:0 0 8px}
.id{color:#f0a030;font-family:monospace;font-size:13px}
.status{display:inline-block;background:#2a2a2a;color:#aaa;padding:2px 8px;border-radius:4px;font-size:11px;text-transform:uppercase;letter-spacing:0.05em}
.cost{font-size:22px;color:#f0a030;margin-top:8px}
.desc{margin-top:16px;white-space:pre-wrap;color:#ccc;font-size:13px}
</style></head><body>
<div class="id">${esc(issue.identifier)}</div>
<h1>${esc(issue.title)}</h1>
<span class="status">${esc(issue.status || "—")}</span>
<div class="cost">$${issue.aiCostUsd.toFixed(2)}<span style="font-size:11px;color:#888;margin-left:6px">total AI cost</span></div>
<div class="desc">${esc(issue.description || "(no description)")}</div>
</body></html>`;
}

// createIssueFromCodeCommand grabs the editor selection (or
// surrounding context if nothing's selected) and seeds Track's
// issue-creation form with an AI-suggested title plus the code
// snippet. The Talyvor Code surface doesn't itself create issues
// today — we hand off to Track via the URL.
export async function createIssueFromCodeCommand(
  config: LensConfig,
): Promise<void> {
  const editor = vscode.window.activeTextEditor;
  if (!editor) {
    void vscode.window.showWarningMessage(
      "Open a file with the relevant code first.",
    );
    return;
  }
  const code = editor.selection.isEmpty
    ? editor.document.lineAt(editor.selection.active.line).text
    : editor.document.getText(editor.selection);
  if (!code.trim()) {
    void vscode.window.showWarningMessage("Nothing to create an issue from.");
    return;
  }
  if (!config.trackUrl) {
    void vscode.window.showWarningMessage(
      "Set talyvor.trackUrl in settings to open Track.",
    );
    return;
  }
  const lang = editor.document.languageId;
  const fence = "```" + lang;
  const description = `From ${editor.document.uri.fsPath}\n\n${fence}\n${code}\n\`\`\``;
  const title = code.split("\n")[0].trim().slice(0, 80) || "New issue from code";
  const url = `${config.trackUrl.replace(/\/$/, "")}/new?title=${encodeURIComponent(title)}&description=${encodeURIComponent(description)}`;
  await vscode.env.openExternal(vscode.Uri.parse(url));
}
