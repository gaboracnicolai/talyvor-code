// Pure helpers for AgentMode. vscode-free so the test runner can
// exercise the plan parser, status state-machine, and diff
// renderer without an Electron host.

export type AgentStatus =
  | "idle"
  | "planning"
  | "executing"
  | "awaiting_approval"
  | "applying"
  | "healing"
  | "completed"
  | "failed"
  | "cancelled";

// MAX_FILES_PER_TASK caps planner outputs at the spec's 20 files.
// Beyond that the model loses the plot and the user loses
// oversight; we surface an error rather than silently truncating.
export const MAX_FILES_PER_TASK = 20;

export interface PlannedFile {
  path: string;
  operation: "create" | "modify" | "delete";
  description: string;
}

export interface AgentPlan {
  plan: string[];
  files: PlannedFile[];
}

// parsePlan reads the planner's JSON reply. Strips an optional
// markdown fence (models often wrap responses despite "no
// fences" instructions) and validates the shape so a malformed
// plan surfaces as an Error rather than a runtime crash later.
export function parsePlan(raw: string): AgentPlan {
  let s = raw.trim();
  if (s.startsWith("```")) {
    const i = s.indexOf("\n");
    if (i >= 0) s = s.substring(i + 1);
  }
  if (s.endsWith("```")) {
    s = s.substring(0, s.lastIndexOf("```")).replace(/\n+$/, "");
  }
  let data: unknown;
  try {
    data = JSON.parse(s);
  } catch (err) {
    throw new Error(
      "agent: planner returned invalid JSON: " + (err instanceof Error ? err.message : String(err)),
    );
  }
  if (!data || typeof data !== "object") {
    throw new Error("agent: plan must be an object");
  }
  const obj = data as Record<string, unknown>;
  const plan = Array.isArray(obj.plan) ? obj.plan.filter((s) => typeof s === "string") as string[] : [];
  const filesRaw = Array.isArray(obj.files) ? obj.files : [];
  const files: PlannedFile[] = [];
  for (const f of filesRaw) {
    if (!f || typeof f !== "object") continue;
    const fObj = f as Record<string, unknown>;
    const path = typeof fObj.path === "string" ? fObj.path : "";
    const op = fObj.operation;
    const description = typeof fObj.description === "string" ? fObj.description : "";
    if (!path) continue;
    if (op !== "create" && op !== "modify" && op !== "delete") continue;
    files.push({ path, operation: op, description });
  }
  if (files.length > MAX_FILES_PER_TASK) {
    throw new Error(
      `agent: ${files.length} planned files exceeds MAX_FILES_PER_TASK (${MAX_FILES_PER_TASK})`,
    );
  }
  return { plan, files };
}

// allowedTransitions describes the AgentTask state machine. The
// keys are origins; the values are the legal next states. We use
// this for both runtime guard rails and a unit-test fixture.
export const allowedTransitions: Record<AgentStatus, AgentStatus[]> = {
  idle: ["planning", "cancelled"],
  planning: ["executing", "failed", "cancelled"],
  executing: ["awaiting_approval", "failed", "cancelled"],
  awaiting_approval: ["applying", "cancelled", "failed"],
  // applying can transition into healing (when the user clicked
  // "Run & Heal") or settle into the usual terminal states.
  applying: ["healing", "completed", "failed"],
  // healing can recover (back to completed) or give up (failed).
  // It can also be cancelled if the user dismisses the heal loop.
  healing: ["completed", "failed", "cancelled"],
  completed: [],
  failed: [],
  cancelled: [],
};

// canTransition is the gate the AgentMode class consults before
// mutating status. Tests use it directly to validate the matrix.
export function canTransition(from: AgentStatus, to: AgentStatus): boolean {
  return allowedTransitions[from]?.includes(to) ?? false;
}

// DiffLine describes one line of a unified diff for renderers
// that want structured input rather than raw text.
export interface DiffLine {
  kind: "context" | "add" | "remove" | "header";
  text: string;
}

// renderUnifiedDiff produces a minimal unified diff between
// original and modified, returning structured lines suited to a
// React/webview renderer that colour-codes each kind. Matches the
// agent CLI's output shape (LCS-driven, 3 lines of context).
//
// Identical inputs return an empty array so callers can treat
// that as a no-change signal.
export function renderUnifiedDiff(
  original: string,
  modified: string,
  contextLines = 3,
): DiffLine[] {
  if (original === modified) return [];
  const a = splitLines(original);
  const b = splitLines(modified);
  const ops = lcsDiff(a, b);
  return assembleHunks(ops, contextLines);
}

// assembleHunks groups ops into hunks (separated by long equal
// runs) and emits header + body lines for each. The renderer
// receives a flat list — easier to map to JSX than nested hunk
// objects.
function assembleHunks(ops: Op[], contextLines: number): DiffLine[] {
  // Annotate ops with original/modified positions so we can emit
  // correct @@ headers when trimming context.
  type Annotated = Op & { origPos: number; modPos: number };
  const ann: Annotated[] = [];
  let origIdx = 0;
  let modIdx = 0;
  for (const o of ops) {
    ann.push({ ...o, origPos: origIdx, modPos: modIdx });
    if (o.kind === "=") {
      origIdx++;
      modIdx++;
    } else if (o.kind === "-") origIdx++;
    else modIdx++;
  }

  const out: DiffLine[] = [];
  let i = 0;
  while (i < ann.length) {
    // Skip leading equals.
    let j = i;
    while (j < ann.length && ann[j].kind === "=") j++;
    if (j === ann.length) break;

    const leadingEquals = Math.min(j - i, contextLines);
    const hunkStart = j - leadingEquals;

    // Find the close of the hunk: walk forward, splitting on
    // equal runs of more than 2*contextLines.
    let k = j;
    while (k < ann.length) {
      if (ann[k].kind !== "=") {
        k++;
        continue;
      }
      let run = 0;
      while (k + run < ann.length && ann[k + run].kind === "=") run++;
      if (run > 2 * contextLines && k + run < ann.length) break;
      k += run;
    }
    let end = k + contextLines;
    if (end > ann.length) end = ann.length;
    while (end > k && ann[end - 1].kind !== "=") end--;
    if (end < k) end = k;

    // Compute @@ header counts.
    let origCount = 0;
    let modCount = 0;
    for (const a of ann.slice(hunkStart, end)) {
      if (a.kind === "=") {
        origCount++;
        modCount++;
      } else if (a.kind === "-") origCount++;
      else modCount++;
    }
    const first = ann[hunkStart];
    out.push({
      kind: "header",
      text: `@@ -${first.origPos + 1},${origCount} +${first.modPos + 1},${modCount} @@`,
    });
    for (const a of ann.slice(hunkStart, end)) {
      if (a.kind === "=") out.push({ kind: "context", text: a.text });
      else if (a.kind === "-") out.push({ kind: "remove", text: a.text });
      else out.push({ kind: "add", text: a.text });
    }
    i = end;
  }
  return out;
}

// ─── tiny LCS diff ───────────────────────────────────

type Op = { kind: "=" | "-" | "+"; text: string };

function lcsDiff(a: string[], b: string[]): Op[] {
  const n = a.length;
  const m = b.length;
  const dp: number[][] = Array.from({ length: n + 1 }, () => new Array(m + 1).fill(0));
  for (let i = 1; i <= n; i++) {
    for (let j = 1; j <= m; j++) {
      if (a[i - 1] === b[j - 1]) dp[i][j] = dp[i - 1][j - 1] + 1;
      else dp[i][j] = Math.max(dp[i - 1][j], dp[i][j - 1]);
    }
  }
  const ops: Op[] = [];
  let i = n;
  let j = m;
  while (i > 0 && j > 0) {
    if (a[i - 1] === b[j - 1]) {
      ops.push({ kind: "=", text: a[i - 1] });
      i--;
      j--;
    } else if (dp[i - 1][j] >= dp[i][j - 1]) {
      ops.push({ kind: "-", text: a[i - 1] });
      i--;
    } else {
      ops.push({ kind: "+", text: b[j - 1] });
      j--;
    }
  }
  while (i > 0) {
    ops.push({ kind: "-", text: a[--i] });
  }
  while (j > 0) {
    ops.push({ kind: "+", text: b[--j] });
  }
  return ops.reverse();
}

function splitLines(s: string): string[] {
  if (s === "") return [];
  const out = s.split("\n");
  if (out.length > 0 && out[out.length - 1] === "") out.pop();
  return out;
}
