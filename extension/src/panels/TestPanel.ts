// TestPanel — webview showing AI-generated tests. One panel per
// generation (a new generation reuses an existing panel if one is
// open). Three actions in the body: create the test file, append
// to an existing file, copy to clipboard.

import * as vscode from "vscode";
import type { GeneratedTests } from "../providers/test-generator";
import { escapeHTML } from "./chat-pure";

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
  <span class="brand">Generated Tests</span>
  <span class="chip">${escapeHTML(this.tests.framework)}</span>
</header>
<section class="meta">
  <div><span>Source</span><code>${escapeHTML(basename(this.sourceUri.fsPath))}</code></div>
  <div><span>Target</span><code>${escapeHTML(this.tests.fileName)}</code></div>
  <div><span>Language</span><code>${escapeHTML(this.tests.language)}</code></div>
</section>
<div class="actions">
  <button id="create">Create test file</button>
  <button id="insert">Insert into existing…</button>
  <button id="copy">Copy</button>
</div>
<pre><code>${escapeHTML(this.tests.code)}</code></pre>
<footer>
  Generated with claude-sonnet-4-6 · cost $${this.tests.costUSD.toFixed(4)} · Powered by Talyvor Lens
</footer>
<script>${this.script()}</script>
</body></html>`;
  }

  private css(): string {
    return `body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#d4d8e2;background:#1e1e1e;margin:0;padding:0;font-size:13px;line-height:1.45}
header{display:flex;align-items:center;gap:8px;padding:8px 12px;border-bottom:1px solid #2a2a2a;background:#191919}
header .brand{font-weight:600;color:#fff}
.chip{font-size:11px;background:#2a2a2a;color:#f0a030;padding:2px 6px;border-radius:4px;font-family:monospace}
.meta{display:flex;gap:18px;padding:8px 12px;border-bottom:1px solid #2a2a2a;background:#16181d;font-size:11px;color:#888}
.meta>div{display:flex;align-items:center;gap:6px}
.meta code{color:#d4d8e2;background:#0c0e12;padding:2px 6px;border-radius:3px;font-family:"SF Mono",Menlo,monospace}
.actions{display:flex;gap:6px;padding:8px 12px;border-bottom:1px solid #2a2a2a;background:#191919}
.actions button{background:#2a2a2a;color:#d4d8e2;border:1px solid #333;border-radius:4px;padding:5px 12px;font-size:12px;cursor:pointer}
.actions button:first-child{background:#f0a030;color:#1e1e1e;border-color:#f0a030;font-weight:600}
.actions button:hover{filter:brightness(1.1)}
pre{margin:0;padding:12px;background:#0c0e12;overflow:auto;font-family:"SF Mono",Menlo,Consolas,monospace;font-size:12px;color:#d4d8e2;white-space:pre;line-height:1.5}
footer{padding:6px 12px;border-top:1px solid #2a2a2a;background:#191919;font-size:11px;color:#666}`;
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
