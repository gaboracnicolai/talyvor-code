// Pure helpers for the self-healing loop. vscode-free so the
// test runner can exercise the build-system detector + JSON
// salvage logic without an Electron host. Mirrors the Go side
// in agent/internal/runner.

export const MAX_HEAL_ATTEMPTS = 3;

export type HealLanguage =
  | "go"
  | "typescript"
  | "javascript"
  | "python"
  | "rust"
  | "ruby"
  | "";

export interface BuildPlan {
  command: string;
  language: HealLanguage;
}

export interface FileFix {
  file: string;
  content: string;
}

export interface HealContext {
  taskDescription: string;
  failedCommand: string;
  errorOutput: string;
  changedFiles: string[];
  language: HealLanguage;
  attempt: number;
}

// detectBuildCommandFromMarkers maps a set of observed file
// basenames to a build plan. Pure so tests can pass synthetic
// marker sets; the vscode-bound caller (heal.ts) builds the set
// from fs.stat against the workspace root.
//
// hasNpmTestScript is supplied separately because parsing
// package.json is the loader's job — this helper only decides
// what to do once that knowledge is in hand.
export function detectBuildCommandFromMarkers(
  markers: Set<string>,
  hasNpmTestScript: boolean,
): BuildPlan | undefined {
  if (markers.has("go.mod")) {
    return { command: "go build ./...", language: "go" };
  }
  if (markers.has("Cargo.toml")) {
    return { command: "cargo check", language: "rust" };
  }
  if (markers.has("requirements.txt") || markers.has("pyproject.toml")) {
    return { command: "python -m pytest", language: "python" };
  }
  if (markers.has("Gemfile")) {
    return { command: "bundle exec rspec", language: "ruby" };
  }
  if (markers.has("package.json")) {
    if (!hasNpmTestScript) return undefined;
    const lang: HealLanguage = markers.has("tsconfig.json") ? "typescript" : "javascript";
    return { command: "npm test", language: lang };
  }
  return undefined;
}

// buildHealPrompt mirrors agent/internal/runner.HealingPrompt
// byte-for-byte so the two surfaces produce identical results.
export function buildHealPrompt(ctx: HealContext): string {
  const lines: string[] = [];
  lines.push("The following code changes caused build/test failures. Diagnose the error and return corrected file content.");
  lines.push("");
  lines.push(`Task: ${ctx.taskDescription}`);
  if (ctx.language) lines.push(`Language: ${ctx.language}`);
  if (ctx.attempt > 0) {
    lines.push(`Attempt: ${ctx.attempt} of ${MAX_HEAL_ATTEMPTS}`);
  }
  lines.push(`Command run: ${ctx.failedCommand}`);
  lines.push("");
  lines.push("Error output:");
  lines.push(ctx.errorOutput);
  if (ctx.changedFiles.length > 0) {
    lines.push("");
    lines.push("Files changed in this task:");
    for (const f of ctx.changedFiles) lines.push(`  - ${f}`);
  }
  lines.push("");
  lines.push("Return a JSON array of corrected files. Each entry must include the FULL new file content, not a diff:");
  lines.push("");
  lines.push("[");
  lines.push('  {"file": "src/auth.ts", "content": "<full corrected file content>"}');
  lines.push("]");
  lines.push("");
  lines.push("Return ONLY the JSON array. No prose, no markdown fences, no explanation.");
  return lines.join("\n");
}

// parseHealResult is the defensive JSON parser. Strips fences,
// salvages an inner array from prose-wrapped responses, and
// drops entries missing either file or content.
export function parseHealResult(raw: string): FileFix[] {
  let s = (raw ?? "").trim();
  if (s.startsWith("```")) {
    const nl = s.indexOf("\n");
    if (nl >= 0) s = s.slice(nl + 1);
    const close = s.lastIndexOf("```");
    if (close >= 0) s = s.slice(0, close).replace(/\n+$/, "");
  }
  if (!s.trimStart().startsWith("[")) {
    const start = s.indexOf("[");
    const end = s.lastIndexOf("]");
    if (start >= 0 && end > start) s = s.slice(start, end + 1);
  }
  s = s.trim();
  let parsed: unknown;
  try {
    parsed = JSON.parse(s);
  } catch (err) {
    throw new Error(
      "heal: invalid JSON response: " + (err instanceof Error ? err.message : String(err)),
    );
  }
  if (!Array.isArray(parsed)) {
    throw new Error("heal: expected JSON array");
  }
  const out: FileFix[] = [];
  for (const entry of parsed) {
    if (!entry || typeof entry !== "object") continue;
    const obj = entry as Record<string, unknown>;
    const file = typeof obj.file === "string" ? obj.file.trim() : "";
    const content = typeof obj.content === "string" ? obj.content : "";
    if (!file || !content) continue;
    out.push({ file, content });
  }
  return out;
}
