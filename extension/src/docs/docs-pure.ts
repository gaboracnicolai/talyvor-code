// Pure helpers for the docs integration. Kept vscode-free so the
// test runner can exercise the markdown renderer, hover-rank
// gate, query builder, and freshness icon mapper directly.

// HOVER_RANK_THRESHOLD is the minimum score a search hit needs to
// be considered "related enough to show". Search returns hits
// sorted by score; we silently swallow anything below this so the
// hover doesn't dilute the editor with marginal matches.
export const HOVER_RANK_THRESHOLD = 0.5;

export interface DocsSearchResult {
  pageId: string;
  pageTitle: string;
  spaceName: string;
  headline: string;
  rank: number;
  source: "fulltext" | "semantic" | "both";
  url: string;
}

// pickRelevantHit walks the search results in order and returns
// the first hit whose score (rank OR similarity, whichever exists)
// clears HOVER_RANK_THRESHOLD. Returns undefined when nothing is
// strong enough to surface.
export function pickRelevantHit(
  hits: DocsSearchResult[],
  threshold = HOVER_RANK_THRESHOLD,
): DocsSearchResult | undefined {
  for (const h of hits) {
    const score = h.rank > 0 ? h.rank : 0;
    if (score >= threshold) return h;
  }
  return undefined;
}

// buildHoverQuery combines the word at cursor with a coarse
// surrounding context (function/class name when reasonably
// detectable). The query is intentionally short — docs search
// favours recall over precision for short queries.
export function buildHoverQuery(word: string, contextSymbols: string[]): string {
  const parts: string[] = [word];
  for (const c of contextSymbols) {
    if (!c) continue;
    if (parts.includes(c)) continue;
    parts.push(c);
    if (parts.length >= 3) break;
  }
  return parts.join(" ").trim();
}

// FRESHNESS_ICON maps the docs freshness_status field to a single
// emoji + label pair. Renderers can use either or both.
export interface FreshnessIcon {
  emoji: string;
  label: string;
  color: string;
}

export function freshnessIcon(status: string): FreshnessIcon {
  switch ((status || "").toLowerCase()) {
    case "fresh":
      return { emoji: "🟢", label: "Fresh", color: "var(--vscode-testing-iconPassed)" };
    case "warning":
      return { emoji: "🟡", label: "Warning", color: "var(--vscode-editorWarning-foreground)" };
    case "stale":
      return { emoji: "🔴", label: "Stale", color: "var(--vscode-errorForeground)" };
    default:
      return { emoji: "⚪", label: "Unknown", color: "var(--vscode-descriptionForeground)" };
  }
}

// renderMarkdown is a minimal CommonMark-ish renderer suited to
// the docs panel webview. We support headings, fenced code,
// inline code, bold/italic, lists, and links. Anything fancier
// stays in the Docs web UI — this is just enough to make a spec
// page readable inline.
export function renderMarkdown(md: string): string {
  const lines = md.split("\n");
  const out: string[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    const fence = line.match(/^```([a-zA-Z0-9_+-]*)\s*$/);
    if (fence) {
      const lang = fence[1] || "";
      const body: string[] = [];
      i++;
      while (i < lines.length && !/^```\s*$/.test(lines[i])) {
        body.push(lines[i]);
        i++;
      }
      if (i < lines.length) i++; // consume closer
      out.push(`<pre class="md-code" data-lang="${escapeAttr(lang)}"><code>${escapeHTML(body.join("\n"))}</code></pre>`);
      continue;
    }
    const heading = line.match(/^(#{1,6})\s+(.*)$/);
    if (heading) {
      const level = heading[1].length;
      out.push(`<h${level} class="md-h${level}">${renderInline(heading[2])}</h${level}>`);
      i++;
      continue;
    }
    const ul = line.match(/^[\s]*[-*]\s+(.*)$/);
    if (ul) {
      const items: string[] = [];
      while (i < lines.length) {
        const m = lines[i].match(/^[\s]*[-*]\s+(.*)$/);
        if (!m) break;
        items.push(`<li>${renderInline(m[1])}</li>`);
        i++;
      }
      out.push(`<ul class="md-ul">${items.join("")}</ul>`);
      continue;
    }
    const ol = line.match(/^[\s]*\d+\.\s+(.*)$/);
    if (ol) {
      const items: string[] = [];
      while (i < lines.length) {
        const m = lines[i].match(/^[\s]*\d+\.\s+(.*)$/);
        if (!m) break;
        items.push(`<li>${renderInline(m[1])}</li>`);
        i++;
      }
      out.push(`<ol class="md-ol">${items.join("")}</ol>`);
      continue;
    }
    if (line.trim() === "") {
      i++;
      continue;
    }
    // Paragraph — coalesce consecutive non-empty plain lines.
    const para: string[] = [line];
    i++;
    while (
      i < lines.length &&
      lines[i].trim() !== "" &&
      !/^```/.test(lines[i]) &&
      !/^(#{1,6})\s/.test(lines[i]) &&
      !/^[\s]*[-*]\s/.test(lines[i]) &&
      !/^[\s]*\d+\.\s/.test(lines[i])
    ) {
      para.push(lines[i]);
      i++;
    }
    out.push(`<p class="md-p">${renderInline(para.join(" "))}</p>`);
  }
  return out.join("\n");
}

// renderInline handles inline-level constructs: bold, italic,
// inline code, and links. Order matters — code spans are taken
// first so backtick contents stay verbatim.
function renderInline(s: string): string {
  // Inline code (must run before HTML escape so backticks pull
  // out the body unchanged).
  const codeSpans: string[] = [];
  let work = s.replace(/`([^`]+)`/g, (_, c: string) => {
    codeSpans.push(c);
    // ⚠ THE DELIMITER IS WRITTEN AS AN ESCAPE AND MUST STAY ONE — IT USED TO BE THE RAW BYTE.
    // A single 0x00 makes a whole file opaque to `grep`, with no error and no non-zero exit:
    // `grep -c 'export' extension/src/docs/docs-pure.ts` printed NOTHING and exited 1, while
    // `git grep -c` on the same file printed 10. `git grep` is not fooled, which is why this
    // survived — it is the tool this project's notes use. The VALUE is unchanged, U+0000
    // either way; only the bytes on disk are. The same defect was fixed in talyvor-suite the
    // same day (36fc1700) and found here by sweeping every repo rather than waiting for it.
    return `\u0000${codeSpans.length - 1}\u0000`;
  });
  work = escapeHTML(work);
  // Links: [text](url)
  work = work.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_, text: string, url: string) => {
    return `<a href="${escapeAttr(url)}">${text}</a>`;
  });
  // Bold then italic. Order matters so **a** doesn't get eaten.
  work = work.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  work = work.replace(/\*([^*]+)\*/g, "<em>$1</em>");
  // Restore code spans.
  work = work.replace(/\u0000(\d+)\u0000/g, (_, idx) => {
    return `<code class="md-ic">${escapeHTML(codeSpans[Number(idx)])}</code>`;
  });
  return work;
}

export function escapeHTML(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

function escapeAttr(s: string): string {
  return escapeHTML(s).replace(/"/g, "&quot;").replace(/'/g, "&#39;");
}

// buildHoverMarkdown produces the markdown body shown in the
// hover tooltip. Kept as a pure helper so tests can assert the
// exact layout without spinning up VS Code.
export function buildHoverMarkdown(
  hit: DocsSearchResult,
  docsUrlBase: string,
): string {
  const url = absolutiseDocsURL(hit.url, docsUrlBase);
  const headline = hit.headline
    ? hit.headline.replace(/\s+/g, " ").trim()
    : "";
  const lines = [
    `📄 **Related spec**: ${hit.pageTitle}`,
    `_in ${hit.spaceName}_`,
  ];
  if (headline) lines.push("", headline);
  lines.push("", `[Open in Docs →](${url})`);
  return lines.join("\n");
}

// absolutiseDocsURL turns a relative URL ("/spaces/s/pages/p")
// into a clickable absolute one. Search results from Docs return
// paths; the hover link needs a full URL.
export function absolutiseDocsURL(path: string, base: string): string {
  if (/^https?:/i.test(path)) return path;
  const cleanBase = (base || "").replace(/\/$/, "");
  if (!cleanBase) return path;
  if (path.startsWith("/")) return cleanBase + path;
  return `${cleanBase}/${path}`;
}
