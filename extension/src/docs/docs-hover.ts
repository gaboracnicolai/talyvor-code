// DocsHoverProvider shows a "related spec" snippet next to the
// editor tooltip when Docs has a strong match for the symbol
// under the cursor. Three operational constraints:
//   1. Never fire when Docs is unconfigured — most users won't
//      have Docs running and we should not add latency.
//   2. 500ms debounce so a rapid scroll doesn't fire one request
//      per character.
//   3. Only surface hits above HOVER_RANK_THRESHOLD so the hover
//      stays useful — a low-score match is more noise than help.

import * as vscode from "vscode";
import type { DocsClient } from "./docs-client";
import type { LensConfig } from "../lens/types";
import {
  buildHoverMarkdown,
  buildHoverQuery,
  pickRelevantHit,
} from "./docs-pure";

const HOVER_DEBOUNCE_MS = 500;

export type DocsConfigSupplier = () => LensConfig;

export class DocsHoverProvider implements vscode.HoverProvider {
  // pendingTimer + lastHit cache the most recent debounce target
  // so a hover on the same word resolves instantly rather than
  // waiting another 500ms.
  private pendingTimer: ReturnType<typeof setTimeout> | undefined;
  private lastQuery = "";
  private lastResult: vscode.Hover | undefined;

  constructor(
    private readonly docsClient: DocsClient,
    private readonly config: DocsConfigSupplier,
  ) {}

  async provideHover(
    document: vscode.TextDocument,
    position: vscode.Position,
    token: vscode.CancellationToken,
  ): Promise<vscode.Hover | undefined> {
    if (!this.docsClient.isConfigured()) return undefined;
    const cfg = this.config();
    if (!cfg.workspaceId) return undefined;

    const wordRange = document.getWordRangeAtPosition(position);
    if (!wordRange) return undefined;
    const word = document.getText(wordRange);
    if (word.length < 3) return undefined; // skip stray tokens

    const symbols = surroundingSymbols(document, position);
    const query = buildHoverQuery(word, symbols);
    if (query === this.lastQuery && this.lastResult) {
      return this.lastResult;
    }

    const ctrl = new AbortController();
    token.onCancellationRequested(() => ctrl.abort());

    const hit = await this.debouncedSearch(cfg.workspaceId, query, ctrl.signal);
    if (!hit) return undefined;

    const markdown = new vscode.MarkdownString(
      buildHoverMarkdown(hit, this.docsClient.baseURL()),
    );
    markdown.isTrusted = true;
    markdown.supportHtml = false;
    const hover = new vscode.Hover(markdown, wordRange);
    this.lastQuery = query;
    this.lastResult = hover;
    return hover;
  }

  private debouncedSearch(
    workspaceId: string,
    query: string,
    signal: AbortSignal,
  ): Promise<ReturnType<typeof pickRelevantHit>> {
    if (this.pendingTimer) clearTimeout(this.pendingTimer);
    return new Promise((resolve) => {
      this.pendingTimer = setTimeout(async () => {
        if (signal.aborted) {
          resolve(undefined);
          return;
        }
        const results = await this.docsClient.searchDocs(
          workspaceId,
          query,
          5,
          signal,
        );
        resolve(pickRelevantHit(results));
      }, HOVER_DEBOUNCE_MS);
    });
  }
}

// surroundingSymbols extracts a couple of coarse contextual
// names: the nearest enclosing function/class identifier above
// the cursor. Good enough to enrich the docs query without a
// full AST walk — for that we'd want a language server.
function surroundingSymbols(
  document: vscode.TextDocument,
  position: vscode.Position,
): string[] {
  const out: string[] = [];
  const re = /(?:function|class|interface|type|def|fn|func)\s+([A-Za-z_][\w]*)/;
  for (let line = position.line; line >= Math.max(0, position.line - 25); line--) {
    const text = document.lineAt(line).text;
    const m = text.match(re);
    if (m && m[1] && !out.includes(m[1])) {
      out.push(m[1]);
      if (out.length === 2) break;
    }
  }
  return out;
}
