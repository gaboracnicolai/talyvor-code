// Session cost tracking. Lives in-memory for the editor process —
// reset on restart. Per-issue breakdown so the dashboard can show
// "ENG-42: $0.12, ENG-43: $0.06" without a Lens round-trip.

export interface SessionSummary {
  totalCostUSD: number;
  totalTokens: number;
  completionCount: number;
  byIssue: Record<string, number>;
  sessionStart: Date;
}

// haiku is the default model — pricing snapshotted here so we can
// estimate cost without waiting on Lens to report back. Real cost
// reconciliation still happens via Lens analytics; this is the
// "what did I just spend?" indicator the status bar shows.
const HAIKU_INPUT_PER_TOKEN_USD = 0.25 / 1_000_000;
const HAIKU_OUTPUT_PER_TOKEN_USD = 1.25 / 1_000_000;

export function estimateCostUSD(
  inputTokens: number,
  outputTokens: number,
): number {
  return (
    inputTokens * HAIKU_INPUT_PER_TOKEN_USD +
    outputTokens * HAIKU_OUTPUT_PER_TOKEN_USD
  );
}

export class CostTracker {
  private sessionCost = 0;
  private sessionTokens = 0;
  private completionCount = 0;
  private byIssue = new Map<string, number>();
  private startedAt = new Date();

  // recordCompletion is what the provider calls after a successful
  // Lens response. The bucket key is the issue identifier (or the
  // literal "(no issue)" for unattributed calls so the rollup is
  // still inspectable).
  recordCompletion(tokens: number, costUSD: number, issueId: string): void {
    this.sessionCost += costUSD;
    this.sessionTokens += tokens;
    this.completionCount += 1;
    const key = issueId || "(no issue)";
    this.byIssue.set(key, (this.byIssue.get(key) ?? 0) + costUSD);
  }

  getSessionSummary(): SessionSummary {
    const byIssue: Record<string, number> = {};
    for (const [k, v] of this.byIssue) byIssue[k] = v;
    return {
      totalCostUSD: this.sessionCost,
      totalTokens: this.sessionTokens,
      completionCount: this.completionCount,
      byIssue,
      sessionStart: this.startedAt,
    };
  }

  reset(): void {
    this.sessionCost = 0;
    this.sessionTokens = 0;
    this.completionCount = 0;
    this.byIssue.clear();
    this.startedAt = new Date();
  }
}

// formatDuration renders a Date pair as "1h 23m" / "47s". Used by
// the dashboard webview so the "duration" line doesn't pull in a
// date library.
export function formatDuration(start: Date, end: Date = new Date()): string {
  const ms = Math.max(0, end.getTime() - start.getTime());
  const sec = Math.floor(ms / 1000);
  if (sec < 60) return `${sec}s`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m`;
  const h = Math.floor(min / 60);
  const rem = min % 60;
  return rem === 0 ? `${h}h` : `${h}h ${rem}m`;
}
