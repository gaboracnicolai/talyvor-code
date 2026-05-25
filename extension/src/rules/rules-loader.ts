// vscode-bound loader for `.talyvor-rules`. Lives next to the
// pure parser so the wiring (FileSystemWatcher) is testable in
// isolation from the parsing logic. The loader caches the most
// recently parsed Rules and exposes a synchronous getter for the
// providers that need it in hot paths (completions).

import * as vscode from "vscode";
import {
  MAX_RULES_FILE_BYTES,
  RULES_FILE_NAME,
  parseRules,
  type Rules,
} from "./rules-pure";

export class RulesLoader implements vscode.Disposable {
  private current: Rules | undefined;
  private watcher: vscode.FileSystemWatcher | undefined;
  private readonly listeners: Array<(r: Rules | undefined) => void> = [];

  // initialize seeds the cache with whatever's on disk today and
  // installs a watcher so we pick up edits without restarting
  // VS Code. Safe to call multiple times — the second call just
  // re-reads.
  async initialize(): Promise<void> {
    await this.refresh();
    if (!this.watcher) {
      this.watcher = vscode.workspace.createFileSystemWatcher(
        "**/" + RULES_FILE_NAME,
      );
      const onAny = () => void this.refresh();
      this.watcher.onDidChange(onAny);
      this.watcher.onDidCreate(onAny);
      this.watcher.onDidDelete(onAny);
    }
  }

  get(): Rules | undefined {
    return this.current;
  }

  // onChange subscribes to rule-file changes. Returns a Disposable
  // so callers can unsubscribe alongside their own teardown.
  onChange(fn: (r: Rules | undefined) => void): vscode.Disposable {
    this.listeners.push(fn);
    return {
      dispose: () => {
        const i = this.listeners.indexOf(fn);
        if (i >= 0) this.listeners.splice(i, 1);
      },
    };
  }

  dispose(): void {
    this.watcher?.dispose();
    this.watcher = undefined;
  }

  // refresh scans the workspace for any `.talyvor-rules` file and
  // picks the one closest to the first workspace folder root.
  // Multi-root workspaces use the first folder — surfacing one
  // unambiguous rules file is more predictable than merging.
  private async refresh(): Promise<void> {
    const matches = await vscode.workspace.findFiles(
      "**/" + RULES_FILE_NAME,
      "**/node_modules/**",
      5,
    );
    if (matches.length === 0) {
      this.set(undefined);
      return;
    }
    const folder = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath ?? "";
    const sorted = [...matches].sort((a, b) =>
      depthFromRoot(a.fsPath, folder) - depthFromRoot(b.fsPath, folder),
    );
    const target = sorted[0];
    try {
      const stat = await vscode.workspace.fs.stat(target);
      if (stat.size > MAX_RULES_FILE_BYTES) {
        // Refuse oversize files but keep any previous version so
        // the user isn't silently left with no rules.
        void vscode.window.showWarningMessage(
          `${RULES_FILE_NAME} exceeds ${MAX_RULES_FILE_BYTES} bytes — ignoring.`,
        );
        return;
      }
      const bytes = await vscode.workspace.fs.readFile(target);
      const raw = new TextDecoder().decode(bytes);
      const rules: Rules = {
        raw,
        filePath: target.fsPath,
        sections: parseRules(raw),
      };
      this.set(rules);
    } catch {
      this.set(undefined);
    }
  }

  private set(r: Rules | undefined): void {
    this.current = r;
    for (const l of this.listeners) {
      try {
        l(r);
      } catch {
        // listeners are best-effort
      }
    }
  }
}

// depthFromRoot returns the directory depth of `file` relative
// to `root` so the closest match wins when multiple rules files
// exist (e.g. a monorepo with package-level overrides).
function depthFromRoot(file: string, root: string): number {
  if (!root) return file.split(/[\\/]/).length;
  const rel = file.startsWith(root) ? file.slice(root.length) : file;
  return rel.split(/[\\/]/).filter((p) => p.length > 0).length;
}
