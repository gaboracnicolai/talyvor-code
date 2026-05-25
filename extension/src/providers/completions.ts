// TalyvorCompletionProvider — VS Code InlineCompletionItemProvider
// backed by Lens. Three operational constraints baked in:
//   1. Completions never throw — return null so the editor keeps
//      typing if Lens is down.
//   2. Debounce 300ms after the last keystroke so a rapid typist
//      doesn't trigger a request per character.
//   3. Cap response at 200 tokens — ghost text past that gets
//      ignored anyway, and short responses keep latency low.

import * as vscode from "vscode";
import type { LensClient } from "../lens/client";
import type { TalyvorConfig } from "../config";
import { CostTracker, estimateCostUSD } from "./cost-tracker";
import type { IssueContextProvider } from "../track/issue-context";
import {
  buildCompletionPrompt,
  getCodeContext,
  isCompletionTrigger,
} from "./context";

// Config supplier — we inject a function rather than the config
// object so the provider always picks up the latest values without
// being rebuilt when the user toggles a setting.
export type ConfigSupplier = () => ReturnType<typeof TalyvorConfig.getLensConfig>;

const SYSTEM_PROMPT =
  "You are a code completion assistant. Complete the code at the [CURSOR] " +
  "marker. Return ONLY the text to insert, with no explanation, no markdown, " +
  "and no code fences. If completion isn't appropriate, return an empty string.";

const DEBOUNCE_MS = 300;
const MAX_TOKENS = 200;

export class TalyvorCompletionProvider
  implements vscode.InlineCompletionItemProvider
{
  // Each completion request gets a debounce timer + an AbortController
  // so a new keystroke can cancel the in-flight HTTP call before its
  // body lands. Without cancellation we'd waste Lens calls on
  // already-stale positions.
  private pendingTimer: ReturnType<typeof setTimeout> | null = null;
  private pendingAbort: AbortController | null = null;
  private onUpdate?: () => void;

  constructor(
    private lens: LensClient,
    private config: ConfigSupplier,
    private tracker: CostTracker,
    private issueContext?: IssueContextProvider,
  ) {}

  // setOnUpdate lets the host (extension.ts) refresh the status bar
  // after each successful completion without us holding a reference
  // to vscode.StatusBarItem directly.
  setOnUpdate(fn: () => void): void {
    this.onUpdate = fn;
  }

  async provideInlineCompletionItems(
    document: vscode.TextDocument,
    position: vscode.Position,
    _context: vscode.InlineCompletionContext,
    token: vscode.CancellationToken,
  ): Promise<vscode.InlineCompletionList | null> {
    const cfg = this.config();
    if (!cfg.enableCompletions) return null;
    if (!this.lens.isConfigured()) return null;

    const lineText = document.lineAt(position.line).text;
    if (!isCompletionTrigger(lineText, position.character, document.lineCount)) {
      return null;
    }

    try {
      const text = await this.requestWithDebounce(document, position, token);
      if (!text) return null;
      return {
        items: [
          {
            insertText: text,
            range: new vscode.Range(position, position),
          },
        ],
      };
    } catch {
      // Iron-clad: never bubble. The editor's input loop must not
      // see exceptions from us.
      return null;
    }
  }

  // requestWithDebounce is the timer + cancellation machinery. Each
  // call clears the previous timer and aborts the previous fetch,
  // then schedules a fresh attempt 300ms out. The cancellation
  // token from VS Code (fires on next keystroke) also aborts.
  private requestWithDebounce(
    document: vscode.TextDocument,
    position: vscode.Position,
    token: vscode.CancellationToken,
  ): Promise<string> {
    if (this.pendingTimer) clearTimeout(this.pendingTimer);
    this.pendingAbort?.abort();
    const abort = new AbortController();
    this.pendingAbort = abort;
    token.onCancellationRequested(() => abort.abort());

    return new Promise((resolve) => {
      this.pendingTimer = setTimeout(async () => {
        if (abort.signal.aborted) return resolve("");
        try {
          const text = await this.fetchCompletion(document, position, abort.signal);
          resolve(text);
        } catch {
          resolve("");
        }
      }, DEBOUNCE_MS);
    });
  }

  // fetchCompletion is the actual Lens call. The result's token
  // usage feeds the CostTracker so the status bar can show a
  // running session total without polling analytics.
  private async fetchCompletion(
    document: vscode.TextDocument,
    position: vscode.Position,
    signal: AbortSignal,
  ): Promise<string> {
    const cfg = this.config();
    const ctx = getCodeContext(document, position);
    const userPrompt = buildCompletionPrompt(ctx);
    const issueCtx = this.issueContext?.getIssueContext() ?? "";
    const systemPrompt = issueCtx
      ? `${SYSTEM_PROMPT}\n\nActive issue context:\n${issueCtx}`
      : SYSTEM_PROMPT;

    const res = await this.lens.completeWithUsage(
      [
        { role: "user", content: `${systemPrompt}\n\n${userPrompt}` },
      ],
      cfg.model || "claude-haiku-4-6",
      "completion",
      cfg.workspaceId,
      cfg.activeIssue,
      MAX_TOKENS,
      signal,
    );
    const cost = estimateCostUSD(res.inputTokens, res.outputTokens);
    this.tracker.recordCompletion(
      res.inputTokens + res.outputTokens,
      cost,
      cfg.activeIssue,
      "completion",
    );
    this.onUpdate?.();
    return sanitiseCompletion(res.text);
  }
}

// sanitiseCompletion strips the common over-eager additions models
// produce when asked for "raw code": triple-backtick fences,
// trailing language hints, leading newlines. We deliberately
// preserve internal whitespace so multi-line completions still
// indent correctly.
function sanitiseCompletion(text: string): string {
  let out = text;
  // Drop a leading code fence + optional language tag.
  const fenceStart = out.match(/^\s*```[a-z0-9_-]*\n/i);
  if (fenceStart) out = out.substring(fenceStart[0].length);
  // Drop a trailing closing fence.
  const fenceEnd = out.match(/\n```\s*$/);
  if (fenceEnd) out = out.substring(0, out.length - fenceEnd[0].length);
  // Trim leading newline only — preserves indentation on
  // multi-line continuations.
  if (out.startsWith("\n")) out = out.substring(1);
  return out;
}

// Exported for the smoke test runner.
export const __testing = { sanitiseCompletion };
