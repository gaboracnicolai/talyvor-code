"use strict";
// Track HTTP client. Talyvor Code only needs three calls today:
// look up an issue by its human identifier (ENG-42), search by
// query, and patch the cost roll-up after a completion. Everything
// else lives in Track's own UI.
Object.defineProperty(exports, "__esModule", { value: true });
exports.TrackClient = void 0;
function normalise(raw) {
    return {
        id: raw.id ?? "",
        identifier: raw.identifier ?? "",
        title: raw.title ?? "",
        status: raw.status ?? "",
        description: raw.description ?? "",
        aiCostUsd: raw.ai_cost_usd ?? 0,
    };
}
class TrackClient {
    url;
    apiKey;
    constructor(url, apiKey) {
        this.url = url;
        this.apiKey = apiKey;
    }
    isConfigured() {
        return !!this.url && !!this.apiKey;
    }
    headers() {
        return {
            "Content-Type": "application/json",
            Authorization: `Bearer ${this.apiKey}`,
        };
    }
    // getIssue returns null when Track is unconfigured OR the lookup
    // fails. The IDE flow degrades gracefully — without Track we
    // still cost-attribute via the X-Talyvor-Issue header.
    async getIssue(workspaceId, identifier) {
        if (!this.isConfigured() || !workspaceId || !identifier)
            return null;
        try {
            const res = await fetch(`${this.url.replace(/\/$/, "")}/v1/workspaces/${encodeURIComponent(workspaceId)}/issues/${encodeURIComponent(identifier)}`, { headers: this.headers() });
            if (!res.ok)
                return null;
            const raw = (await res.json());
            return normalise(raw);
        }
        catch {
            return null;
        }
    }
    async searchIssues(workspaceId, query) {
        if (!this.isConfigured() || !workspaceId)
            return [];
        try {
            const res = await fetch(`${this.url.replace(/\/$/, "")}/v1/workspaces/${encodeURIComponent(workspaceId)}/issues/search?q=${encodeURIComponent(query)}&limit=10`, { headers: this.headers() });
            if (!res.ok)
                return [];
            const arr = (await res.json());
            return arr.map(normalise);
        }
        catch {
            return [];
        }
    }
    // updateIssueCost is best-effort. Lens normally writes back to
    // Track on its own; the IDE only calls this when the user
    // explicitly requests a re-sync from the cost dashboard.
    async updateIssueCost(workspaceId, issueId, costUsd) {
        if (!this.isConfigured() || !workspaceId || !issueId)
            return;
        try {
            await fetch(`${this.url.replace(/\/$/, "")}/v1/workspaces/${encodeURIComponent(workspaceId)}/issues/${encodeURIComponent(issueId)}`, {
                method: "PATCH",
                headers: this.headers(),
                body: JSON.stringify({ ai_cost_usd: costUsd }),
            });
        }
        catch {
            // Best-effort — swallowed.
        }
    }
}
exports.TrackClient = TrackClient;
//# sourceMappingURL=client.js.map