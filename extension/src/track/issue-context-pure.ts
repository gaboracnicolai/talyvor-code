// Pure helpers for IssueContextProvider. vscode-free so the test
// runner can exercise the formatter, identifier validator, and
// session-cost accumulator without a vscode runtime.

import type { TrackIssue } from "./client";

// ISSUE_IDENTIFIER_RE matches the conventional "PREFIX-NUMBER"
// shape Track uses (ENG-42, BUG-7, FE-103). 1–8 letters, then a
// dash, then up to 6 digits. Loose enough to cover every project
// we've seen, tight enough to reject obvious typos like "eng42"
// or "ENG42-foo".
const ISSUE_IDENTIFIER_RE = /^[A-Z][A-Z0-9]{0,7}-\d{1,6}$/;

export function isValidIssueIdentifier(s: string): boolean {
  if (!s) return false;
  return ISSUE_IDENTIFIER_RE.test(s);
}

// CONTEXT_DESCRIPTION_CHARS is the trim point for the description
// field in formatIssueContext. The agent and chat have plenty of
// other context; we don't want a thousand-word ticket description
// to crowd it out.
export const CONTEXT_DESCRIPTION_CHARS = 500;

// formatIssueContext renders an issue as a system-prompt fragment.
// When no issue is supplied (or one with no identifier) we return
// the empty string so callers can concatenate unconditionally.
export function formatIssueContext(issue: TrackIssue | undefined): string {
  if (!issue || !issue.identifier) return "";
  const desc = (issue.description ?? "").trim();
  const trimmed =
    desc.length > CONTEXT_DESCRIPTION_CHARS
      ? desc.slice(0, CONTEXT_DESCRIPTION_CHARS) + "…"
      : desc;
  const lines = [
    `Active issue: ${issue.identifier}`,
    `Title: ${issue.title}`,
  ];
  if (issue.status) lines.push(`Status: ${issue.status}`);
  if (trimmed) lines.push(`Description: ${trimmed}`);
  return lines.join("\n");
}

// accumulateSessionCost is the in-memory rollup the provider uses
// to track "this session" spend per issue, before syncing to
// Track. Returns a *new* map so the caller can keep the previous
// snapshot if useful (e.g. for diffing).
export function accumulateSessionCost(
  prev: Map<string, number>,
  issueId: string,
  costUsd: number,
): Map<string, number> {
  const out = new Map(prev);
  const key = issueId || "(no issue)";
  out.set(key, (out.get(key) ?? 0) + costUsd);
  return out;
}

// pickIssuesForSync filters the rollup to entries that actually
// have a non-trivial cost AND a real issue identifier (the
// "(no issue)" bucket has no Track destination). Below threshold
// is dropped so we don't burn a PATCH on $0.0001 noise.
const MIN_SYNC_COST_USD = 0.0001;

export function pickIssuesForSync(
  rollup: Map<string, number>,
): { issueId: string; costUsd: number }[] {
  const out: { issueId: string; costUsd: number }[] = [];
  for (const [issueId, costUsd] of rollup) {
    if (!isValidIssueIdentifier(issueId)) continue;
    if (costUsd < MIN_SYNC_COST_USD) continue;
    out.push({ issueId, costUsd });
  }
  return out;
}

// buildAgentCompletionComment is the body posted to Track after
// an agent task finishes. Format follows the spec: prefix emoji,
// task description, file count, cost, model.
export function buildAgentCompletionComment(
  description: string,
  fileCount: number,
  costUsd: number,
  model: string,
): string {
  return [
    `🤖 Talyvor Agent completed task: ${description}`,
    `Files changed: ${fileCount}`,
    `AI cost: $${costUsd.toFixed(4)}`,
    `Model: ${model}`,
  ].join("\n");
}
