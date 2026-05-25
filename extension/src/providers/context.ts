// vscode-bound context utilities. The pure helpers live in
// context-pure.ts so the test runner can import them without an
// Electron host; this file layers TextDocument-driven
// getCodeContext on top.

import * as vscode from "vscode";
import {
  CodeContext,
  buildCompletionPrompt,
  isCompletionTrigger,
} from "./context-pure";

export type { CodeContext };
export { buildCompletionPrompt, isCompletionTrigger };

// getCodeContext snapshots the editor state for a completion
// request. Limits prefix/suffix to a fixed line budget so the
// prompt size stays bounded for fast models.
export function getCodeContext(
  document: vscode.TextDocument,
  position: vscode.Position,
  prefixLines = 20,
  suffixLines = 5,
): CodeContext {
  const startLine = Math.max(0, position.line - prefixLines);
  const endLine = Math.min(document.lineCount - 1, position.line + suffixLines);
  const prefixRange = new vscode.Range(startLine, 0, position.line, 0);
  const prefix = document.getText(prefixRange);
  const currentLine = document
    .lineAt(position.line)
    .text.substring(0, position.character);
  const suffixStart = new vscode.Position(position.line + 1, 0);
  const suffixEnd = new vscode.Position(
    endLine,
    document.lineAt(endLine).text.length,
  );
  const suffix =
    suffixStart.line <= endLine
      ? document.getText(new vscode.Range(suffixStart, suffixEnd))
      : "";

  const workspaceRoot =
    vscode.workspace.workspaceFolders?.[0]?.uri.fsPath ?? "";
  return {
    prefix,
    suffix,
    currentLine,
    languageId: document.languageId,
    fileName: relativeFileName(document.uri.fsPath, workspaceRoot),
    filePath: document.uri.fsPath,
    workspaceRoot,
  };
}

// relativeFileName returns the file's path relative to the
// workspace root when possible, otherwise the basename. Keeps the
// prompt readable without leaking absolute paths.
function relativeFileName(filePath: string, workspaceRoot: string): string {
  if (workspaceRoot && filePath.startsWith(workspaceRoot)) {
    return filePath.substring(workspaceRoot.length + 1);
  }
  const i = Math.max(filePath.lastIndexOf("/"), filePath.lastIndexOf("\\"));
  return i >= 0 ? filePath.substring(i + 1) : filePath;
}
