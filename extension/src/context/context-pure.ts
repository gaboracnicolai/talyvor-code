// TS port of agent/internal/projectctx/loader.go. Pure helpers
// so the test runner can exercise parsing + prompt-section
// rendering without a vscode runtime. We keep YAML support
// minimal — a hand-rolled parser that handles the documented
// schema (top-level scalar / list / map of strings). The CLI
// handles arbitrary YAML; the IDE accepts the conventional
// .talyvor-context shape.

export const CONTEXT_FILE_NAME = ".talyvor-context";
export const MAX_CONTEXT_FILE_BYTES = 64 * 1024;
export const MAX_CONTEXT_PROMPT_BYTES = 2000;

export interface ProjectContext {
  name: string;
  description: string;
  stack: string[];
  architecture: string;
  conventions: Record<string, string>;
  key_files: string[];
  team_size?: number;
  links?: Record<string, string>;
  filePath?: string;
}

// parseContext sniffs JSON vs YAML on the first non-blank byte
// and dispatches. Empty input yields undefined so callers can
// treat "no usable file" uniformly.
export function parseContext(content: string): ProjectContext | undefined {
  const trimmed = (content ?? "").trim();
  if (!trimmed) return undefined;
  const first = trimmed.charAt(0);
  if (first === "{" || first === "[") {
    return parseJSON(trimmed);
  }
  return parseYAML(trimmed);
}

function parseJSON(body: string): ProjectContext | undefined {
  try {
    const obj = JSON.parse(body) as Partial<ProjectContext>;
    return normalise(obj);
  } catch {
    return undefined;
  }
}

// parseYAML handles the documented `.talyvor-context` subset:
//   key: value                  → scalar
//   key:                        → start of list or nested map
//     - item                    → list element
//     subkey: subvalue          → nested map entry
// No anchors, no multi-line strings, no comments mid-line. The
// CLI uses gopkg.in/yaml.v3 for the full grammar; the IDE keeps
// the zero-dep stance.
function parseYAML(body: string): ProjectContext | undefined {
  const obj: Record<string, unknown> = {};
  const lines = body.split("\n");
  let i = 0;
  while (i < lines.length) {
    const raw = lines[i];
    const line = raw.replace(/\r$/, "");
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) {
      i++;
      continue;
    }
    const indent = line.length - line.trimStart().length;
    if (indent !== 0) {
      // Should not happen at the top level — skip stray.
      i++;
      continue;
    }
    const colon = trimmed.indexOf(":");
    if (colon < 0) {
      i++;
      continue;
    }
    const key = trimmed.slice(0, colon).trim();
    const rest = trimmed.slice(colon + 1).trim();
    if (rest === "") {
      // Block scalar — could be a list or nested map.
      const block: string[] = [];
      i++;
      while (i < lines.length) {
        const next = lines[i].replace(/\r$/, "");
        if (!next.trim() || next.trim().startsWith("#")) {
          i++;
          continue;
        }
        const childIndent = next.length - next.trimStart().length;
        if (childIndent === 0) break;
        block.push(next);
        i++;
      }
      obj[key] = parseBlock(block);
      continue;
    }
    obj[key] = unquote(rest);
    i++;
  }
  return normalise(obj as Partial<ProjectContext>);
}

function parseBlock(lines: string[]): string[] | Record<string, string> {
  // List if every non-blank child line starts with "-".
  let isList = true;
  for (const line of lines) {
    if (!line.trim().startsWith("-")) {
      isList = false;
      break;
    }
  }
  if (isList) {
    const out: string[] = [];
    for (const line of lines) {
      const value = line.trim().replace(/^-\s*/, "");
      if (value !== "") out.push(unquote(value));
    }
    return out;
  }
  const out: Record<string, string> = {};
  for (const line of lines) {
    const trimmed = line.trim();
    const colon = trimmed.indexOf(":");
    if (colon < 0) continue;
    const key = trimmed.slice(0, colon).trim();
    const value = trimmed.slice(colon + 1).trim();
    out[key] = unquote(value);
  }
  return out;
}

function unquote(s: string): string {
  if ((s.startsWith('"') && s.endsWith('"')) || (s.startsWith("'") && s.endsWith("'"))) {
    return s.slice(1, -1);
  }
  return s;
}

function normalise(obj: Partial<ProjectContext> & Record<string, unknown>): ProjectContext {
  const stack = Array.isArray(obj.stack)
    ? (obj.stack as unknown[]).filter((v) => typeof v === "string") as string[]
    : [];
  const keyFiles = Array.isArray(obj.key_files)
    ? (obj.key_files as unknown[]).filter((v) => typeof v === "string") as string[]
    : [];
  return {
    name: typeof obj.name === "string" ? obj.name : "",
    description: typeof obj.description === "string" ? obj.description : "",
    stack,
    architecture: typeof obj.architecture === "string" ? obj.architecture : "",
    conventions: stringMap(obj.conventions),
    key_files: keyFiles,
    team_size: typeof obj.team_size === "number" ? obj.team_size : undefined,
    links: obj.links !== undefined ? stringMap(obj.links) : undefined,
  };
}

function stringMap(v: unknown): Record<string, string> {
  if (!v || typeof v !== "object") return {};
  const out: Record<string, string> = {};
  for (const [k, val] of Object.entries(v as Record<string, unknown>)) {
    if (typeof val === "string") out[k] = val;
  }
  return out;
}

// toPromptSection renders the canonical "Project context:" block.
// Caps the description first (most likely to balloon) and then
// hard-caps the total at MAX_CONTEXT_PROMPT_BYTES.
export function toPromptSection(pc: ProjectContext | undefined): string {
  if (!pc) return "";
  const lines: string[] = ["Project context:"];
  if (pc.name) lines.push(`  Name: ${pc.name}`);
  if (pc.description) {
    let desc = pc.description;
    const maxDesc = Math.max(200, MAX_CONTEXT_PROMPT_BYTES - 600);
    if (desc.length > maxDesc) desc = desc.slice(0, maxDesc) + "…";
    lines.push(`  Description: ${desc}`);
  }
  if (pc.stack.length > 0) lines.push(`  Stack: ${pc.stack.join(", ")}`);
  if (pc.architecture) lines.push(`  Architecture: ${pc.architecture}`);
  const convEntries = Object.entries(pc.conventions);
  if (convEntries.length > 0) {
    lines.push("  Key conventions:");
    for (const [k, v] of convEntries) lines.push(`    - ${k}: ${v}`);
  }
  if (pc.key_files.length > 0) lines.push(`  Key files: ${pc.key_files.join(", ")}`);
  let out = lines.join("\n") + "\n";
  if (out.length > MAX_CONTEXT_PROMPT_BYTES) {
    out = out.slice(0, MAX_CONTEXT_PROMPT_BYTES - 3) + "…\n";
  }
  return out;
}

// validate returns non-fatal warnings about the context. Empty
// array means the context is fit for purpose; callers should
// print these as info, not errors.
export function validate(pc: ProjectContext | undefined): string[] {
  if (!pc) return ["context is empty"];
  const out: string[] = [];
  if (!pc.name.trim()) out.push("name is required");
  if (pc.description.trim().length < 20) {
    out.push("description should be at least 20 characters");
  }
  if (pc.stack.length === 0) out.push("stack should list at least one technology");
  return out;
}

// combinedPrefix mirrors agent/internal/projectctx.CombinedPrefix
// — rules first, context second, separated by a blank line.
export function combinedPrefix(
  rulesPrefix: string,
  pc: ProjectContext | undefined,
): string {
  const rp = (rulesPrefix ?? "").replace(/\n+$/, "");
  const cs = toPromptSection(pc).replace(/\n+$/, "");
  if (!rp && !cs) return "";
  if (!rp) return cs + "\n\n";
  if (!cs) return rp + "\n\n";
  return rp + "\n\n" + cs + "\n\n";
}
