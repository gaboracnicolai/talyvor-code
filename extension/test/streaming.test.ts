// Smoke tests for the SSE pure helpers + provider mapping.
// Validates the buffer's blank-line splitting, the per-format
// event parsers, and the provider router. Mirrors the Go-side
// coverage in agent/internal/lens/client_test.go.

import {
  SSEBuffer,
  extractData,
  parseEvent,
  providerForModel,
} from "../src/lens/sse-pure";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

// ─── SSEBuffer ────────────────────────────────────

function testBufferSplitsOnBlankLine(): void {
  const buf = new SSEBuffer();
  // One event delivered across two chunks.
  const a = buf.push("data: hello\n");
  assert(a.length === 0, "no event yet");
  const b = buf.push("\ndata: world\n\n");
  assert(b.length === 2, "two events: " + JSON.stringify(b));
  assert(b[0].includes("hello"), "first event");
  assert(b[1].includes("world"), "second event");
}

function testBufferIgnoresEmptyBlocks(): void {
  const buf = new SSEBuffer();
  const events = buf.push("\n\n\n\n");
  assert(events.length === 0, "all-blank input yields no events");
}

function testBufferFlushesTrailingEvent(): void {
  const buf = new SSEBuffer();
  buf.push("data: tail");
  const tail = buf.flush();
  assert(tail.length === 1 && tail[0].includes("tail"), "trailing event flushed");
}

// ─── extractData ──────────────────────────────────

function testExtractData(): void {
  assert(extractData("data: hello") === "hello", "basic");
  assert(extractData("event: ping\ndata: payload") === "payload", "multi-line");
  assert(extractData("comment only") === "", "no data line");
}

// ─── Anthropic events ─────────────────────────────

function testParseAnthropicTextDelta(): void {
  const ev = parseEvent(
    JSON.stringify({ type: "content_block_delta", delta: { type: "text_delta", text: "Hello" } }),
    "anthropic",
  );
  assert(ev?.kind === "text" && ev.text === "Hello", "text delta");
}

function testParseAnthropicMessageStartUsage(): void {
  const ev = parseEvent(
    JSON.stringify({ type: "message_start", message: { usage: { input_tokens: 120 } } }),
    "anthropic",
  );
  assert(ev?.kind === "done" && ev.inputTokens === 120, "input tokens captured");
}

function testParseAnthropicMessageDeltaUsage(): void {
  const ev = parseEvent(
    JSON.stringify({ type: "message_delta", usage: { output_tokens: 42 } }),
    "anthropic",
  );
  assert(ev?.kind === "done" && ev.outputTokens === 42, "output tokens captured");
}

function testParseAnthropicErrorEvent(): void {
  const ev = parseEvent(
    JSON.stringify({ type: "error", error: { message: "upstream exploded" } }),
    "anthropic",
  );
  assert(ev?.kind === "error" && (ev.message ?? "").includes("exploded"), "error surfaced");
}

function testParseDoneSentinel(): void {
  const ev = parseEvent("[DONE]", "anthropic");
  assert(ev?.kind === "done", "[DONE] → done");
}

function testParseInvalidJSONReturnsUndefined(): void {
  assert(parseEvent("not json", "anthropic") === undefined, "invalid → undefined");
}

// ─── OpenAI events ────────────────────────────────

function testParseOpenAITextDelta(): void {
  const ev = parseEvent(
    JSON.stringify({ choices: [{ delta: { content: "Hello" } }] }),
    "openai",
  );
  assert(ev?.kind === "text" && ev.text === "Hello", "openai text");
}

function testParseOpenAIUsageFinal(): void {
  const ev = parseEvent(
    JSON.stringify({
      choices: [{ delta: {}, finish_reason: "stop" }],
      usage: { prompt_tokens: 10, completion_tokens: 5 },
    }),
    "openai",
  );
  assert(
    ev?.kind === "done" && ev.inputTokens === 10 && ev.outputTokens === 5,
    "usage captured",
  );
}

function testParseOpenAIError(): void {
  const ev = parseEvent(
    JSON.stringify({ error: { message: "rate limited" } }),
    "openai",
  );
  assert(ev?.kind === "error" && (ev.message ?? "").includes("rate"), "openai error");
}

// ─── providerForModel ─────────────────────────────

function testProviderMapping(): void {
  assert(providerForModel("gpt-4o") === "openai", "gpt-4o");
  assert(providerForModel("gpt-4o-mini") === "openai", "gpt-4o-mini");
  assert(providerForModel("o1") === "openai", "o1");
  assert(providerForModel("o3-mini") === "openai", "o3-mini");
  assert(providerForModel("claude-sonnet-4-6") === "anthropic", "claude");
  assert(providerForModel("mistral-large") === "anthropic", "mistral");
  assert(providerForModel("") === "anthropic", "empty → anthropic");
}

// ─── Streaming accumulation ───────────────────────

function testAccumulatesTextAcrossChunks(): void {
  // Simulate a typical streamed reply: deltas + final usage.
  const buf = new SSEBuffer();
  const raw = [
    "data: " + JSON.stringify({ type: "message_start", message: { usage: { input_tokens: 5 } } }),
    "",
    "data: " + JSON.stringify({ type: "content_block_delta", delta: { text: "Hello " } }),
    "",
    "data: " + JSON.stringify({ type: "content_block_delta", delta: { text: "world" } }),
    "",
    "data: " + JSON.stringify({ type: "message_delta", usage: { output_tokens: 2 } }),
    "",
    "data: [DONE]",
    "",
  ].join("\n");
  const events = buf.push(raw);
  let text = "";
  let inputTokens = 0;
  let outputTokens = 0;
  let done = false;
  for (const raw of events) {
    const payload = extractData(raw);
    const ev = parseEvent(payload, "anthropic");
    if (!ev) continue;
    if (ev.kind === "text" && ev.text) text += ev.text;
    if (ev.kind === "done") {
      done = true;
      if (ev.inputTokens) inputTokens = ev.inputTokens;
      if (ev.outputTokens) outputTokens = ev.outputTokens;
    }
  }
  assert(text === "Hello world", "accumulated text: " + text);
  assert(done, "done seen");
  assert(inputTokens === 5 && outputTokens === 2, `usage ${inputTokens}/${outputTokens}`);
}

async function main(): Promise<void> {
  testBufferSplitsOnBlankLine();
  testBufferIgnoresEmptyBlocks();
  testBufferFlushesTrailingEvent();
  testExtractData();
  testParseAnthropicTextDelta();
  testParseAnthropicMessageStartUsage();
  testParseAnthropicMessageDeltaUsage();
  testParseAnthropicErrorEvent();
  testParseDoneSentinel();
  testParseInvalidJSONReturnsUndefined();
  testParseOpenAITextDelta();
  testParseOpenAIUsageFinal();
  testParseOpenAIError();
  testProviderMapping();
  testAccumulatesTextAcrossChunks();
  // eslint-disable-next-line no-console
  console.log("ok (15 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
