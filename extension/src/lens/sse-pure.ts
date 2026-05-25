// Pure helpers for SSE parsing on the extension side. vscode-
// free so the test runner can exercise the line buffer + event
// extraction without spinning up Electron. Mirrors the Go-side
// behaviour in agent/internal/lens (readAnthropicSSE +
// readOpenAISSE).

export type StreamProvider = "anthropic" | "openai";

export interface StreamEvent {
  kind: "text" | "done" | "error";
  text?: string;
  inputTokens?: number;
  outputTokens?: number;
  message?: string;
}

// SSEBuffer accumulates chunks from a ReadableStream and splits
// them on the SSE blank-line delimiter. Each call to `push`
// returns the events finalised by the new bytes (i.e. anything
// terminated by `\n\n`).
export class SSEBuffer {
  private buffer = "";

  push(chunk: string): string[] {
    this.buffer += chunk;
    const out: string[] = [];
    while (true) {
      const idx = this.buffer.indexOf("\n\n");
      if (idx < 0) break;
      const raw = this.buffer.slice(0, idx);
      this.buffer = this.buffer.slice(idx + 2);
      if (raw.trim() === "") continue;
      out.push(raw);
    }
    return out;
  }

  // flush returns any trailing event the stream ended on without
  // the canonical blank-line terminator. Some servers omit it.
  flush(): string[] {
    const tail = this.buffer;
    this.buffer = "";
    return tail.trim() ? [tail] : [];
  }
}

// extractData pulls the `data:` payload from a raw SSE event
// block (which may contain multiple lines like `event: foo`
// followed by `data: …`). Returns "" when no data line exists.
export function extractData(raw: string): string {
  const lines = raw.split(/\r?\n/);
  for (const line of lines) {
    if (line.startsWith("data:")) {
      return line.slice(5).trim();
    }
  }
  return "";
}

// parseEvent maps one SSE payload to a StreamEvent for the
// supplied provider. `[DONE]` always yields {kind: "done"}.
// Malformed JSON returns undefined so callers can skip.
export function parseEvent(payload: string, provider: StreamProvider): StreamEvent | undefined {
  if (payload === "[DONE]") return { kind: "done" };
  let obj: Record<string, unknown>;
  try {
    obj = JSON.parse(payload) as Record<string, unknown>;
  } catch {
    return undefined;
  }
  if (provider === "anthropic") return parseAnthropic(obj);
  return parseOpenAI(obj);
}

function parseAnthropic(obj: Record<string, unknown>): StreamEvent | undefined {
  const type = typeof obj.type === "string" ? obj.type : "";
  switch (type) {
    case "content_block_delta": {
      const delta = obj.delta as { text?: string } | undefined;
      const text = delta?.text ?? "";
      if (text) return { kind: "text", text };
      return undefined;
    }
    case "message_start": {
      const msg = obj.message as { usage?: { input_tokens?: number } } | undefined;
      const inputTokens = msg?.usage?.input_tokens ?? 0;
      if (inputTokens > 0) return { kind: "done", inputTokens };
      return undefined;
    }
    case "message_delta": {
      const usage = obj.usage as { output_tokens?: number } | undefined;
      const outputTokens = usage?.output_tokens ?? 0;
      if (outputTokens > 0) return { kind: "done", outputTokens };
      return undefined;
    }
    case "message_stop":
      return { kind: "done" };
    case "error": {
      const err = obj.error as { message?: string } | undefined;
      return { kind: "error", message: err?.message ?? "stream error" };
    }
  }
  return undefined;
}

function parseOpenAI(obj: Record<string, unknown>): StreamEvent | undefined {
  if (obj.error) {
    const err = obj.error as { message?: string };
    return { kind: "error", message: err.message ?? "stream error" };
  }
  const choices = Array.isArray(obj.choices) ? (obj.choices as Array<Record<string, unknown>>) : [];
  for (const ch of choices) {
    const delta = ch.delta as { content?: string } | undefined;
    if (delta?.content) {
      return { kind: "text", text: delta.content };
    }
  }
  const usage = obj.usage as { prompt_tokens?: number; completion_tokens?: number } | undefined;
  if (usage) {
    return {
      kind: "done",
      inputTokens: usage.prompt_tokens ?? 0,
      outputTokens: usage.completion_tokens ?? 0,
    };
  }
  return undefined;
}

// providerForModel mirrors agent/internal/lens.isOpenAIModel so
// the IDE picks the same endpoint the CLI would.
export function providerForModel(model: string): StreamProvider {
  const m = (model ?? "").trim().toLowerCase();
  if (m.startsWith("gpt-") || m.startsWith("o1") || m.startsWith("o3")) return "openai";
  return "anthropic";
}
