// DocsPanel — webview for browsing Talyvor Docs from inside VS
// Code. Three views in one panel:
//   - search: search input + results list
//   - page:   selected page with freshness badge + rendered MD
//   - ask:    inline Q&A with sources, posts to /ai/ask
//
// Mode switches happen by re-rendering HTML. We keep the panel
// instance singleton so re-opening reveals the existing one
// (mirrors ChatPanel / AgentPanel).

import * as vscode from "vscode";
import type { DocsClient, DocsPage, AskResult } from "./docs-client";
import type { LensConfig } from "../lens/types";
import {
  absolutiseDocsURL,
  escapeHTML,
  freshnessIcon,
  renderMarkdown,
  type DocsSearchResult,
} from "./docs-pure";

type Inbound =
  | { type: "search"; query: string }
  | { type: "openPage"; spaceUrl: string; pageId: string; pageTitle: string }
  | { type: "openExternal"; url: string }
  | { type: "ask"; question: string }
  | { type: "back" };

export class DocsPanel {
  private static current: DocsPanel | undefined;
  private readonly disposables: vscode.Disposable[] = [];
  private mode: "search" | "page" | "ask" = "search";
  private searchResults: DocsSearchResult[] = [];
  private lastQuery = "";
  private currentPage: DocsPage | undefined;
  private currentPageUrl = "";
  private askResult: AskResult | undefined;
  private askQuestion = "";

  static createOrShow(
    extensionUri: vscode.Uri,
    docsClient: DocsClient,
    config: LensConfig,
    initialQuery = "",
  ): void {
    if (DocsPanel.current) {
      DocsPanel.current.panel.reveal(vscode.ViewColumn.Beside);
      if (initialQuery) void DocsPanel.current.runSearch(initialQuery);
      return;
    }
    const panel = vscode.window.createWebviewPanel(
      "talyvorDocs",
      "Talyvor Docs",
      vscode.ViewColumn.Beside,
      {
        enableScripts: true,
        retainContextWhenHidden: true,
        localResourceRoots: [extensionUri],
      },
    );
    DocsPanel.current = new DocsPanel(panel, docsClient, config);
    if (initialQuery) void DocsPanel.current.runSearch(initialQuery);
  }

  // ask is the host-side entry point for the askDocs command —
  // opens (or reveals) the panel and immediately runs the Q&A
  // flow with the supplied question.
  static async ask(
    extensionUri: vscode.Uri,
    docsClient: DocsClient,
    config: LensConfig,
    question: string,
  ): Promise<void> {
    DocsPanel.createOrShow(extensionUri, docsClient, config);
    await DocsPanel.current?.runAsk(question);
  }

  private constructor(
    private readonly panel: vscode.WebviewPanel,
    private readonly docsClient: DocsClient,
    private config: LensConfig,
  ) {
    this.render();
    this.panel.webview.onDidReceiveMessage(
      (raw: Inbound) => void this.handleMessage(raw),
      null,
      this.disposables,
    );
    this.panel.onDidDispose(() => this.dispose(), null, this.disposables);
  }

  private dispose(): void {
    DocsPanel.current = undefined;
    this.panel.dispose();
    while (this.disposables.length) this.disposables.pop()?.dispose();
  }

  private async handleMessage(msg: Inbound): Promise<void> {
    switch (msg.type) {
      case "search":
        await this.runSearch(msg.query);
        break;
      case "openPage": {
        const parts = msg.spaceUrl.split("/spaces/")[1]?.split("/pages/");
        if (!parts || parts.length < 2) return;
        const spaceId = parts[0];
        const pageId = parts[1];
        await this.openPage(spaceId, pageId);
        break;
      }
      case "openExternal":
        await vscode.env.openExternal(vscode.Uri.parse(msg.url));
        break;
      case "ask":
        await this.runAsk(msg.question);
        break;
      case "back":
        this.mode = "search";
        this.render();
        break;
    }
  }

  async runSearch(query: string): Promise<void> {
    if (!this.docsClient.isConfigured()) {
      void vscode.window.showWarningMessage(
        "Talyvor Docs isn't configured. Set talyvor.docsUrl + talyvor.docsApiKey.",
      );
      return;
    }
    if (!this.config.workspaceId) {
      void vscode.window.showWarningMessage(
        "Set talyvor.workspaceId before searching docs.",
      );
      return;
    }
    this.lastQuery = query;
    this.mode = "search";
    this.searchResults = await this.docsClient.searchDocs(
      this.config.workspaceId,
      query,
      10,
    );
    this.render();
  }

  private async openPage(spaceId: string, pageId: string): Promise<void> {
    const page = await this.docsClient.getPage(spaceId, pageId);
    if (!page) {
      void vscode.window.showWarningMessage("Page not found.");
      return;
    }
    this.currentPage = page;
    this.currentPageUrl = absolutiseDocsURL(
      `/spaces/${spaceId}/pages/${pageId}`,
      this.docsClient.baseURL(),
    );
    this.mode = "page";
    this.render();
  }

  async runAsk(question: string): Promise<void> {
    if (!this.docsClient.isConfigured() || !this.config.workspaceId) return;
    this.askQuestion = question;
    this.mode = "ask";
    this.askResult = undefined;
    this.render(); // show pending state
    const res = await this.docsClient.askDocs(
      this.config.workspaceId,
      question,
    );
    this.askResult = res ?? { answer: "(no answer)", sources: [] };
    this.render();
  }

  private render(): void {
    this.panel.webview.html = this.renderHTML();
  }

  private renderHTML(): string {
    const body =
      this.mode === "page"
        ? this.renderPage()
        : this.mode === "ask"
          ? this.renderAsk()
          : this.renderSearch();
    return `<!doctype html><html><head><meta charset="utf-8">
<style>${this.css()}</style>
</head><body>
<header>
  <span class="brand">Talyvor Docs</span>
  ${this.mode !== "search" ? `<button id="backBtn" class="ghost">← Back</button>` : ""}
</header>
<main>${body}</main>
<script>${this.script()}</script>
</body></html>`;
  }

  private renderSearch(): string {
    const rows = this.searchResults
      .map(
        (r) =>
          `<li class="result" data-url="${escapeHTML(r.url)}" data-pageid="${escapeHTML(r.pageId)}" data-title="${escapeHTML(r.pageTitle)}">
  <div class="r-title">${escapeHTML(r.pageTitle)} <span class="space">${escapeHTML(r.spaceName)}</span></div>
  <div class="r-headline">${escapeHTML(r.headline)}</div>
  <div class="r-meta"><span class="badge badge-${escapeHTML(r.source)}">${escapeHTML(r.source)}</span> <span class="rank">rank ${r.rank.toFixed(2)}</span></div>
</li>`,
      )
      .join("");
    const empty =
      this.lastQuery !== "" && this.searchResults.length === 0
        ? `<p class="muted">No results for "${escapeHTML(this.lastQuery)}".</p>`
        : "";
    return `<form id="searchForm">
  <input id="q" type="search" placeholder="Search docs… (full-text + semantic)" value="${escapeHTML(this.lastQuery)}">
  <button type="submit">Search</button>
</form>
${empty}
<ul class="results">${rows}</ul>`;
  }

  private renderPage(): string {
    const page = this.currentPage;
    if (!page) return `<p class="muted">No page selected.</p>`;
    const fresh = freshnessIcon(page.freshnessStatus);
    const verified = page.lastVerifiedAt
      ? `<span class="verified">Verified ✓ ${escapeHTML(page.lastVerifiedAt)}</span>`
      : `<span class="needs">Needs verification</span>`;
    return `<article class="page">
  <header class="page-header">
    <h1>${escapeHTML(page.title)}</h1>
    <div class="page-meta">
      <span class="freshness" style="color:${fresh.color}">${fresh.emoji} ${fresh.label}</span>
      ${verified}
      <span class="muted">AI cost: $${page.aiCostUsd.toFixed(2)}</span>
    </div>
    <div class="page-actions">
      <button data-action="ask">Ask AI about this doc</button>
      <button data-action="external" data-url="${escapeHTML(this.currentPageUrl)}" class="ghost">Open in browser</button>
    </div>
  </header>
  <div class="md-body">${renderMarkdown(page.contentText)}</div>
</article>`;
  }

  private renderAsk(): string {
    const q = escapeHTML(this.askQuestion);
    if (!this.askResult) {
      return `<div class="ask">
  <h2>Asked the docs</h2>
  <p class="muted">${q}</p>
  <div class="dots"><i></i><i></i><i></i></div>
</div>`;
    }
    const sources = this.askResult.sources
      .map(
        (s) =>
          `<li><a href="#" data-url="${escapeHTML(absolutiseDocsURL(s.url, this.docsClient.baseURL()))}" class="ext-link">${escapeHTML(s.title)}</a></li>`,
      )
      .join("");
    return `<div class="ask">
  <h2>Asked the docs</h2>
  <p class="q">${q}</p>
  <div class="answer">${renderMarkdown(this.askResult.answer)}</div>
  ${sources ? `<h3>Sources</h3><ul class="sources">${sources}</ul>` : ""}
  <form id="followupForm">
    <input id="followup" type="text" placeholder="Follow-up question…">
    <button type="submit">Ask</button>
  </form>
</div>`;
  }

  private css(): string {
    return `body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#d4d8e2;background:#1e1e1e;margin:0;display:flex;flex-direction:column;height:100vh;line-height:1.5;font-size:13px}
header{display:flex;align-items:center;gap:8px;padding:8px 12px;border-bottom:1px solid #2a2a2a;background:#191919}
header .brand{font-weight:600;color:#fff}
header button{margin-left:auto}
main{flex:1;overflow-y:auto;padding:12px}
button{background:#f0a030;color:#1e1e1e;border:0;border-radius:4px;padding:6px 14px;font-weight:600;cursor:pointer;font-size:12px}
button.ghost{background:#2a2a2a;color:#d4d8e2;border:1px solid #333}
button:hover{opacity:0.9}
form{display:flex;gap:6px;margin-bottom:12px}
input[type=search],input[type=text]{flex:1;background:#0c0e12;color:#d4d8e2;border:1px solid #2a2a2a;border-radius:6px;padding:6px 10px;font-size:13px}
input:focus{outline:none;border-color:#f0a030}
.results{list-style:none;padding:0;margin:0;display:flex;flex-direction:column;gap:8px}
.result{padding:8px 10px;border:1px solid #2a2a2a;border-radius:6px;cursor:pointer}
.result:hover{border-color:#f0a030}
.r-title{font-weight:600;color:#fff}
.r-title .space{font-size:11px;color:#888;font-weight:400;margin-left:6px}
.r-headline{font-size:12px;color:#aaa;margin-top:4px}
.r-meta{margin-top:6px;font-size:10px;color:#666;display:flex;gap:8px;align-items:center}
.badge{font-size:10px;background:#2a2a2a;color:#f0a030;padding:1px 6px;border-radius:3px;text-transform:uppercase;letter-spacing:0.05em}
.muted{color:#666;font-size:12px}
.page-header h1{margin:0 0 6px;font-size:20px;color:#fff}
.page-meta{display:flex;gap:10px;font-size:11px;color:#888;margin-bottom:6px;flex-wrap:wrap;align-items:center}
.freshness{font-weight:500}
.verified{color:#5cd187}
.needs{color:#f0a030}
.page-actions{display:flex;gap:6px;margin:6px 0 12px}
.md-body h1,.md-body h2,.md-body h3{color:#fff;margin-top:16px}
.md-h1{font-size:18px}
.md-h2{font-size:16px}
.md-h3{font-size:14px}
.md-p{margin:6px 0}
.md-code{background:#0c0e12;border:1px solid #1f242c;border-radius:6px;padding:8px;font-family:"SF Mono",Menlo,monospace;font-size:12px;overflow-x:auto}
.md-ic{background:#13161c;padding:1px 4px;border-radius:3px;font-family:"SF Mono",Menlo,monospace;font-size:11px}
.md-ul,.md-ol{margin:6px 0;padding-left:20px}
a{color:#f0a030}
.ask h2{font-size:13px;color:#aaa;text-transform:uppercase;letter-spacing:0.05em;margin:0 0 8px}
.ask .q{padding:6px 8px;background:#13161c;border-radius:4px;color:#ddd}
.ask .answer{margin-top:8px}
.sources{margin:6px 0 12px;padding-left:18px;font-size:12px}
.dots{display:flex;gap:6px;padding:12px 0}
.dots i{width:6px;height:6px;border-radius:50%;background:#888;animation:bounce 1.2s infinite}
.dots i:nth-child(2){animation-delay:0.15s}
.dots i:nth-child(3){animation-delay:0.3s}
@keyframes bounce{0%,80%,100%{transform:scale(0.6);opacity:0.4}40%{transform:scale(1);opacity:1}}`;
  }

  private script(): string {
    return `(() => {
const vscode = acquireVsCodeApi();
const back = document.getElementById('backBtn');
if (back) back.addEventListener('click', () => vscode.postMessage({type:'back'}));

const sf = document.getElementById('searchForm');
if (sf) sf.addEventListener('submit', (e) => {
  e.preventDefault();
  const q = document.getElementById('q').value.trim();
  if (q) vscode.postMessage({type:'search', query: q});
});

document.querySelectorAll('.result').forEach((node) => {
  node.addEventListener('click', () => {
    vscode.postMessage({
      type: 'openPage',
      spaceUrl: node.dataset.url,
      pageId: node.dataset.pageid,
      pageTitle: node.dataset.title,
    });
  });
});

document.querySelectorAll('[data-action]').forEach((btn) => {
  btn.addEventListener('click', (e) => {
    const action = btn.dataset.action;
    if (action === 'external') {
      vscode.postMessage({type:'openExternal', url: btn.dataset.url});
    } else if (action === 'ask') {
      const q = prompt('What would you like to know about this doc?');
      if (q && q.trim()) vscode.postMessage({type:'ask', question: q.trim()});
    }
  });
});

document.querySelectorAll('.ext-link').forEach((a) => {
  a.addEventListener('click', (e) => {
    e.preventDefault();
    vscode.postMessage({type:'openExternal', url: a.dataset.url});
  });
});

const ff = document.getElementById('followupForm');
if (ff) ff.addEventListener('submit', (e) => {
  e.preventDefault();
  const q = document.getElementById('followup').value.trim();
  if (q) vscode.postMessage({type:'ask', question: q});
});
})();`;
  }
}
