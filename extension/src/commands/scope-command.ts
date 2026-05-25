// setScopeCommand opens a QuickPick over the loaded
// .talyvor-scopes catalogue. Picking a scope persists it to
// .talyvor-active-scope (which the manager's watcher then picks
// up); picking "Clear scope" removes the file.

import * as vscode from "vscode";
import type { ScopeManager } from "../scope/scope-manager";

interface ScopeQuickPickItem extends vscode.QuickPickItem {
  scopeKey?: string;
  clear?: boolean;
}

export async function setScopeCommand(manager: ScopeManager): Promise<void> {
  const keys = manager.listKeys();
  if (keys.length === 0) {
    void vscode.window.showInformationMessage(
      "No scopes defined. Create a .talyvor-scopes file or run `talyvor-code scope add`.",
    );
    return;
  }
  const active = manager.activeName();
  const items: ScopeQuickPickItem[] = keys.map((key) => {
    const s = manager.get(key);
    const display = s?.name?.trim() || key;
    return {
      label: `$(filter) ${display}`,
      description: key === active ? "(active)" : key,
      detail: s?.focus?.trim() || s?.includes.join(", "),
      scopeKey: key,
    };
  });
  if (active) {
    items.push({
      label: "$(filter-filled) Clear scope",
      description: "All files in context",
      clear: true,
    });
  }
  const picked = await vscode.window.showQuickPick(items, {
    title: "Set context scope",
    placeHolder: "Choose a scope to focus AI context on",
    matchOnDescription: true,
    matchOnDetail: true,
  });
  if (!picked) return;
  if (picked.clear) {
    await manager.clearActive();
    void vscode.window.showInformationMessage("Scope cleared. All files in context.");
    return;
  }
  if (!picked.scopeKey) return;
  try {
    await manager.setActive(picked.scopeKey);
    const s = manager.get(picked.scopeKey);
    const display = s?.name?.trim() || picked.scopeKey;
    void vscode.window.showInformationMessage(
      `Scope: ${display}${s?.focus ? " — " + s.focus : ""}`,
    );
  } catch (err) {
    void vscode.window.showErrorMessage(
      "Set scope failed: " + (err instanceof Error ? err.message : String(err)),
    );
  }
}
