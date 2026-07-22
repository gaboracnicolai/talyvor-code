// Configuration adapter around vscode.workspace.getConfiguration.
// The Lens/Track clients only see plain objects so they stay
// testable without the VS Code API.

import * as vscode from "vscode";
import { LensConfig } from "./lens/types";
import {
  SECRET_KEYS,
  loadSecrets,
  migrateSecrets,
  type PlaintextStore,
  type SecretKey,
  type SecretStore,
} from "./secrets-pure";

const SECTION = "talyvor";

// Credentials live in SecretStorage (the OS keychain), never in plaintext settings. getLensConfig is
// synchronous and called from ~25 sites, so the migrated secrets are cached here at activation and
// served synchronously; initSecrets (async) refreshes the cache. Empty until initSecrets runs — the
// reader falls back to any legacy plaintext so a not-yet-migrated install still works.
let secretCache: Record<SecretKey, string> = {
  lensApiKey: "",
  trackApiKey: "",
  docsApiKey: "",
  githubToken: "",
};

// vscodeSecretStore / vscodePlaintextStore bind the pure interfaces to the real VS Code APIs.
function vscodeSecretStore(secrets: vscode.SecretStorage): SecretStore {
  return {
    get: (k) => Promise.resolve(secrets.get(k)),
    store: (k, v) => Promise.resolve(secrets.store(k, v)),
    delete: (k) => Promise.resolve(secrets.delete(k)),
  };
}

function vscodePlaintextStore(): PlaintextStore {
  return {
    read: (k) => vscode.workspace.getConfiguration(SECTION).get<string>(k, ""),
    // Remove the plaintext from EVERY scope it might be persisted in (Global + Workspace +
    // WorkspaceFolder), so a synced/committed copy can't linger. undefined deletes the key.
    clear: async (k) => {
      const cfg = vscode.workspace.getConfiguration(SECTION);
      for (const target of [
        vscode.ConfigurationTarget.Global,
        vscode.ConfigurationTarget.Workspace,
        vscode.ConfigurationTarget.WorkspaceFolder,
      ]) {
        try {
          await cfg.update(k, undefined, target);
        } catch {
          // updating a scope that doesn't apply (e.g. WorkspaceFolder with no folder) throws — ignore.
        }
      }
    },
  };
}

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
  // secret reads the cached SecretStorage value; it falls back to a legacy plaintext setting so an
  // install whose migration hasn't run yet still authenticates (that plaintext is cleared the moment
  // initSecrets runs). The cache is authoritative post-migration.
  private static secret(cfg: vscode.WorkspaceConfiguration, key: SecretKey): string {
    return secretCache[key] || cfg.get<string>(key, "");
  }

  static getLensConfig(): LensConfig {
    const cfg = vscode.workspace.getConfiguration(SECTION);
    return {
      url: safeBaseUrl(cfg.get<string>("lensUrl", "")),
      apiKey: this.secret(cfg, "lensApiKey"),
      workspaceId: cfg.get<string>("workspaceId", ""),
      activeIssue: cfg.get<string>("activeIssue", ""),
      model: cfg.get<string>("model", "claude-haiku-4-5"),
      trackUrl: safeBaseUrl(cfg.get<string>("trackUrl", "")),
      trackApiKey: this.secret(cfg, "trackApiKey"),
      docsUrl: safeBaseUrl(cfg.get<string>("docsUrl", "")),
      docsApiKey: this.secret(cfg, "docsApiKey"),
      enableCompletions: cfg.get<boolean>("enableCompletions", true),
    };
  }

  // githubToken returns the GitHub PAT from SecretStorage (fallback: legacy plaintext, then
  // $GITHUB_TOKEN). Used by the PR creator, which previously read the plaintext setting directly.
  static githubToken(): string {
    const cfg = vscode.workspace.getConfiguration(SECTION);
    const fromSecret = this.secret(cfg, "githubToken").trim();
    if (fromSecret !== "") return fromSecret;
    return (process.env.GITHUB_TOKEN ?? "").trim();
  }

  // initSecrets runs the one-time plaintext→SecretStorage migration, then loads every credential into
  // the synchronous cache. Idempotent and loss-free (see secrets-pure). Returns the count migrated so
  // the caller can surface a one-time notice. Call it BEFORE building any client.
  static async initSecrets(secrets: vscode.SecretStorage): Promise<number> {
    const migrated = await migrateSecrets(vscodePlaintextStore(), vscodeSecretStore(secrets));
    secretCache = await loadSecrets(vscodeSecretStore(secrets));
    return migrated.length;
  }

  // secretKeys exposes the credential setting keys so callers can detect a plaintext re-entry.
  static get secretKeys(): readonly string[] {
    return SECRET_KEYS;
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
