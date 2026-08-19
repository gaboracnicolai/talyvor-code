// TalyvorStatusBar — single source of truth for the status bar.
// Renders three states (not configured / no issue / with issue),
// drives the 5-minute cost-sync timer, and shows a spinner while
// the sync is in flight.

import * as vscode from "vscode";
import { costDisclaimerLines, formatSessionCost } from "./cost-label-pure";
import type { LensConfig } from "../lens/types";
import type { ScopeManager } from "../scope/scope-manager";
import { getModel } from "../model/models-pure";

export class TalyvorStatusBar implements vscode.Disposable {
  private readonly item: vscode.StatusBarItem;
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
    // ⚠ LABELLED AS AN ESTIMATE, because the bar renders this figure directly beside an ISSUE
    // IDENTIFIER and Track shows a number for that same issue. A reader has every reason to take
    // them for the same quantity, and they are not: this is a LOCAL estimate of THIS SESSION at one
    // hardcoded price — Claude 3 Haiku's, so it is 4x low on claude-haiku-4-5, the default model —
    // and it counts only what this editor did. Track's figure is the authoritative per-request cost
    // for the whole issue, from Lens. The "~" and the tooltip are what keep an understated number
    // from reading as the bill. Every factor is in providers/cost-tracker.ts, stated once.
    const cost = formatSessionCost(this.lastSessionCost);
    const scopeName = this.scopeManager?.activeName() ?? "";
    const scopeChip = scopeName ? ` | $(filter) ${scopeName}` : "";
    if (!cfg.activeIssue) {
      this.item.text = `$(sparkle) Talyvor${scopeChip} | ${cost}`;
      this.item.tooltip = [
        `Session cost: ${cost} (${this.lastTokens.toLocaleString()} tokens)`,
        ...costDisclaimerLines(),
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
    // ⚠ The two numbers a user can see for this issue are different quantities. Say so here, where
    // they are looking, rather than trusting them to know.
    parts.push(...costDisclaimerLines(cfg.activeIssue));
    parts.push(`Model: ${modelLabel(cfg.model)}`);
    parts.push(scopeName ? `Scope: ${this.scopeDescription(scopeName)}` : "Scope: (all files)");
    parts.push("Click to change issue · `Talyvor: Set Context Scope` to change scope · `Talyvor: Select AI Model` to change model");
    return parts.join("\n");
  }

  // ⚠ startCostSync / stopCostSync / runSync WERE HERE AND ARE DELETED.
  //
  // The five-minute timer existed only to drive the client-side cost PATCH, which is gone: Lens
  // records per-issue spend server-side from the real cost (migration 0116 + issue_id on
  // /v1/api/spend/by-request; Track a962b0c prefers it over the feature). Keeping a timer that
  // calls nothing would be worse than deleting it — it reads as working sync to the next person.
  //
  // The "Syncing…" state went with it: there is nothing to sync from here any more.

  dispose(): void {
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
