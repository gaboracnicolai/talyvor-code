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
import { DOCS_CSS } from "../panels/theme-pure";
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
  <span class="eyebrow">Talyvor · Docs</span>
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
  <div class="r-meta"><span class="tag badge-${escapeHTML(r.source)}">${escapeHTML(r.source)}</span> <span class="eyebrow">rank</span> <span class="figure">${r.rank.toFixed(2)}</span></div>
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
      <span><span class="eyebrow">AI cost</span> <span class="figure">$${page.aiCostUsd.toFixed(2)}</span></span>
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
  <h2 class="eyebrow">Asked the docs</h2>
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
  <h2 class="eyebrow">Asked the docs</h2>
  <p class="q">${q}</p>
  <div class="answer">${renderMarkdown(this.askResult.answer)}</div>
  ${sources ? `<h3 class="eyebrow">Sources</h3><ul class="sources">${sources}</ul>` : ""}
  <form id="followupForm">
    <input id="followup" type="text" placeholder="Follow-up question…">
    <button type="submit">Ask</button>
  </form>
</div>`;
  }

  private css(): string {
    return DOCS_CSS;
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
