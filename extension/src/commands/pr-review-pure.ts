// Pure helpers for the PR review flow. vscode-free so the
// test runner can exercise verdict extraction, finding counts,
// diff truncation, and prompt assembly without an Electron host.
// Mirrors agent/internal/github/{review,truncate}.go.

export type PRVerdict = "APPROVE" | "REQUEST CHANGES" | "NEEDS DISCUSSION";

export const MAX_DIFF_CHARS = 32000;

// extractVerdict scans the review body for the verdict line.
// REQUEST CHANGES wins over APPROVE when both appear — a real
// blocker beats a polite "but otherwise approved". Default when
// nothing matches: NEEDS DISCUSSION, the safe non-action.
export function extractVerdict(review: string): PRVerdict {
  const upper = (review ?? "").toUpperCase();
  if (upper.includes("REQUEST CHANGES")) return "REQUEST CHANGES";
  if (upper.includes("APPROVE")) return "APPROVE";
  if (upper.includes("NEEDS DISCUSSION")) return "NEEDS DISCUSSION";
  return "NEEDS DISCUSSION";
}

export interface VerdictBadge {
  label: PRVerdict;
  color: string;
  emoji: string;
}

export function verdictBadge(v: PRVerdict): VerdictBadge {
  switch (v) {
    case "APPROVE":
      return { label: "APPROVE", color: "#5cd187", emoji: "✅" };
    case "REQUEST CHANGES":
      return { label: "REQUEST CHANGES", color: "#ff7070", emoji: "🔴" };
    case "NEEDS DISCUSSION":
      return { label: "NEEDS DISCUSSION", color: "#f0a030", emoji: "🟡" };
  }
}

// truncateDiff matches the Go-side TruncateDiff exactly. When
// the input fits, returns it unchanged; otherwise splits in half
// and inserts a marker so the model sees both ends of the change.
export function truncateDiff(diff: string, maxChars: number = MAX_DIFF_CHARS): string {
  if (maxChars <= 0) maxChars = MAX_DIFF_CHARS;
  if (diff.length <= maxChars) return diff;
  const half = Math.floor(maxChars / 2);
  return diff.slice(0, half)
    + "\n\n... [diff truncated — showing representative sample] ...\n\n"
    + diff.slice(diff.length - half);
}

// bulletStart mirrors the Go regex — strips bullet prefixes
// (-, *, "1.") plus any leading whitespace.
const bulletStart = /^\s*([-*]|\d+\.)\s+/;

// countFindings walks the review body and reports counts under
// the Critical and Warning headings. "None" / empty entries
// count as zero so a clean review is correctly reported.
export function countFindings(review: string): { critical: number; warning: number } {
  let section: "critical" | "warning" | "" = "";
  let critical = 0;
  let warning = 0;
  for (const raw of (review ?? "").split("\n")) {
    const line = raw.trim();
    if (!line) continue;
    if (line.startsWith("#")) {
      const lower = line.toLowerCase();
      if (lower.includes("critical")) section = "critical";
      else if (lower.includes("warning")) section = "warning";
      else section = "";
      continue;
    }
    if (!section) continue;
    if (!bulletStart.test(line)) continue;
    let body = line.replace(bulletStart, "").trim();
    if (!body) continue;
    // Drop a leading short emoji + space so "🔴 SQL injection"
    // doesn't look like the literal "None" label.
    const firstSpace = body.indexOf(" ");
    if (firstSpace > 0 && firstSpace <= 4) {
      const tail = body.slice(firstSpace + 1).trim();
      if (tail) body = tail;
    }
    const lower = body.toLowerCase();
    if (lower === "none" || lower === "none.") continue;
    if (section === "critical") critical++;
    else warning++;
  }
  return { critical, warning };
}

// buildPRReviewSystemPrompt mirrors the Go reviewSystemPrompt
// in PR mode. Kept here so the extension's review uses the same
// structured-output contract as the CLI.
export function buildPRReviewSystemPrompt(reviewType = "general"): string {
  let focus =
    "Bugs and logic errors, security vulnerabilities, performance issues, code quality, and maintainability.";
  switch ((reviewType ?? "general").toLowerCase()) {
    case "security":
      focus = "Authentication & authorization gaps, input validation, injection (SQL/command/template), unsafe deserialization, secret handling, CSRF/XSS, dependency CVEs, and data leakage.";
      break;
    case "performance":
      focus = "Algorithmic complexity, N+1 queries, memory allocations on hot paths, blocking I/O, lock contention, and unnecessary computation in render paths.";
      break;
  }
  return [
    `You are an expert code reviewer performing a pull-request review. Analyze the diff carefully and focus on: ${focus}`,
    "",
    "Structure your review as Markdown with these sections:",
    "",
    "## PR Summary",
    "<2-3 sentence summary of what this PR does>",
    "",
    "## Review",
    "",
    "### 🔴 Critical Issues",
    "<blocking issues that must be fixed>",
    "",
    "### 🟡 Warnings",
    "<non-blocking issues worth addressing>",
    "",
    "### 💡 Suggestions",
    "<optional improvements>",
    "",
    "### ✅ Good Patterns",
    "<things done well — always include at least one>",
    "",
    "## Verdict",
    "APPROVE / REQUEST CHANGES / NEEDS DISCUSSION",
  ].join("\n");
}

// buildPRReviewUserMessage mirrors the Go buildReviewUserMessage
// for the PR path. Commits + files + diff in that order so the
// model sees intent before the raw change.
export function buildPRReviewUserMessage(
  systemPrompt: string,
  diff: string,
  commits: string[],
  files: string[],
): string {
  const lines: string[] = [];
  lines.push(systemPrompt, "", "Review this pull request:", "");
  if (commits.length > 0) {
    lines.push("Commits:");
    for (const c of commits) lines.push(`  - ${c}`);
    lines.push("");
  }
  if (files.length > 0) {
    lines.push("Files changed:");
    for (const f of files) lines.push(`  - ${f}`);
    lines.push("");
  }
  lines.push("Diff:", diff);
  return lines.join("\n");
}
