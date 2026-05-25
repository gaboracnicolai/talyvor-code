// vscode-bound loader for .talyvor-context. Mirrors the rules
// loader (extension/src/rules/rules-loader.ts): scan via
// findFiles, watch for changes, expose synchronous get() for hot
// paths.

import * as vscode from "vscode";
import {
  CONTEXT_FILE_NAME,
  MAX_CONTEXT_FILE_BYTES,
  parseContext,
  type ProjectContext,
} from "./context-pure";

export class ContextLoader implements vscode.Disposable {
  private current: ProjectContext | undefined;
  private watcher: vscode.FileSystemWatcher | undefined;
  private readonly listeners: Array<(c: ProjectContext | undefined) => void> = [];

  async initialize(): Promise<void> {
    await this.refresh();
    if (!this.watcher) {
      this.watcher = vscode.workspace.createFileSystemWatcher("**/" + CONTEXT_FILE_NAME);
      const onAny = () => void this.refresh();
      this.watcher.onDidChange(onAny);
      this.watcher.onDidCreate(onAny);
      this.watcher.onDidDelete(onAny);
    }
  }

  get(): ProjectContext | undefined {
    return this.current;
  }

  onChange(fn: (c: ProjectContext | undefined) => void): vscode.Disposable {
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

  private async refresh(): Promise<void> {
    const matches = await vscode.workspace.findFiles(
      "**/" + CONTEXT_FILE_NAME,
      "**/node_modules/**",
      5,
    );
    if (matches.length === 0) {
      this.set(undefined);
      return;
    }
    const folder = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath ?? "";
    const sorted = [...matches].sort((a, b) =>
      depth(a.fsPath, folder) - depth(b.fsPath, folder),
    );
    const target = sorted[0];
    try {
      const stat = await vscode.workspace.fs.stat(target);
      if (stat.size > MAX_CONTEXT_FILE_BYTES) {
        void vscode.window.showWarningMessage(
          `${CONTEXT_FILE_NAME} exceeds ${MAX_CONTEXT_FILE_BYTES} bytes — ignoring.`,
        );
        return;
      }
      const bytes = await vscode.workspace.fs.readFile(target);
      const parsed = parseContext(new TextDecoder().decode(bytes));
      if (parsed) parsed.filePath = target.fsPath;
      this.set(parsed);
    } catch {
      this.set(undefined);
    }
  }

  private set(c: ProjectContext | undefined): void {
    this.current = c;
    for (const l of this.listeners) {
      try {
        l(c);
      } catch {
        // listeners are best-effort
      }
    }
  }
}

function depth(file: string, root: string): number {
  if (!root) return file.split(/[\\/]/).length;
  const rel = file.startsWith(root) ? file.slice(root.length) : file;
  return rel.split(/[\\/]/).filter((p) => p.length > 0).length;
}
