// Configuration adapter around vscode.workspace.getConfiguration.
// The Lens/Track clients only see plain objects so they stay
// testable without the VS Code API.

import * as vscode from "vscode";
import { LensConfig } from "./lens/types";

const SECTION = "talyvor";

// safeBaseUrl returns raw only if it is a safe Talyvor endpoint, else "". The config is WORKSPACE-scoped,
// so a hostile repo's .vscode/settings.json could point a URL at an attacker host (with the user's API
// key attached) or at the cloud metadata endpoint. Require https (except explicit localhost dev), and
// reject link-local / metadata / unspecified hosts. An unsafe URL sanitizes to "" so the client is never
// configured with it — the key is never sent there.
export function safeBaseUrl(raw: string): string {
  if (!raw) return "";
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return "";
  }
  const host = u.hostname;
  const isLocal = host === "localhost" || host === "127.0.0.1" || host === "::1";
  if (u.protocol !== "https:" && !(u.protocol === "http:" && isLocal)) return "";
  if (host === "0.0.0.0" || host.startsWith("169.254.") || host.startsWith("[fe80")) return "";
  return raw;
}

export class TalyvorConfig {
  static getLensConfig(): LensConfig {
    const cfg = vscode.workspace.getConfiguration(SECTION);
    return {
      url: safeBaseUrl(cfg.get<string>("lensUrl", "")),
      apiKey: cfg.get<string>("lensApiKey", ""),
      workspaceId: cfg.get<string>("workspaceId", ""),
      activeIssue: cfg.get<string>("activeIssue", ""),
      model: cfg.get<string>("model", "claude-haiku-4-6"),
      trackUrl: safeBaseUrl(cfg.get<string>("trackUrl", "")),
      trackApiKey: cfg.get<string>("trackApiKey", ""),
      docsUrl: safeBaseUrl(cfg.get<string>("docsUrl", "")),
      docsApiKey: cfg.get<string>("docsApiKey", ""),
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
    const cfg = vscode.workspace.getConfiguration(SECTION);
    const c = this.getLensConfig();
    const out: string[] = [];
    if (!c.url) {
      const raw = cfg.get<string>("lensUrl", "");
      out.push(
        raw
          ? "Lens URL is unsafe — must be https (or localhost) and not an internal/metadata address (talyvor.lensUrl)"
          : "Lens URL is required (talyvor.lensUrl)",
      );
    }
    if (!c.apiKey) out.push("Lens API key is required (talyvor.lensApiKey)");
    if (!c.workspaceId) out.push("Workspace ID is required (talyvor.workspaceId)");
    return out;
  }
}
