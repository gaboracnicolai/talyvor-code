// Talyvor Docs HTTP client. Three operations: workspace search,
// per-page fetch, ask-the-docs Q&A. Lean surface — anything
// richer (comments, attachments) stays in the Docs web UI.

import type { DocsSearchResult } from "./docs-pure";

export interface DocsPage {
  id: string;
  spaceId: string;
  title: string;
  contentText: string;
  freshnessStatus: string;
  aiCostUsd: number;
  lastVerifiedAt?: string;
  updatedAt: string;
}

export interface AskSource {
  title: string;
  url: string;
}

export interface AskResult {
  answer: string;
  sources: AskSource[];
}

interface RawSearchEnvelope {
  results?: RawSearchResult[];
  total?: number;
}

interface RawSearchResult {
  page_id?: string;
  page_title?: string;
  space_name?: string;
  headline?: string;
  rank?: number;
  similarity?: number;
  source?: string;
  url?: string;
}

interface RawPage {
  id?: string;
  space_id?: string;
  title?: string;
  content_text?: string;
  freshness_status?: string;
  ai_cost_usd?: number;
  last_verified_at?: string;
  updated_at?: string;
}

function normaliseSearch(raw: RawSearchResult): DocsSearchResult {
  const sourceRaw = (raw.source ?? "").toLowerCase();
  const source: DocsSearchResult["source"] =
    sourceRaw === "fulltext" || sourceRaw === "semantic" || sourceRaw === "both"
      ? (sourceRaw as DocsSearchResult["source"])
      : "fulltext";
  const score = raw.rank ?? raw.similarity ?? 0;
  return {
    pageId: raw.page_id ?? "",
    pageTitle: raw.page_title ?? "",
    spaceName: raw.space_name ?? "",
    headline: raw.headline ?? "",
    rank: score,
    source,
    url: raw.url ?? "",
  };
}

function normalisePage(raw: RawPage): DocsPage {
  return {
    id: raw.id ?? "",
    spaceId: raw.space_id ?? "",
    title: raw.title ?? "",
    contentText: raw.content_text ?? "",
    freshnessStatus: raw.freshness_status ?? "unknown",
    aiCostUsd: raw.ai_cost_usd ?? 0,
    lastVerifiedAt: raw.last_verified_at,
    updatedAt: raw.updated_at ?? "",
  };
}

export class DocsClient {
  constructor(
    private url: string,
    private apiKey: string,
  ) {}

  isConfigured(): boolean {
    return !!this.url && !!this.apiKey;
  }

  baseURL(): string {
    return this.url.replace(/\/$/, "");
  }

  private headers(): Record<string, string> {
    return {
      "Content-Type": "application/json",
      Authorization: `Bearer ${this.apiKey}`,
    };
  }

  // searchDocs returns an empty array when Docs is unconfigured
  // OR the lookup fails. The hover and panel both degrade
  // gracefully — empty results just hide the suggestion.
  async searchDocs(
    workspaceId: string,
    query: string,
    limit = 5,
    signal?: AbortSignal,
  ): Promise<DocsSearchResult[]> {
    if (!this.isConfigured() || !workspaceId || !query.trim()) return [];
    try {
      const res = await fetch(
        `${this.baseURL()}/v1/workspaces/${encodeURIComponent(workspaceId)}/search?q=${encodeURIComponent(query)}&limit=${limit}`,
        { headers: this.headers(), signal },
      );
      if (!res.ok) return [];
      const env = (await res.json()) as RawSearchEnvelope;
      return (env.results ?? []).map(normaliseSearch);
    } catch {
      return [];
    }
  }

  // getPage fetches a single page by space + page id. Returns
  // null on 404 / network failure / unconfigured.
  async getPage(spaceId: string, pageId: string): Promise<DocsPage | null> {
    if (!this.isConfigured() || !spaceId || !pageId) return null;
    try {
      const res = await fetch(
        `${this.baseURL()}/v1/spaces/${encodeURIComponent(spaceId)}/pages/${encodeURIComponent(pageId)}`,
        { headers: this.headers() },
      );
      if (!res.ok) return null;
      const raw = (await res.json()) as RawPage;
      return normalisePage(raw);
    } catch {
      return null;
    }
  }

  // askDocs posts a natural-language question. Returns null
  // when Docs is unconfigured so the UI can fall back to the
  // regular chat path.
  async askDocs(
    workspaceId: string,
    question: string,
  ): Promise<AskResult | null> {
    if (!this.isConfigured() || !workspaceId || !question.trim()) return null;
    try {
      const res = await fetch(
        `${this.baseURL()}/v1/workspaces/${encodeURIComponent(workspaceId)}/ai/ask`,
        {
          method: "POST",
          headers: this.headers(),
          body: JSON.stringify({ question }),
        },
      );
      if (!res.ok) return null;
      const raw = (await res.json()) as { answer?: string; sources?: AskSource[] };
      return {
        answer: raw.answer ?? "",
        sources: raw.sources ?? [],
      };
    } catch {
      return null;
    }
  }
}
