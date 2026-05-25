// Pure helpers for the `.talyvor-rules` integration. vscode-free
// so the test runner can exercise the parser and prompt
// combinators without an Electron host. Mirrors the Go side
// (agent/internal/rules) verbatim — same section names, same
// case-insensitive normalisation, same prompt envelope.

export const RULES_FILE_NAME = ".talyvor-rules";

// MAX_RULES_FILE_BYTES caps the on-disk file. Matches the Go
// constant; loaders should refuse to read beyond this.
export const MAX_RULES_FILE_BYTES = 32 * 1024;

export interface RuleSections {
  general: string;
  languages: Record<string, string>;
  agent: string;
  testing: string;
  review: string;
}

export interface Rules {
  raw: string;
  filePath: string;
  sections: RuleSections;
}

// parseRules splits an INI-style body into sections. Section
// names are normalised to lower-case; section bodies are trimmed;
// `#`-prefixed comment lines are dropped; empty sections are not
// stored.
export function parseRules(content: string): RuleSections {
  const sections: RuleSections = {
    general: "",
    languages: {},
    agent: "",
    testing: "",
    review: "",
  };
  let current = "";
  let buf: string[] = [];
  const flush = () => {
    const body = buf.join("\n").trim();
    buf = [];
    if (!body || !current) return;
    switch (current) {
      case "general":
        sections.general = body;
        break;
      case "agent":
        sections.agent = body;
        break;
      case "testing":
        sections.testing = body;
        break;
      case "review":
        sections.review = body;
        break;
      default:
        sections.languages[current] = body;
    }
  };
  for (const rawLine of content.split("\n")) {
    const line = rawLine.replace(/\r$/, "");
    const trimmed = line.trim();
    if (trimmed.startsWith("#")) continue;
    if (trimmed.startsWith("[") && trimmed.endsWith("]") && trimmed.length >= 3) {
      flush();
      current = trimmed.slice(1, -1).trim().toLowerCase();
      continue;
    }
    if (!current) continue;
    buf.push(line);
  }
  flush();
  return sections;
}

// langSection returns the body for the language ID (lower-cased)
// or an empty string when no match. Safe to call with nullish.
function langSection(rules: Rules | undefined, languageId: string): string {
  if (!rules) return "";
  if (!languageId) return "";
  return rules.sections.languages[languageId.trim().toLowerCase()] ?? "";
}

// combine joins non-empty section bodies with a blank line.
function combine(...parts: Array<string | undefined>): string {
  return parts
    .map((p) => (p ?? "").trim())
    .filter((p) => p.length > 0)
    .join("\n\n");
}

export function forLanguage(rules: Rules | undefined, languageId: string): string {
  if (!rules) return "";
  return combine(rules.sections.general, langSection(rules, languageId));
}

export function forAgent(rules: Rules | undefined): string {
  if (!rules) return "";
  return combine(rules.sections.general, rules.sections.agent);
}

export function forReview(rules: Rules | undefined): string {
  if (!rules) return "";
  return combine(rules.sections.general, rules.sections.review);
}

export function forTesting(rules: Rules | undefined, languageId: string): string {
  if (!rules) return "";
  return combine(
    rules.sections.general,
    rules.sections.testing,
    langSection(rules, languageId),
  );
}

// promptPrefix wraps a combined rules blob in the canonical
// "Project rules" envelope. Empty when the input is empty so
// callers can build their prompts unconditionally.
export function promptPrefix(combined: string): string {
  const body = (combined ?? "").trim();
  if (!body) return "";
  return `Project rules (follow these exactly):\n---\n${body}\n---\n\n`;
}
