// Track HTTP client. Talyvor Code only needs three calls today:
// look up an issue by its human identifier (ENG-42), search by
// query, and patch the cost roll-up after a completion. Everything
// else lives in Track's own UI.

export interface TrackIssue {
  id: string;
  identifier: string;
  title: string;
  status: string;
  description: string;
  aiCostUsd: number;
}

interface RawIssue {
  id?: string;
  identifier?: string;
  title?: string;
  status?: string;
  description?: string;
  ai_cost_usd?: number;
}

function normalise(raw: RawIssue): TrackIssue {
  return {
    id: raw.id ?? "",
    identifier: raw.identifier ?? "",
    title: raw.title ?? "",
    status: raw.status ?? "",
    description: raw.description ?? "",
    aiCostUsd: raw.ai_cost_usd ?? 0,
  };
}

export class TrackClient {
  constructor(
    private url: string,
    private apiKey: string,
  ) {}

  isConfigured(): boolean {
    return !!this.url && !!this.apiKey;
  }

  private headers(): Record<string, string> {
    return {
      "Content-Type": "application/json",
      Authorization: `Bearer ${this.apiKey}`,
    };
  }

  // getIssue returns null when Track is unconfigured OR the lookup
  // fails. The IDE flow degrades gracefully — without Track we
  // still cost-attribute via the X-Talyvor-Issue header.
  async getIssue(
    workspaceId: string,
    identifier: string,
  ): Promise<TrackIssue | null> {
    if (!this.isConfigured() || !workspaceId || !identifier) return null;
    try {
      const res = await fetch(
        `${this.url.replace(/\/$/, "")}/v1/workspaces/${encodeURIComponent(workspaceId)}/issues/${encodeURIComponent(identifier)}`,
        { headers: this.headers() },
      );
      if (!res.ok) return null;
      const raw = (await res.json()) as RawIssue;
      return normalise(raw);
    } catch {
      return null;
    }
  }

  async searchIssues(
    workspaceId: string,
    query: string,
  ): Promise<TrackIssue[]> {
    if (!this.isConfigured() || !workspaceId) return [];
    try {
      const res = await fetch(
        `${this.url.replace(/\/$/, "")}/v1/workspaces/${encodeURIComponent(workspaceId)}/issues/search?q=${encodeURIComponent(query)}&limit=10`,
        { headers: this.headers() },
      );
      if (!res.ok) return [];
      const arr = (await res.json()) as RawIssue[];
      return arr.map(normalise);
    } catch {
      return [];
    }
  }

  // updateIssueCost is best-effort. Lens normally writes back to
  // Track on its own; the IDE only calls this when the user
  // explicitly requests a re-sync from the cost dashboard.
  async updateIssueCost(
    workspaceId: string,
    issueId: string,
    costUsd: number,
  ): Promise<void> {
    if (!this.isConfigured() || !workspaceId || !issueId) return;
    try {
      await fetch(
        `${this.url.replace(/\/$/, "")}/v1/workspaces/${encodeURIComponent(workspaceId)}/issues/${encodeURIComponent(issueId)}`,
        {
          method: "PATCH",
          headers: this.headers(),
          body: JSON.stringify({ ai_cost_usd: costUsd }),
        },
      );
    } catch {
      // Best-effort — swallowed.
    }
  }
}
