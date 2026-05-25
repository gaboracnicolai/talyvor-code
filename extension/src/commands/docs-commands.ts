// Docs-related commands: open the panel, run an ad-hoc search,
// ask the docs about the selection, and link the active issue to
// the current docs page. Lives next to issue-commands so the
// extension.ts host stays readable.

import * as vscode from "vscode";
import type { DocsClient } from "../docs/docs-client";
import { DocsPanel } from "../docs/DocsPanel";
import type { LensConfig } from "../lens/types";
import type { IssueContextProvider } from "../track/issue-context";

// openDocsCommand reveals (or creates) the docs panel in search
// mode without any pre-filled query.
export function openDocsCommand(
  extensionUri: vscode.Uri,
  docs: DocsClient,
  config: LensConfig,
): void {
  if (!docs.isConfigured()) {
    void vscode.window.showWarningMessage(
      "Talyvor Docs is not configured. Set talyvor.docsUrl and talyvor.docsApiKey in settings.",
    );
    return;
  }
  DocsPanel.createOrShow(extensionUri, docs, config);
}

// searchDocsCommand prompts for a query (or uses the editor
// selection if non-empty) and opens DocsPanel with the results.
export async function searchDocsCommand(
  extensionUri: vscode.Uri,
  docs: DocsClient,
  config: LensConfig,
): Promise<void> {
  if (!docs.isConfigured()) {
    void vscode.window.showWarningMessage(
      "Talyvor Docs is not configured.",
    );
    return;
  }
  const editor = vscode.window.activeTextEditor;
  const selection =
    editor && !editor.selection.isEmpty
      ? editor.document.getText(editor.selection)
      : "";
  const query = await vscode.window.showInputBox({
    title: "Search Talyvor Docs",
    prompt: "Enter a query (full-text + semantic search)",
    value: selection.slice(0, 120),
    placeHolder: "e.g. authentication flow",
  });
  if (!query || query.trim() === "") return;
  DocsPanel.createOrShow(extensionUri, docs, config, query.trim());
}

// askDocsCommand grabs the selection (or current word) and posts
// it as a docs Q&A. We open the DocsPanel in ask mode so the
// answer + sources are visible alongside the rest of the docs UI.
export async function askDocsCommand(
  extensionUri: vscode.Uri,
  docs: DocsClient,
  config: LensConfig,
): Promise<void> {
  if (!docs.isConfigured()) {
    void vscode.window.showWarningMessage(
      "Talyvor Docs is not configured.",
    );
    return;
  }
  const editor = vscode.window.activeTextEditor;
  const selection =
    editor && !editor.selection.isEmpty
      ? editor.document.getText(editor.selection)
      : "";
  const defaultQ = selection
    ? `How does ${selection.split("\n")[0].trim().slice(0, 80)} work according to our docs?`
    : "";
  const question = await vscode.window.showInputBox({
    title: "Ask Talyvor Docs",
    prompt: "What would you like to know? (Docs Q&A grounded in your team's specs)",
    value: defaultQ,
    placeHolder: "How does JWT refresh work?",
  });
  if (!question || question.trim() === "") return;
  await DocsPanel.ask(extensionUri, docs, config, question.trim());
}

// linkDocToIssueCommand attaches a docs page to the active Track
// issue. We do not yet have a programmatic "create page link"
// endpoint exposed to the IDE; instead, we open the docs panel
// pointed at the issue's identifier and let the user paste a
// link from there. This is the seam where a future PageLink API
// would slot in.
export async function linkDocToIssueCommand(
  extensionUri: vscode.Uri,
  docs: DocsClient,
  config: LensConfig,
  provider: IssueContextProvider,
): Promise<void> {
  if (!docs.isConfigured()) {
    void vscode.window.showWarningMessage("Talyvor Docs is not configured.");
    return;
  }
  const issue = provider.getCurrentIssue();
  if (!issue || !issue.identifier) {
    void vscode.window.showWarningMessage(
      "Set an active issue first (Talyvor: Set Active Issue).",
    );
    return;
  }
  // Pre-fill the docs panel with a search for the issue identifier
  // and title — that's the most useful entry point for "which docs
  // describe what this issue is for?".
  const query = `${issue.identifier} ${issue.title}`.trim();
  DocsPanel.createOrShow(extensionUri, docs, config, query);
  void vscode.window.showInformationMessage(
    `Showing docs matching ${issue.identifier}. Select a page to link.`,
  );
}
