// Pure helpers for the chat panel — kept independent of the vscode
// module so the test runner can exercise them without an Electron
// host. The webview-bound class in ChatPanel.ts consumes these.

import type { Message } from "../lens/types";

// MAX_HISTORY caps the conversation buffer. Once we hit the limit,
// the oldest user/assistant pair is dropped — the system prompt is
// re-injected at send time, so it's never part of the trimmed
// buffer.
export const MAX_HISTORY = 20;

// trimHistory keeps the last MAX_HISTORY messages, preserving the
// oldest pairs as a single drop rather than half-pairs that would
// leave the model with mismatched user/assistant turns.
export function trimHistory(history: Message[], limit = MAX_HISTORY): Message[] {
  if (history.length <= limit) return history.slice();
  // Drop oldest two-at-a-time so we never strand a half-turn.
  const overflow = history.length - limit;
  const dropCount = overflow + (overflow % 2);
  return history.slice(dropCount);
}

// buildSystemPrompt assembles the persistent system message that
// rides on every chat request. We refresh it per-send so the
// active issue stays current after the user runs /issue mid-chat.
// issueContext (when present) is the multi-line Track summary
// from IssueContextProvider — appended verbatim so the model sees
// title, status, and description for grounded answers.
export function buildSystemPrompt(issueID: string, issueContext = ""): string {
  const issueLine = issueID
    ? `The active issue is ${issueID}.`
    : "No active issue is set.";
  const base = [
    "You are an expert coding assistant integrated into VS Code.",
    "You help developers understand, fix, and improve code.",
    "When showing code, use markdown code fences with the language identifier.",
    "Be concise but complete. Skip restating the question.",
    issueLine,
  ].join(" ");
  if (issueContext.trim() === "") return base;
  return `${base}\n\nActive issue context:\n${issueContext}`;
}

// CodeBlock represents one ``` fenced segment in a response. The
// webview renders each with a copy + insert button; the agent's
// REPL renders them as plain text.
export interface CodeBlock {
  language: string;
  code: string;
}

// Segment is either prose text or a fenced code block. splitBlocks
// returns the response as an ordered list of segments so the UI
// can interleave both kinds in the natural reading order.
export type Segment =
  | { kind: "text"; text: string }
  | { kind: "code"; code: CodeBlock };

// splitBlocks parses markdown fences out of a response. We accept
// the standard ``` opener with an optional language tag and the
// matching ``` closer on its own line. Unterminated fences emit
// whatever follows as a code block so the user sees the partial
// answer rather than nothing.
export function splitBlocks(text: string): Segment[] {
  const out: Segment[] = [];
  const lines = text.split("\n");
  let i = 0;
  let buf: string[] = [];
  const flushText = () => {
    if (buf.length === 0) return;
    const joined = buf.join("\n");
    if (joined.trim().length > 0) out.push({ kind: "text", text: joined });
    buf = [];
  };
  while (i < lines.length) {
    const line = lines[i];
    const fence = line.match(/^```([a-zA-Z0-9_+-]*)\s*$/);
    if (!fence) {
      buf.push(line);
      i++;
      continue;
    }
    flushText();
    const language = fence[1] || "";
    i++; // consume opener
    const codeLines: string[] = [];
    while (i < lines.length && !/^```\s*$/.test(lines[i])) {
      codeLines.push(lines[i]);
      i++;
    }
    if (i < lines.length) i++; // consume closer
    out.push({ kind: "code", code: { language, code: codeLines.join("\n") } });
  }
  flushText();
  return out;
}

// escapeHTML is the standard ampersand/angle-bracket/quote escaper.
// Used by both the webview text rendering and the code-block
// renderer.
export function escapeHTML(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}
