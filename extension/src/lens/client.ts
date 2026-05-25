// Lens HTTP client. Every AI call carries an X-Talyvor-Issue
// header — that's the contract that lets Lens (and downstream
// Track) attribute spend to a specific issue. If the active issue
// is empty, the header is still sent (empty string) so a cost
// roll-up by header value naturally buckets unattributed calls.

import {
  CompletionResponse,
  CostInfo,
  LensError,
  Message,
} from "./types";

export class LensClient {
  constructor(
    private url: string,
    private apiKey: string,
  ) {}

  isConfigured(): boolean {
    return !!this.url && !!this.apiKey;
  }

  // complete proxies a chat completion through Lens. `feature`
  // becomes the X-Talyvor-Feature tag — we prefix with "code-" so
  // Lens dashboards can break down spend per IDE affordance
  // (code-completion, code-chat, code-explain, ...).
  async complete(
    messages: Message[],
    model: string,
    feature: string,
    workspaceId: string,
    issueId: string,
  ): Promise<string> {
    if (!this.isConfigured()) {
      throw new LensError("Lens is not configured", 0);
    }
    const body = {
      model,
      max_tokens: 2048,
      messages,
    };
    const res = await fetch(
      `${this.url.replace(/\/$/, "")}/v1/proxy/anthropic/v1/messages`,
      {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${this.apiKey}`,
          "X-Talyvor-Feature": `code-${feature}`,
          "X-Talyvor-Workspace": workspaceId,
          "X-Talyvor-Issue": issueId,
        },
        body: JSON.stringify(body),
      },
    );
    if (!res.ok) {
      const text = await res.text().catch(() => "");
      throw new LensError(
        `Lens ${res.status}: ${text || res.statusText}`,
        res.status,
      );
    }
    const data = (await res.json()) as CompletionResponse;
    return (data.content ?? [])
      .filter((c) => c.type === "text")
      .map((c) => c.text)
      .join("");
  }

  // getStatus probes /healthz so the "Test Connection" command
  // can give a fast yes/no without paying for a real inference
  // round-trip.
  async getStatus(): Promise<{ available: boolean; version: string }> {
    try {
      const res = await fetch(`${this.url.replace(/\/$/, "")}/healthz`);
      if (!res.ok) return { available: false, version: "unknown" };
      const data = (await res.json().catch(() => ({}))) as {
        version?: string;
      };
      return { available: true, version: data.version ?? "unknown" };
    } catch {
      return { available: false, version: "unknown" };
    }
  }

  // getCostForIssue pulls the cumulative AI spend tied to the
  // active issue from Lens analytics. Used by the cost-dashboard
  // command. Returns a zeroed CostInfo when Lens is unconfigured
  // or the lookup fails — the UI shows that gracefully as "$0.00".
  async getCostForIssue(
    workspaceId: string,
    issueId: string,
  ): Promise<CostInfo> {
    const zero: CostInfo = {
      issueId,
      totalCostUSD: 0,
      tokens: 0,
      lastUpdated: new Date(),
    };
    if (!this.isConfigured() || !workspaceId || !issueId) return zero;
    try {
      const res = await fetch(
        `${this.url.replace(/\/$/, "")}/v1/workspaces/${encodeURIComponent(workspaceId)}/analytics/ai-costs?issue=${encodeURIComponent(issueId)}`,
        {
          headers: {
            Authorization: `Bearer ${this.apiKey}`,
          },
        },
      );
      if (!res.ok) return zero;
      const data = (await res.json()) as {
        total_cost_usd?: number;
        tokens?: number;
      };
      return {
        issueId,
        totalCostUSD: data.total_cost_usd ?? 0,
        tokens: data.tokens ?? 0,
        lastUpdated: new Date(),
      };
    } catch {
      return zero;
    }
  }
}
