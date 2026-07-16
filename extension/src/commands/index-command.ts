// buildIndexCommand — the vscode-bound entry point for building/refreshing the
// workspace's semantic index IN-PROCESS (no shell-out to the Go binary). It is a thin
// wrapper over the headless-tested `refreshIndex`: resolve the workspace root + a
// Lens-backed embedder, run the build behind a progress notification, report the delta.
//
// MANUAL-VERIFY ONLY: this file imports vscode (commands, workspace, window/progress) and
// cannot run under the headless test harness — by the run's rules it is NOT unit-tested
// (no mock/fake/skip). Everything it calls (refreshIndex → walk/chunk/hash/incremental/
// atomic-save, lensEmbedder) IS headless-tested.

import * as vscode from "vscode";
import type { LensClient } from "../lens/client";
import type { LensConfig } from "../lens/types";
import { lensEmbedder } from "../agent/retrieval-pure";
import { refreshIndex } from "../agent/index-build-pure";

export async function buildIndexCommand(lens: LensClient, config: LensConfig): Promise<void> {
  if (!lens.isConfigured()) {
    void vscode.window.showErrorMessage(
      "Talyvor is not configured. Set talyvor.lensUrl and talyvor.lensApiKey to build the semantic index.",
    );
    return;
  }
  const folder = vscode.workspace.workspaceFolders?.[0];
  if (!folder) {
    void vscode.window.showErrorMessage("Open a workspace folder to build its semantic index.");
    return;
  }
  const root = folder.uri.fsPath;
  // Only the chunk text leaves the machine (to Lens, feature `embed`) — same trust
  // boundary as chat; the built index stays a local file under <root>/.talyvor/.
  const emb = lensEmbedder(lens, config.workspaceId, config.activeIssue);

  try {
    const res = await vscode.window.withProgress(
      {
        location: vscode.ProgressLocation.Notification,
        title: "Talyvor: building semantic index…",
        cancellable: false,
      },
      async () => refreshIndex(emb, root),
    );
    void vscode.window.showInformationMessage(
      `Semantic index ${res.fullRebuild ? "built" : "refreshed"}: ${res.chunks} chunks across ${res.files} files — ` +
        `${res.changed} re-embedded, ${res.reused} reused → .talyvor/codebase-index.json`,
    );
  } catch (err) {
    void vscode.window.showErrorMessage(
      "Semantic index build failed: " + (err instanceof Error ? err.message : String(err)),
    );
  }
}
