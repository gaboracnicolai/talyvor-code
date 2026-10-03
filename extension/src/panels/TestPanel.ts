// TestPanel — webview showing AI-generated tests. One panel per
// generation (a new generation reuses an existing panel if one is
// open). Three actions in the body: create the test file, append
// to an existing file, copy to clipboard.

import * as vscode from "vscode";
import type { GeneratedTests } from "../providers/test-generator";
import { escapeHTML } from "./chat-pure";
import { TEST_CSS, estimateHTML } from "./theme-pure";

type Inbound =
  | { type: "createFile" }
  | { type: "insertExisting" }
  | { type: "copy" };

export class TestPanel {
  private static current: TestPanel | undefined;
  private readonly disposables: vscode.Disposable[] = [];

  static show(
    extensionUri: vscode.Uri,
    tests: GeneratedTests,
    sourceUri: vscode.Uri,
  ): void {
    if (TestPanel.current) {
      TestPanel.current.update(tests, sourceUri);
      TestPanel.current.panel.reveal(vscode.ViewColumn.Beside);
      return;
    }
    const panel = vscode.window.createWebviewPanel(
      "talyvorTests",
      "Talyvor: Generated Tests",
      vscode.ViewColumn.Beside,
      {
        enableScripts: true,
        retainContextWhenHidden: true,
        localResourceRoots: [extensionUri],
      },
    );
    TestPanel.current = new TestPanel(panel, tests, sourceUri);
  }

  private constructor(
    private readonly panel: vscode.WebviewPanel,
    private tests: GeneratedTests,
    private sourceUri: vscode.Uri,
  ) {
    this.panel.webview.html = this.renderHTML();
    this.panel.webview.onDidReceiveMessage(
      (raw: Inbound) => void this.handleMessage(raw),
      null,
      this.disposables,
    );
    this.panel.onDidDispose(() => this.dispose(), null, this.disposables);
  }

  private dispose(): void {
    TestPanel.current = undefined;
    this.panel.dispose();
    while (this.disposables.length) this.disposables.pop()?.dispose();
  }

  // update swaps the test contents without rebuilding the panel —
  // used when the user re-runs Generate Tests on a different file.
  private update(tests: GeneratedTests, sourceUri: vscode.Uri): void {
    this.tests = tests;
    this.sourceUri = sourceUri;
    this.panel.webview.html = this.renderHTML();
  }

  private async handleMessage(msg: Inbound): Promise<void> {
    switch (msg.type) {
      case "createFile":
        await this.createTestFile();
        break;
      case "insertExisting":
        await this.insertIntoExisting();
        break;
      case "copy":
        await vscode.env.clipboard.writeText(this.tests.code);
        void vscode.window.showInformationMessage(
          "Tests copied to clipboard.",
        );
        break;
    }
  }

  // createTestFile writes the generated tests to the suggested
  // path. If the file already exists we confirm before overwriting
  // — the AI generated tests in good faith but the user may have
  // hand-written work we'd otherwise clobber.
  private async createTestFile(): Promise<void> {
    const target = this.resolveTargetPath();
    let exists = false;
    try {
      await vscode.workspace.fs.stat(target);
      exists = true;
    } catch {
      // not found — proceed
    }
    if (exists) {
      const choice = await vscode.window.showWarningMessage(
        `${target.fsPath} already exists. Overwrite?`,
        { modal: true },
        "Overwrite",
        "Cancel",
      );
      if (choice !== "Overwrite") return;
    }
    const enc = new TextEncoder();
    await vscode.workspace.fs.writeFile(target, enc.encode(this.tests.code));
    const doc = await vscode.workspace.openTextDocument(target);
    await vscode.window.showTextDocument(doc, vscode.ViewColumn.One);
    void vscode.window.showInformationMessage(
      `Created ${target.fsPath}`,
    );
  }

  // insertIntoExisting opens a file picker, then appends the
  // generated tests to the chosen file's tail. Useful when the
  // project keeps all suite cases in one big file.
  private async insertIntoExisting(): Promise<void> {
    const picked = await vscode.window.showOpenDialog({
      canSelectFiles: true,
      canSelectMany: false,
      openLabel: "Append tests to…",
    });
    if (!picked || picked.length === 0) return;
    const target = picked[0];
    const doc = await vscode.workspace.openTextDocument(target);
    const editor = await vscode.window.showTextDocument(doc);
    const end = new vscode.Position(doc.lineCount, 0);
    await editor.edit((edit) =>
      edit.insert(end, "\n\n" + this.tests.code),
    );
    void vscode.window.showInformationMessage(
      `Appended tests to ${target.fsPath}`,
    );
  }

  // resolveTargetPath turns the relative filename suggestion back
  // into a URI rooted at the source file's directory. The pure
  // helper hands us a string like "auth.test.ts" or
  // "src/auth.test.ts"; we anchor that on the source file.
  private resolveTargetPath(): vscode.Uri {
    const sourceDir = vscode.Uri.joinPath(this.sourceUri, "..");
    // Take the basename of the suggestion so we don't accidentally
    // walk outside the source's directory.
    const i = Math.max(
      this.tests.fileName.lastIndexOf("/"),
      this.tests.fileName.lastIndexOf("\\"),
    );
    const baseName =
      i >= 0 ? this.tests.fileName.substring(i + 1) : this.tests.fileName;
    return vscode.Uri.joinPath(sourceDir, baseName);
  }

  // renderHTML emits the static webview — header, code preview,
  // three action buttons, footer with cost + model + Lens credit.
  private renderHTML(): string {
    return `<!doctype html><html><head><meta charset="utf-8">
<style>${this.css()}</style>
</head><body>
<header>
  <span class="eyebrow">Talyvor · Generated tests</span>
  <span class="tag">${escapeHTML(this.tests.framework)}</span>
</header>
<section class="meta">
  <div><span class="eyebrow">Source</span><code>${escapeHTML(basename(this.sourceUri.fsPath))}</code></div>
  <div><span class="eyebrow">Target</span><code>${escapeHTML(this.tests.fileName)}</code></div>
  <div><span class="eyebrow">Language</span><code>${escapeHTML(this.tests.language)}</code></div>
</section>
<div class="actions">
  <button id="create">Create test file</button>
  <button id="insert" class="ghost">Insert into existing…</button>
  <button id="copy" class="ghost">Copy</button>
</div>
<pre><code>${escapeHTML(this.tests.code)}</code></pre>
<footer>
  <span class="eyebrow">Model</span> claude-sonnet-4-6 · <span class="eyebrow">Cost</span> ${estimateHTML(this.tests.costUSD)} · Powered by Talyvor Lens
</footer>
<script>${this.script()}</script>
</body></html>`;
  }

  private css(): string {
    return TEST_CSS;
  }

  private script(): string {
    return `(() => {
const vscode = acquireVsCodeApi();
document.getElementById('create').addEventListener('click', () => vscode.postMessage({type:'createFile'}));
document.getElementById('insert').addEventListener('click', () => vscode.postMessage({type:'insertExisting'}));
document.getElementById('copy').addEventListener('click', () => vscode.postMessage({type:'copy'}));
})();`;
  }
}

function basename(p: string): string {
  const i = Math.max(p.lastIndexOf("/"), p.lastIndexOf("\\"));
  return i >= 0 ? p.substring(i + 1) : p;
}
