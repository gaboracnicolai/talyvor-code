"use strict";
// Lens HTTP client. Every AI call carries an X-Talyvor-Issue
// header — that's the contract that lets Lens (and downstream
// Track) attribute spend to a specific issue. If the active issue
// is empty, the header is still sent (empty string) so a cost
// roll-up by header value naturally buckets unattributed calls.
Object.defineProperty(exports, "__esModule", { value: true });
exports.LensClient = void 0;
const types_1 = require("./types");
class LensClient {
    url;
    apiKey;
    constructor(url, apiKey) {
        this.url = url;
        this.apiKey = apiKey;
    }
    isConfigured() {
        return !!this.url && !!this.apiKey;
    }
    // complete proxies a chat completion through Lens. `feature`
    // becomes the X-Talyvor-Feature tag — we prefix with "code-" so
    // Lens dashboards can break down spend per IDE affordance
    // (code-completion, code-chat, code-explain, ...).
    async complete(messages, model, feature, workspaceId, issueId) {
        if (!this.isConfigured()) {
            throw new types_1.LensError("Lens is not configured", 0);
        }
        const body = {
            model,
            max_tokens: 2048,
            messages,
        };
        const res = await fetch(`${this.url.replace(/\/$/, "")}/v1/proxy/anthropic/v1/messages`, {
            method: "POST",
            headers: {
                "Content-Type": "application/json",
                Authorization: `Bearer ${this.apiKey}`,
                "X-Talyvor-Feature": `code-${feature}`,
                "X-Talyvor-Workspace": workspaceId,
                "X-Talyvor-Issue": issueId,
            },
            body: JSON.stringify(body),
        });
        if (!res.ok) {
            const text = await res.text().catch(() => "");
            throw new types_1.LensError(`Lens ${res.status}: ${text || res.statusText}`, res.status);
        }
        const data = (await res.json());
        return (data.content ?? [])
            .filter((c) => c.type === "text")
            .map((c) => c.text)
            .join("");
    }
    // getStatus probes /healthz so the "Test Connection" command
    // can give a fast yes/no without paying for a real inference
    // round-trip.
    async getStatus() {
        try {
            const res = await fetch(`${this.url.replace(/\/$/, "")}/healthz`);
            if (!res.ok)
                return { available: false, version: "unknown" };
            const data = (await res.json().catch(() => ({})));
            return { available: true, version: data.version ?? "unknown" };
        }
        catch {
            return { available: false, version: "unknown" };
        }
    }
    // getCostForIssue pulls the cumulative AI spend tied to the
    // active issue from Lens analytics. Used by the cost-dashboard
    // command. Returns a zeroed CostInfo when Lens is unconfigured
    // or the lookup fails — the UI shows that gracefully as "$0.00".
    async getCostForIssue(workspaceId, issueId) {
        const zero = {
            issueId,
            totalCostUSD: 0,
            tokens: 0,
            lastUpdated: new Date(),
        };
        if (!this.isConfigured() || !workspaceId || !issueId)
            return zero;
        try {
            const res = await fetch(`${this.url.replace(/\/$/, "")}/v1/workspaces/${encodeURIComponent(workspaceId)}/analytics/ai-costs?issue=${encodeURIComponent(issueId)}`, {
                headers: {
                    Authorization: `Bearer ${this.apiKey}`,
                },
            });
            if (!res.ok)
                return zero;
            const data = (await res.json());
            return {
                issueId,
                totalCostUSD: data.total_cost_usd ?? 0,
                tokens: data.tokens ?? 0,
                lastUpdated: new Date(),
            };
        }
        catch {
            return zero;
        }
    }
}
exports.LensClient = LensClient;
//# sourceMappingURL=client.js.map