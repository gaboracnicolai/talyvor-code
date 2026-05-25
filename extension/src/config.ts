// Configuration adapter around vscode.workspace.getConfiguration.
// The Lens/Track clients only see plain objects so they stay
// testable without the VS Code API.

import * as vscode from "vscode";
import { LensConfig } from "./lens/types";

const SECTION = "talyvor";

export class TalyvorConfig {
  static getLensConfig(): LensConfig {
    const cfg = vscode.workspace.getConfiguration(SECTION);
    return {
      url: cfg.get<string>("lensUrl", ""),
      apiKey: cfg.get<string>("lensApiKey", ""),
      workspaceId: cfg.get<string>("workspaceId", ""),
      activeIssue: cfg.get<string>("activeIssue", ""),
      model: cfg.get<string>("model", "claude-haiku-4-6"),
      trackUrl: cfg.get<string>("trackUrl", ""),
      trackApiKey: cfg.get<string>("trackApiKey", ""),
      enableCompletions: cfg.get<boolean>("enableCompletions", true),
    };
  }

  // setActiveIssue persists to the workspace-scoped config so the
  // selection follows the project. Global scope would leak the
  // active issue across unrelated repos.
  static async setActiveIssue(issue: string): Promise<void> {
    const cfg = vscode.workspace.getConfiguration(SECTION);
    await cfg.update(
      "activeIssue",
      issue,
      vscode.ConfigurationTarget.Workspace,
    );
  }

  static isConfigured(): boolean {
    const c = this.getLensConfig();
    return !!c.url && !!c.apiKey;
  }

  // validate returns a list of human-readable issues so the
  // welcome message + test-connection command can show the user
  // exactly what's missing.
  static validate(): string[] {
    const c = this.getLensConfig();
    const out: string[] = [];
    if (!c.url) out.push("Lens URL is required (talyvor.lensUrl)");
    if (!c.apiKey) out.push("Lens API key is required (talyvor.lensApiKey)");
    if (!c.workspaceId) out.push("Workspace ID is required (talyvor.workspaceId)");
    return out;
  }
}
