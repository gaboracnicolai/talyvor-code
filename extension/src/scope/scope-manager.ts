// vscode-bound scope manager: loads .talyvor-scopes from the
// workspace, watches for changes, persists the active scope to
// .talyvor-active-scope, and exposes synchronous getters for the
// hot paths (completions, ChatPanel, status bar).

import * as vscode from "vscode";
import {
  ACTIVE_SCOPE_FILE_NAME,
  SCOPES_FILE_NAME,
  parseScopes,
  type Scope,
  type ScopeCatalogue,
} from "./scope-pure";

export class ScopeManager implements vscode.Disposable {
  private catalogue: ScopeCatalogue = {};
  private activeKey = "";
  private active: Scope | undefined;
  private workspaceRoot = "";
  private watchers: vscode.Disposable[] = [];
  private listeners: Array<() => void> = [];

  // initialize seeds the catalogue + active scope from the first
  // workspace folder and installs FileSystemWatchers on both
  // marker files so external edits (CLI / `git pull`) propagate
  // into the running editor.
  async initialize(): Promise<void> {
    this.workspaceRoot = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath ?? "";
    await this.refresh();
    if (this.watchers.length === 0) {
      for (const name of [SCOPES_FILE_NAME, ACTIVE_SCOPE_FILE_NAME]) {
        const w = vscode.workspace.createFileSystemWatcher("**/" + name);
        const onAny = () => void this.refresh();
        w.onDidChange(onAny);
        w.onDidCreate(onAny);
        w.onDidDelete(onAny);
        this.watchers.push(w);
      }
    }
  }

  listKeys(): string[] {
    return Object.keys(this.catalogue).sort();
  }

  get(key: string): Scope | undefined {
    return this.catalogue[key];
  }

  getActive(): Scope | undefined {
    return this.active;
  }

  activeName(): string {
    return this.activeKey;
  }

  // setActive writes the active scope to disk and updates the
  // in-memory pointer. Caller is expected to handle the "unknown
  // key" case (validated against the catalogue).
  async setActive(key: string): Promise<void> {
    if (!this.workspaceRoot) return;
    if (!this.catalogue[key]) {
      throw new Error(`unknown scope ${JSON.stringify(key)}`);
    }
    this.activeKey = key;
    this.active = this.catalogue[key];
    const uri = vscode.Uri.file(joinPath(this.workspaceRoot, ACTIVE_SCOPE_FILE_NAME));
    await vscode.workspace.fs.writeFile(uri, new TextEncoder().encode(key + "\n"));
    this.notify();
  }

  async clearActive(): Promise<void> {
    this.activeKey = "";
    this.active = undefined;
    if (!this.workspaceRoot) return;
    const uri = vscode.Uri.file(joinPath(this.workspaceRoot, ACTIVE_SCOPE_FILE_NAME));
    try {
      await vscode.workspace.fs.delete(uri);
    } catch {
      // Missing file is fine — clear is idempotent.
    }
    this.notify();
  }

  // onChange lets the status bar + prompt providers react when
  // the user switches scopes (or another window edits the
  // catalogue). Returns a Disposable for symmetric teardown.
  onChange(fn: () => void): vscode.Disposable {
    this.listeners.push(fn);
    return {
      dispose: () => {
        const i = this.listeners.indexOf(fn);
        if (i >= 0) this.listeners.splice(i, 1);
      },
    };
  }

  dispose(): void {
    for (const w of this.watchers) w.dispose();
    this.watchers = [];
  }

  private notify(): void {
    for (const l of this.listeners) {
      try {
        l();
      } catch {
        // best-effort
      }
    }
  }

  private async refresh(): Promise<void> {
    if (!this.workspaceRoot) {
      this.catalogue = {};
      this.active = undefined;
      this.activeKey = "";
      return;
    }
    const scopesUri = vscode.Uri.file(joinPath(this.workspaceRoot, SCOPES_FILE_NAME));
    try {
      const bytes = await vscode.workspace.fs.readFile(scopesUri);
      const parsed = parseScopes(new TextDecoder().decode(bytes));
      this.catalogue = parsed ?? {};
    } catch {
      this.catalogue = {};
    }
    // Re-apply the active key against the (possibly updated)
    // catalogue. Unknown keys silently clear so the user isn't
    // stuck on a scope that no longer exists.
    const activeUri = vscode.Uri.file(joinPath(this.workspaceRoot, ACTIVE_SCOPE_FILE_NAME));
    try {
      const bytes = await vscode.workspace.fs.readFile(activeUri);
      const key = new TextDecoder().decode(bytes).trim();
      if (key && this.catalogue[key]) {
        this.activeKey = key;
        this.active = this.catalogue[key];
      } else {
        this.activeKey = "";
        this.active = undefined;
      }
    } catch {
      this.activeKey = "";
      this.active = undefined;
    }
    this.notify();
  }
}

function joinPath(root: string, name: string): string {
  if (!root) return name;
  const sep = root.endsWith("/") || root.endsWith("\\") ? "" : "/";
  return root + sep + name;
}
