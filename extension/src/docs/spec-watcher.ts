// SpecWatcher polls the Docs server for updates to the pages a
// user is actively coding against. When a page's updated_at
// changes we surface a non-intrusive information notification so
// the engineer can review what shifted in the spec.
//
// The watcher is intentionally a single timer per session, not
// per-page — Docs handles the per-page batching server-side
// (search includes only what the user has linked to the active
// issue).

import * as vscode from "vscode";
import type { DocsClient, DocsPage } from "./docs-client";

const WATCH_INTERVAL_MS = 10 * 60 * 1000; // 10 minutes

export class SpecWatcher implements vscode.Disposable {
  // watchedPages tracks pageId → "spaceId/lastUpdatedAt" so we
  // can detect when the page server-side has changed since we
  // last looked.
  private watchedPages = new Map<string, { spaceId: string; updatedAt: string; title: string }>();
  private timer: ReturnType<typeof setInterval> | undefined;

  constructor(private readonly docsClient: DocsClient) {}

  // startWatching seeds the watcher with the pages we care
  // about. `pageRefs` is a list of "spaceId/pageId" strings — the
  // canonical reference shape we use elsewhere.
  startWatching(pageRefs: string[]): void {
    this.stop();
    this.watchedPages.clear();
    for (const ref of pageRefs) {
      const [spaceId, pageId] = ref.split("/", 2);
      if (!spaceId || !pageId) continue;
      this.watchedPages.set(pageId, { spaceId, updatedAt: "", title: "" });
    }
    if (this.watchedPages.size === 0) return;
    if (!this.docsClient.isConfigured()) return;
    void this.checkOnce(); // initial seed
    this.timer = setInterval(() => void this.checkOnce(), WATCH_INTERVAL_MS);
  }

  stop(): void {
    if (this.timer) {
      clearInterval(this.timer);
      this.timer = undefined;
    }
  }

  dispose(): void {
    this.stop();
  }

  // checkOnce is the polling tick. We pull the current state of
  // every watched page; the first poll seeds `updatedAt` so we
  // don't fire on startup; subsequent polls fire when the
  // server-side timestamp moves.
  async checkOnce(): Promise<void> {
    for (const [pageId, entry] of this.watchedPages) {
      const page = await this.docsClient.getPage(entry.spaceId, pageId);
      if (!page) continue;
      if (entry.updatedAt === "") {
        // First time we've seen this page — record without alerting.
        entry.updatedAt = page.updatedAt;
        entry.title = page.title;
        continue;
      }
      if (page.updatedAt && page.updatedAt !== entry.updatedAt) {
        this.notifyUpdated(entry.spaceId, pageId, page);
        entry.updatedAt = page.updatedAt;
        entry.title = page.title;
      }
    }
  }

  private notifyUpdated(spaceId: string, pageId: string, page: DocsPage): void {
    const docsBase = this.docsClient.baseURL();
    const url = `${docsBase}/spaces/${spaceId}/pages/${pageId}`;
    // Information-level only — spec-watcher is informational, not
    // a blocker. The engineer can ignore it if they're in flow.
    void vscode.window
      .showInformationMessage(
        `📄 Spec updated: ${page.title}`,
        "View in Docs",
      )
      .then((action) => {
        if (action === "View in Docs") {
          void vscode.env.openExternal(vscode.Uri.parse(url));
        }
      });
  }
}
