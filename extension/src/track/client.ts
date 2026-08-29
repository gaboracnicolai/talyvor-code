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
  //
  // ⚠ IT ASKS TWO ROUTES BECAUSE TRACK HAS TWO AND THEY ARE DISJOINT. `ref` is what the IDE
  // supplies, and issue-context.ts validates it as a HUMAN identifier (isValidIssueIdentifier,
  // "e.g. ENG-42") before calling here. Track serves `/issues/{id}` off the uuid primary key and
  // `/issues/by-identifier/{identifier}` off `WHERE identifier = $1 AND workspace_id = $2`;
  // neither answers for the other's input. Before this, only the first was ever asked, so every
  // ENG-42 lookup missed an issue that exists — and setActiveIssue then fabricated a SYNTHETIC
  // issue titled with the key itself, so the miss arrived looking like a hit. talyvor-track added
  // by-identifier for exactly this and its handler says so: "the CLI agent's Track client put an
  // identifier in the {id} slot below and got 404 for issues that exist — measured on the wire,
  // talyvor-code #57".
  //
  // ⚠ `{id}` IS TRIED FIRST, exactly as before, and by-identifier only after a miss — so every
  // input that resolved before resolves the same way, through the same request. This can only
  // turn misses into hits, and needs no decision about which route wins.
  async getIssue(
    workspaceId: string,
    ref: string,
  ): Promise<TrackIssue | null> {
    if (!this.isConfigured() || !workspaceId || !ref) return null;
    const base = `${this.url.replace(/\/$/, "")}/v1/workspaces/${encodeURIComponent(workspaceId)}/issues/`;
    return (
      (await this.getIssueAt(`${base}${encodeURIComponent(ref)}`)) ??
      (await this.getIssueAt(`${base}by-identifier/${encodeURIComponent(ref)}`))
    );
  }

  private async getIssueAt(endpoint: string): Promise<TrackIssue | null> {
    try {
      const res = await fetch(endpoint, { headers: this.headers() });
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

  // ⚠ updateIssueCost WAS HERE AND IS DELETED ON PURPOSE — do not reintroduce it.
  //
  // It PATCHed an ABSOLUTE total (issue.aiCostUsd + delta) computed from a LOCAL estimate, racing
  // Lens's own exactly-once syncer for the same column. Both wrote issues.ai_cost_usd; last writer
  // won; the `catch {}` beneath it made every loss silent.
  //
  // Lens now records the issue itself: request_attribution gained request_id (Lens migration 0116)
  // and /v1/api/spend/by-request emits issue_id, and Track's syncer prefers that issue over the
  // feature (Track a962b0c). Attribution arrives server-side from the authoritative cost, so a
  // second client-side writer is not a redundancy — it is a double-count with a worse number.

  // addComment posts a comment to a Track issue. Used by the
  // agent flow to leave "agent task completed" notes alongside
  // human discussion so the audit trail of automated changes is
  // visible inside Track.
  // ⚠ THE FIELD IS `body`, NOT `content`, AND THIS CALL HAD NEVER ONCE SUCCEEDED.
  //
  // Track decodes this route into `model.Comment`, whose json tags are id / issue_id / author_id /
  // body / edited_at / created_at / updated_at — there is no `content`. And Track's
  // `internal/httpx.DecodeJSON` calls `dec.DisallowUnknownFields()`, so an undeclared field is a
  // HARD 400 BAD_JSON before a line of handler logic runs. Measured by executing the stdlib
  // decoder against a verbatim transcription of that struct at talyvor-track 7ad05ce3:
  //
  //     {"content":"…","author_id":"talyvor-code"}  ->  json: unknown field "content"
  //     {"body":"…","author_id":"talyvor-code"}     ->  <nil>
  //
  // So every "agent task completed" note this extension has ever tried to leave was refused, and
  // the audit trail the comment below describes has never existed. ⚠ IT WAS DEAD TWICE OVER: even
  // if `content` had decoded, `Comment.Body` would have been empty and the comment blank.
  //
  // ⚠⚠ AND THE REASON IT SURVIVED IS TWO LINES DOWN, NOT IN THE SPELLING: the response was never
  // examined. `await fetch(...)` with no `res.ok` check and a bare `catch {}` cannot distinguish a
  // permanent 400 from success, so a request that failed on every call for the life of the feature
  // produced exactly the same trace as one that worked. It returns a boolean now — still
  // best-effort at the caller, but the failure is at least REPRESENTABLE.
  //
  // `author_id` is fine and stays: Track declares it on model.Comment and then OVERWRITES it with
  // the verified session member (SEC-5), so it is accepted and ignored rather than refused.
  async addComment(
    workspaceId: string,
    issueId: string,
    content: string,
  ): Promise<boolean> {
    if (!this.isConfigured() || !workspaceId || !issueId) return false;
    try {
      const res = await fetch(
        `${this.url.replace(/\/$/, "")}/v1/workspaces/${encodeURIComponent(workspaceId)}/issues/${encodeURIComponent(issueId)}/comments`,
        {
          method: "POST",
          headers: this.headers(),
          body: JSON.stringify({ body: content, author_id: "talyvor-code" }),
        },
      );
      return res.ok;
    } catch {
      // Best-effort — swallowed. The boolean is the only signal.
      return false;
    }
  }

  // listIssues returns recent issues for the workspace. Used to
  // seed the QuickPick when the user hasn't typed enough chars to
  // search yet.
  async listIssues(
    workspaceId: string,
    limit = 25,
  ): Promise<TrackIssue[]> {
    if (!this.isConfigured() || !workspaceId) return [];
    try {
      const res = await fetch(
        `${this.url.replace(/\/$/, "")}/v1/workspaces/${encodeURIComponent(workspaceId)}/issues?limit=${limit}`,
        { headers: this.headers() },
      );
      if (!res.ok) return [];
      const arr = (await res.json()) as RawIssue[];
      return arr.map(normalise);
    } catch {
      return [];
    }
  }
}
