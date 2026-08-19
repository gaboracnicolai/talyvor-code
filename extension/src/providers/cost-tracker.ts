// Session cost tracking. Lives in-memory for the editor process —
// reset on restart. Per-issue AND per-feature breakdowns so the
// dashboard can show "ENG-42: $0.12 (completions $0.08, tests
// $0.04)" without a Lens round-trip.

export interface SessionSummary {
  totalCostUSD: number;
  totalTokens: number;
  completionCount: number;
  byIssue: Record<string, number>;
  byFeature: Record<string, FeatureUsage>;
  sessionStart: Date;
}

export interface FeatureUsage {
  costUSD: number;
  calls: number;
  tokens: number;
}

export interface RecordEvent {
  issueId: string;
  costUSD: number;
  tokens: number;
  feature: string;
}

// haiku is the default model — pricing snapshotted here so we can
// estimate cost without waiting on Lens to report back. Real cost
// reconciliation still happens via Lens analytics; this is the
// "what did I just spend?" indicator the status bar shows.
// ⚠ ONE PRICE FOR EVERY MODEL, AND THAT IS WHY THE OUTPUT MUST BE LABELLED. ⚠ AND THE PRICE IS NOT
// THE DEFAULT MODEL'S: $0.25/$1.25 per 1M is CLAUDE 3 HAIKU's rate, two generations old. Measured
// against talyvor-lens's price catalog (`internal/catalog/seed.go`, the table the proxy bills from),
// the figure this produces is low by:
//     4x on claude-haiku-4-5   ← THE MODEL package.json DEFAULTS TO, so the bar is 4x low as shipped
//     8x on claude-sonnet-5
//     20x on claude-opus-5
//     40x on claude-fable-5
// ⚠ ONE CLAIM PER LINE IS NOT COSMETIC: the guard pairs each factor with the nearest model id on its
// OWN line, so a factor that drifts away from the model it describes is a failure, not a wrap.
// ⚠ THE FACTORS ARE STATED HERE ONCE AND NOWHERE ELSE: three
// copies of this paragraph is three numbers that can disagree, which is how two of them came to be
// false. `track/cost-claim.test.ts` derives every factor from the catalog and reds if any comment in
// this extension states a different one. ⚠ THE RATE ITSELF IS DELIBERATELY UNCHANGED — it is a money
// figure a user is shown, and queue item W4.11 states the three options. This function survives ONLY to
// drive the status-bar indicator ("what did I just spend?"), which now renders it as `~$x.xx` and
// says in its tooltip that it is a local estimate and not the bill. It no longer feeds any WRITE:
// the client-side PATCH to Track that used it is deleted, because Lens records the real
// per-request cost server-side and Track credits the issue from it.
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
  private byFeature = new Map<string, FeatureUsage>();
  private startedAt = new Date();
  private listeners: Array<(e: RecordEvent) => void> = [];

  // recordCompletion is what the provider calls after a successful
  // Lens response. The bucket key is the issue identifier (or the
  // literal "(no issue)" for unattributed calls so the rollup is
  // still inspectable). `feature` tags the call kind (completion,
  // chat, test-generation, agent-plan, agent-execute) so the
  // dashboard can break down spend by purpose.
  recordCompletion(
    tokens: number,
    costUSD: number,
    issueId: string,
    feature = "other",
  ): void {
    this.sessionCost += costUSD;
    this.sessionTokens += tokens;
    this.completionCount += 1;
    const issueKey = issueId || "(no issue)";
    this.byIssue.set(issueKey, (this.byIssue.get(issueKey) ?? 0) + costUSD);
    const fk = feature || "other";
    const cur = this.byFeature.get(fk) ?? { costUSD: 0, calls: 0, tokens: 0 };
    cur.costUSD += costUSD;
    cur.calls += 1;
    cur.tokens += tokens;
    this.byFeature.set(fk, cur);
    const event: RecordEvent = { issueId: issueKey, costUSD, tokens, feature: fk };
    for (const l of this.listeners) {
      try {
        l(event);
      } catch {
        // listeners are best-effort
      }
    }
  }

  onRecord(fn: (e: RecordEvent) => void): { dispose: () => void } {
    this.listeners.push(fn);
    return {
      dispose: () => {
        this.listeners = this.listeners.filter((x) => x !== fn);
      },
    };
  }

  getSessionSummary(): SessionSummary {
    const byIssue: Record<string, number> = {};
    for (const [k, v] of this.byIssue) byIssue[k] = v;
    const byFeature: Record<string, FeatureUsage> = {};
    for (const [k, v] of this.byFeature) byFeature[k] = { ...v };
    return {
      totalCostUSD: this.sessionCost,
      totalTokens: this.sessionTokens,
      completionCount: this.completionCount,
      byIssue,
      byFeature,
      sessionStart: this.startedAt,
    };
  }

  reset(): void {
    this.sessionCost = 0;
    this.sessionTokens = 0;
    this.completionCount = 0;
    this.byIssue.clear();
    this.byFeature.clear();
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
