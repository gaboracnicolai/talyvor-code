// selectModelCommand opens a QuickPick over the known models and
// persists the choice to `talyvor.model`. The status bar reads
// the setting on its next refresh and surfaces the new model in
// its tooltip; the completion provider + ChatPanel + AgentMode
// all read the same setting on each call so the change takes
// effect immediately.

import * as vscode from "vscode";
import { KNOWN_MODELS, getModel } from "../model/models-pure";

interface ModelQuickPickItem extends vscode.QuickPickItem {
  modelId: string;
}

export async function selectModelCommand(): Promise<void> {
  const cfg = vscode.workspace.getConfiguration("talyvor");
  const current = cfg.get<string>("model", "");
  const items: ModelQuickPickItem[] = KNOWN_MODELS.map((m) => ({
    label: `$(${m.icon}) ${m.displayName}`,
    description: `${m.provider} · ${m.speedTier} · ${m.costTier}`,
    detail: `Best for: ${m.bestFor.join(", ")}`,
    modelId: m.id,
    picked: m.id === current,
  }));
  // Place the current model at the top so the user can re-confirm
  // (or escape) without scrolling.
  items.sort((a, b) => {
    if (a.modelId === current) return -1;
    if (b.modelId === current) return 1;
    return 0;
  });
  const picked = await vscode.window.showQuickPick(items, {
    title: "Select AI model",
    placeHolder: "Choose the model Talyvor Code should use",
    matchOnDescription: true,
    matchOnDetail: true,
  });
  if (!picked) return;
  const profile = getModel(picked.modelId);
  await cfg.update(
    "model",
    picked.modelId,
    vscode.ConfigurationTarget.Workspace,
  );
  void vscode.window.showInformationMessage(
    `Talyvor model: ${profile?.displayName ?? picked.modelId}`,
  );
}
