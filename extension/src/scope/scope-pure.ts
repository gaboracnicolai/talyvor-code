// Pure helpers for the scope feature. vscode-free so the test
// runner can exercise the JSON parser, glob matcher, filter, and
// prompt renderer without an Electron host. Mirrors the Go-side
// agent/internal/scope package.

export const SCOPES_FILE_NAME = ".talyvor-scopes";
export const ACTIVE_SCOPE_FILE_NAME = ".talyvor-active-scope";
export const MAX_SCOPES = 20;

export interface Scope {
  name: string;
  includes: string[];
  excludes: string[];
  focus: string;
}

export type ScopeCatalogue = Record<string, Scope>;

// parseScopes decodes the JSON catalogue. Returns undefined for
// unparseable input so callers can show a graceful warning
// instead of crashing.
export function parseScopes(body: string): ScopeCatalogue | undefined {
  const trimmed = (body ?? "").trim();
  if (!trimmed) return {};
  let raw: unknown;
  try {
    raw = JSON.parse(trimmed);
  } catch {
    return undefined;
  }
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return undefined;
  const out: ScopeCatalogue = {};
  for (const [key, value] of Object.entries(raw as Record<string, unknown>)) {
    if (!isValidScopeKey(key)) continue;
    if (!value || typeof value !== "object") continue;
    const obj = value as Record<string, unknown>;
    const includes = stringArray(obj.includes);
    if (includes.length === 0) continue; // at least one include required
    out[key] = {
      name: typeof obj.name === "string" ? obj.name : key,
      includes,
      excludes: stringArray(obj.excludes),
      focus: typeof obj.focus === "string" ? obj.focus : "",
    };
  }
  return out;
}

const SCOPE_KEY_RE = /^[a-z0-9][a-z0-9\-]*$/;

export function isValidScopeKey(key: string): boolean {
  return SCOPE_KEY_RE.test(key ?? "");
}

function stringArray(v: unknown): string[] {
  if (!Array.isArray(v)) return [];
  return v.filter((x) => typeof x === "string" && x.length > 0) as string[];
}

// matchGlob extends path-style glob matching with the `**`
// segment ("zero or more path segments"). Mirrors the Go
// implementation so the IDE and CLI agree on scope membership.
export function matchGlob(pattern: string, path: string): boolean {
  const pat = normaliseSlashes(pattern).split("/").filter((p) => p.length > 0);
  const segs = normaliseSlashes(path).split("/").filter((p) => p.length > 0);
  return matchSegments(pat, segs);
}

function normaliseSlashes(s: string): string {
  return (s ?? "").replace(/\\/g, "/");
}

function matchSegments(pat: string[], segs: string[]): boolean {
  let pi = 0;
  let si = 0;
  while (pi < pat.length) {
    const seg = pat[pi];
    if (seg === "**") {
      if (pi === pat.length - 1) return true;
      const rest = pat.slice(pi + 1);
      // Try consuming 0, 1, 2, … remaining segments.
      for (let i = si; i <= segs.length; i++) {
        if (matchSegments(rest, segs.slice(i))) return true;
      }
      return false;
    }
    if (si >= segs.length) return false;
    if (!segmentMatches(seg, segs[si])) return false;
    pi++;
    si++;
  }
  return si === segs.length;
}

// segmentMatches implements path.Match semantics for a single
// path segment: `*` matches anything but `/`, `?` matches a
// single char (not `/`), and `[…]` runs are honoured.
function segmentMatches(pattern: string, name: string): boolean {
  return new RegExp("^" + globToRegex(pattern) + "$").test(name);
}

function globToRegex(p: string): string {
  let out = "";
  for (let i = 0; i < p.length; i++) {
    const c = p[i];
    if (c === "*") {
      out += "[^/]*";
    } else if (c === "?") {
      out += "[^/]";
    } else if (c === "[") {
      const close = p.indexOf("]", i);
      if (close < 0) {
        out += "\\[";
      } else {
        out += "[" + p.slice(i + 1, close) + "]";
        i = close;
      }
    } else if ("\\^$.|+(){}".includes(c)) {
      out += "\\" + c;
    } else {
      out += c;
    }
  }
  return out;
}

export function matchAny(patterns: string[], path: string): boolean {
  return (patterns ?? []).some((p) => matchGlob(p, path));
}

// filterFiles applies the supplied scope's include/exclude rules
// to a list of paths. Undefined scope → pass-through.
export function filterFiles(files: string[], scope: Scope | undefined): string[] {
  if (!scope) return files;
  return files.filter((path) => {
    if (matchAny(scope.excludes, path)) return false;
    if (scope.includes.length === 0) return true;
    return matchAny(scope.includes, path);
  });
}

// toPromptSection renders the canonical "Active scope:" block.
// Empty string when no scope is supplied so callers can
// concatenate unconditionally.
export function toPromptSection(scope: Scope | undefined, key?: string): string {
  if (!scope) return "";
  const display = scope.name.trim() || key || "(unnamed)";
  const lines: string[] = [`Active scope: ${display}`];
  if (scope.focus.trim()) lines.push(`  Focus: ${scope.focus.trim()}`);
  if (scope.includes.length > 0) {
    lines.push(`  Included files: ${scope.includes.join(", ")}`);
  }
  if (scope.excludes.length > 0) {
    lines.push(`  Excluded files: ${scope.excludes.join(", ")}`);
  }
  return lines.join("\n") + "\n";
}
