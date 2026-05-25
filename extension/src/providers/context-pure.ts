// Pure helpers shared with the test runner. Kept in their own
// module so we can import them from plain-node test scripts that
// can't load the `vscode` runtime.

export interface CodeContext {
  prefix: string;
  suffix: string;
  currentLine: string;
  languageId: string;
  fileName: string;
  filePath: string;
  workspaceRoot: string;
}

// buildCompletionPrompt assembles the structured user-message
// payload from a CodeContext snapshot. The system prompt lives in
// the completion provider; this function only builds the user
// side, with a [CURSOR] sentinel marking where the model should
// insert text.
export function buildCompletionPrompt(ctx: CodeContext): string {
  return [
    `Language: ${ctx.languageId}`,
    `File: ${ctx.fileName}`,
    "",
    ctx.prefix + ctx.currentLine + "[CURSOR]" + (ctx.suffix ? "\n" + ctx.suffix : ""),
    "",
    "Complete the code at [CURSOR]. Return only the text to insert.",
  ].join("\n");
}

// isCompletionTrigger heuristically rejects positions where a
// ghost-text completion would feel intrusive: open string
// literals, plain comments, just-after-import, blank docs. Takes
// the raw line text + cursor column rather than a TextDocument
// so the function stays pure.
export function isCompletionTrigger(
  lineText: string,
  character: number,
  totalLines = 1,
): boolean {
  if (totalLines === 0) return false;
  const before = lineText.substring(0, character);
  const trimmed = before.trimEnd();

  // Inside an open string literal — odd count of un-escaped
  // matching quotes before the cursor.
  if (countUnescaped(before, '"') % 2 === 1) return false;
  if (countUnescaped(before, "'") % 2 === 1) return false;
  if (countUnescaped(before, "`") % 2 === 1) return false;

  // Inside a line comment. We allow it when the comment ends
  // with a hint phrase (TODO/FIXME/why) so doc-style requests
  // still work.
  const commentIdx = lineText.search(/(\/\/|#)/);
  if (commentIdx >= 0 && commentIdx <= character) {
    const commentBody = before.substring(commentIdx).toLowerCase();
    if (!/todo|fixme|hack|why|explain/.test(commentBody)) {
      return false;
    }
  }

  // Skip after specific keywords that shouldn't auto-complete.
  if (/\b(import|require|use|package|namespace)\s*$/.test(trimmed)) {
    return false;
  }

  return true;
}

function countUnescaped(s: string, ch: string): number {
  let n = 0;
  for (let i = 0; i < s.length; i++) {
    if (s[i] !== ch) continue;
    if (i > 0 && s[i - 1] === "\\") continue;
    n++;
  }
  return n;
}
