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
import {
  SSEBuffer,
  extractData,
  parseEvent,
  providerForModel,
  type StreamProvider,
} from "./sse-pure";

// StreamUsage is the token-count payload that arrives with the
// terminal `done` event. Callers attribute cost from this.
export interface StreamUsage {
  inputTokens: number;
  outputTokens: number;
}

export interface StreamCallbacks {
  onChunk: (text: string) => void;
  onDone: (usage: StreamUsage) => void;
  onError: (err: Error) => void;
}

// CompletionResult bundles text + upstream token usage so callers
// (the completion provider) can roll up session cost without a
// second Lens round-trip.
export interface CompletionResult {
  text: string;
  inputTokens: number;
  outputTokens: number;
}

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

  // completeWithUsage is the same call as complete() but returns
  // the upstream token usage so the caller can attribute cost
  // immediately. We expose both signatures to keep simple "give
  // me the text" callers ergonomic.
  async completeWithUsage(
    messages: Message[],
    model: string,
    feature: string,
    workspaceId: string,
    issueId: string,
    maxTokens = 2048,
    signal?: AbortSignal,
  ): Promise<CompletionResult> {
    if (!this.isConfigured()) {
      throw new LensError("Lens is not configured", 0);
    }
    const body = { model, max_tokens: maxTokens, messages };
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
        signal,
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
    return {
      text: (data.content ?? [])
        .filter((c) => c.type === "text")
        .map((c) => c.text)
        .join(""),
      inputTokens: data.usage?.input_tokens ?? 0,
      outputTokens: data.usage?.output_tokens ?? 0,
    };
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

  // completeStream proxies a streaming completion. Each text
  // delta fires `onChunk`; the terminal `done` event fires
  // `onDone` with the accumulated token usage. Errors land on
  // `onError`. When the Lens response isn't real SSE (some
  // proxies ignore stream:true and return JSON) we read the
  // body once and emit it as a single chunk + done, so the
  // caller doesn't have to fall back manually.
  async completeStream(
    messages: Message[],
    model: string,
    feature: string,
    workspaceId: string,
    issueId: string,
    callbacks: StreamCallbacks,
    signal?: AbortSignal,
  ): Promise<void> {
    if (!this.isConfigured()) {
      callbacks.onError(new LensError("Lens is not configured", 0));
      return;
    }
    const provider = providerForModel(model);
    const endpoint = provider === "openai"
      ? `${this.url.replace(/\/$/, "")}/v1/proxy/openai/v1/chat/completions`
      : `${this.url.replace(/\/$/, "")}/v1/proxy/anthropic/v1/messages`;
    const body = provider === "openai"
      ? { model, messages, stream: true }
      : { model, max_tokens: 2048, messages, stream: true };
    let res: Response;
    try {
      res = await fetch(endpoint, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Accept": "text/event-stream",
          "Authorization": `Bearer ${this.apiKey}`,
          "X-Talyvor-Feature": `code-${feature}`,
          "X-Talyvor-Workspace": workspaceId,
          "X-Talyvor-Issue": issueId,
        },
        body: JSON.stringify(body),
        signal,
      });
    } catch (err) {
      callbacks.onError(err instanceof Error ? err : new Error(String(err)));
      return;
    }
    if (!res.ok) {
      const text = await res.text().catch(() => "");
      callbacks.onError(new LensError(`Lens ${res.status}: ${text || res.statusText}`, res.status));
      return;
    }
    const contentType = res.headers.get("content-type") ?? "";
    if (!contentType.toLowerCase().startsWith("text/event-stream")) {
      await consumeNonStream(res, provider, callbacks);
      return;
    }
    await consumeStream(res, provider, callbacks, signal);
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

// consumeStream reads the SSE body, splits on the blank-line
// delimiter, and dispatches each event to the right callback.
// Aborts immediately when the signal fires.
async function consumeStream(
  res: Response,
  provider: StreamProvider,
  cb: StreamCallbacks,
  signal: AbortSignal | undefined,
): Promise<void> {
  if (!res.body) {
    cb.onError(new Error("Lens streaming response has no body"));
    return;
  }
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  const buf = new SSEBuffer();
  let inputTokens = 0;
  let outputTokens = 0;
  try {
    for (;;) {
      if (signal?.aborted) {
        cb.onError(new Error("Lens stream aborted"));
        return;
      }
      const { value, done } = await reader.read();
      if (done) break;
      const events = buf.push(decoder.decode(value, { stream: true }));
      for (const raw of events) {
        const payload = extractData(raw);
        if (!payload) continue;
        const ev = parseEvent(payload, provider);
        if (!ev) continue;
        if (ev.kind === "text" && ev.text) cb.onChunk(ev.text);
        else if (ev.kind === "error") {
          cb.onError(new Error(ev.message ?? "stream error"));
          return;
        } else if (ev.kind === "done") {
          if (ev.inputTokens) inputTokens = ev.inputTokens;
          if (ev.outputTokens) outputTokens = ev.outputTokens;
        }
      }
    }
    // Drain any trailing event not terminated by \n\n.
    for (const raw of buf.flush()) {
      const payload = extractData(raw);
      const ev = parseEvent(payload, provider);
      if (ev?.kind === "text" && ev.text) cb.onChunk(ev.text);
      if (ev?.kind === "done") {
        if (ev.inputTokens) inputTokens = ev.inputTokens;
        if (ev.outputTokens) outputTokens = ev.outputTokens;
      }
    }
    cb.onDone({ inputTokens, outputTokens });
  } catch (err) {
    cb.onError(err instanceof Error ? err : new Error(String(err)));
  }
}

// consumeNonStream is the JSON fallback path — used when the
// Lens response Content-Type isn't text/event-stream. We read
// the body once and emit it as a single chunk + done.
async function consumeNonStream(
  res: Response,
  provider: StreamProvider,
  cb: StreamCallbacks,
): Promise<void> {
  try {
    const data = (await res.json()) as unknown;
    if (provider === "openai") {
      const obj = data as {
        choices?: Array<{ message?: { content?: string } }>;
        usage?: { prompt_tokens?: number; completion_tokens?: number };
      };
      const text = obj.choices?.[0]?.message?.content ?? "";
      if (text) cb.onChunk(text);
      cb.onDone({
        inputTokens: obj.usage?.prompt_tokens ?? 0,
        outputTokens: obj.usage?.completion_tokens ?? 0,
      });
      return;
    }
    const obj = data as CompletionResponse;
    const text = (obj.content ?? [])
      .filter((c) => c.type === "text")
      .map((c) => c.text)
      .join("");
    if (text) cb.onChunk(text);
    cb.onDone({
      inputTokens: obj.usage?.input_tokens ?? 0,
      outputTokens: obj.usage?.output_tokens ?? 0,
    });
  } catch (err) {
    cb.onError(err instanceof Error ? err : new Error(String(err)));
  }
}
