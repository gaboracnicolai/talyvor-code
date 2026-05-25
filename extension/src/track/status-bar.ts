// TalyvorStatusBar — single source of truth for the status bar.
// Renders three states (not configured / no issue / with issue),
// drives the 5-minute cost-sync timer, and shows a spinner while
// the sync is in flight.

import * as vscode from "vscode";
import type { LensConfig } from "../lens/types";
import type { IssueContextProvider } from "./issue-context";
import type { ScopeManager } from "../scope/scope-manager";
import { getModel } from "../model/models-pure";

export class TalyvorStatusBar implements vscode.Disposable {
  private readonly item: vscode.StatusBarItem;
  private costSyncTimer: ReturnType<typeof setInterval> | undefined;
  private syncing = false;
  private lastConfig: LensConfig | undefined;
  private lastSessionCost = 0;
  private lastTokens = 0;
  private scopeManager: ScopeManager | undefined;

  constructor(context: vscode.ExtensionContext) {
    this.item = vscode.window.createStatusBarItem(
      vscode.StatusBarAlignment.Right,
      100,
    );
    this.item.show();
    context.subscriptions.push(this.item);
  }

  // update is called whenever the config or session totals
  // change. Keeps the last values cached so the sync spinner can
  // re-render without callers passing them in again.
  update(config: LensConfig, sessionCostUsd: number, tokens = 0): void {
    this.lastConfig = config;
    this.lastSessionCost = sessionCostUsd;
    this.lastTokens = tokens;
    this.render();
  }

  // bindScopeManager wires a ScopeManager whose active state
  // gets rendered between issue and cost in the bar. Calls
  // render() once and again on every scope change.
  bindScopeManager(manager: ScopeManager): vscode.Disposable {
    this.scopeManager = manager;
    this.render();
    return manager.onChange(() => this.render());
  }

  private render(): void {
    const cfg = this.lastConfig;
    if (!cfg || !cfg.url || !cfg.apiKey) {
      this.item.text = "$(warning) Talyvor: Setup required";
      this.item.tooltip = "Click to open Talyvor settings";
      this.item.command = "workbench.action.openSettings";
      return;
    }
    if (this.syncing) {
      this.item.text = "$(sync~spin) Syncing…";
      this.item.tooltip = "Syncing AI cost to Track…";
      this.item.command = undefined;
      return;
    }
    const cost = `$${this.lastSessionCost.toFixed(2)}`;
    const scopeName = this.scopeManager?.activeName() ?? "";
    const scopeChip = scopeName ? ` | $(filter) ${scopeName}` : "";
    if (!cfg.activeIssue) {
      this.item.text = `$(sparkle) Talyvor${scopeChip} | ${cost}`;
      this.item.tooltip = [
        `Session cost: ${cost} (${this.lastTokens.toLocaleString()} tokens)`,
        `Model: ${modelLabel(cfg.model)}`,
        scopeName ? `Scope: ${this.scopeDescription(scopeName)}` : "Scope: (all files)",
        "Click to set an active issue.",
      ].join("\n");
      this.item.command = "talyvor.setActiveIssue";
      return;
    }
    this.item.text = `$(sparkle) ${cfg.activeIssue}${scopeChip} | ${cost}`;
    this.item.tooltip = this.buildIssueTooltip(cfg, cost, scopeName);
    this.item.command = "talyvor.setActiveIssue";
  }

  private scopeDescription(key: string): string {
    const s = this.scopeManager?.get(key);
    if (!s) return key;
    const display = s.name.trim() || key;
    return s.focus.trim() ? `${display} — ${s.focus.trim()}` : display;
  }

  private buildIssueTooltip(cfg: LensConfig, costStr: string, scopeName: string): string {
    // The provider is what holds the current issue object; we
    // accept the small coupling so the tooltip can show a title.
    const parts = [`Active issue: ${cfg.activeIssue}`];
    parts.push(`Session cost: ${costStr} (${this.lastTokens.toLocaleString()} tokens)`);
    parts.push(`Model: ${modelLabel(cfg.model)}`);
    parts.push(scopeName ? `Scope: ${this.scopeDescription(scopeName)}` : "Scope: (all files)");
    parts.push("Click to change issue · `Talyvor: Set Context Scope` to change scope · `Talyvor: Select AI Model` to change model");
    return parts.join("\n");
  }

  // startCostSync wires the 5-minute timer that pushes session
  // cost to Track. Best-effort throughout — sync failures show in
  // the tooltip but never raise to the user.
  startCostSync(
    issueProvider: IssueContextProvider,
    workspaceId: string,
    intervalMs = 5 * 60 * 1000,
  ): void {
    this.stopCostSync();
    if (!workspaceId) return;
    this.costSyncTimer = setInterval(() => {
      void this.runSync(issueProvider, workspaceId);
    }, intervalMs);
  }

  stopCostSync(): void {
    if (this.costSyncTimer) {
      clearInterval(this.costSyncTimer);
      this.costSyncTimer = undefined;
    }
  }

  private async runSync(
    provider: IssueContextProvider,
    workspaceId: string,
  ): Promise<void> {
    this.syncing = true;
    this.render();
    try {
      await provider.syncCostToTrack(workspaceId);
    } catch {
      // best-effort — never fail the editor
    }
    this.syncing = false;
    this.render();
  }

  dispose(): void {
    this.stopCostSync();
    this.item.dispose();
  }
}

// modelLabel returns the human-friendly name for a model ID, or
// the ID itself when we don't recognise it (custom proxy models
// configured by an admin land in the fallback case).
function modelLabel(id: string): string {
  const profile = getModel(id);
  return profile ? profile.displayName : id || "(default)";
}
