// generateContextCommand asks Lens to synthesise a
// .talyvor-context for the workspace and opens the JSON in a
// scratch editor with a "Save as .talyvor-context" action. The
// user always sees the proposed content before it lands on disk.

import * as vscode from "vscode";
import { execFile } from "child_process";
import { promisify } from "util";
import type { LensClient } from "../lens/client";
import type { LensConfig } from "../lens/types";
import { CostTracker, estimateCostUSD } from "../providers/cost-tracker";
import {
  CONTEXT_FILE_NAME,
  parseContext,
  validate,
} from "../context/context-pure";

const execFileP = promisify(execFile);

const SYSTEM_PROMPT = `Analyze this codebase and return a JSON project context. Return ONLY valid JSON matching this schema:

{
  "name": "string",
  "description": "string (1-2 sentences)",
  "stack": ["string", ...],
  "architecture": "string (one sentence)",
  "conventions": {"area": "convention"},
  "key_files": ["path", ...]
}

No prose, no markdown fences. Be concise — descriptions ≤ 200 chars, conventions ≤ 5 entries.`;

export async function generateContextCommand(
  lens: LensClient,
  tracker: CostTracker,
  config: LensConfig,
): Promise<void> {
  if (!lens.isConfigured()) {
    void vscode.window.showErrorMessage(
      "Talyvor is not configured. Set talyvor.lensUrl and talyvor.lensApiKey.",
    );
    return;
  }
  const root = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
  if (!root) {
    void vscode.window.showWarningMessage("Open a workspace folder before generating context.");
    return;
  }

  const generated = await vscode.window.withProgress(
    {
      location: vscode.ProgressLocation.Notification,
      title: "Generating .talyvor-context from codebase…",
      cancellable: false,
    },
    async (): Promise<string | undefined> => {
      try {
        const summary = await buildContextSummary(root);
        const userMsg = SYSTEM_PROMPT + "\n\n" + summary;
        const res = await lens.completeWithUsage(
          [{ role: "user", content: userMsg }],
          "claude-haiku-4-6",
          "context-generate",
          config.workspaceId,
          config.activeIssue,
          2048,
        );
        const cost = estimateCostUSD(res.inputTokens, res.outputTokens);
        tracker.recordCompletion(
          res.inputTokens + res.outputTokens,
          cost,
          config.activeIssue,
          "context-generate",
        );
        return stripFences(res.text);
      } catch (err) {
        void vscode.window.showErrorMessage(
          "Context generation failed: " + (err instanceof Error ? err.message : String(err)),
        );
        return undefined;
      }
    },
  );
  if (!generated) return;

  // Pretty-print whatever we got (round-tripping through JSON
  // catches the common case where the model added trailing
  // commas or odd whitespace).
  const pretty = formatJSON(generated);

  // Open the proposed content in a scratch editor so the user
  // can review + edit before saving.
  const doc = await vscode.workspace.openTextDocument({
    language: "json",
    content: pretty,
  });
  await vscode.window.showTextDocument(doc, { preview: false });

  const action = await vscode.window.showInformationMessage(
    "Save this content to .talyvor-context?",
    "Save",
    "Discard",
  );
  if (action !== "Save") return;

  const targetUri = vscode.Uri.file(joinPath(root, CONTEXT_FILE_NAME));
  await vscode.workspace.fs.writeFile(targetUri, new TextEncoder().encode(pretty + "\n"));
  const parsed = parseContext(pretty);
  const warns = validate(parsed);
  if (warns.length === 0) {
    void vscode.window.showInformationMessage(`Wrote ${CONTEXT_FILE_NAME}.`);
  } else {
    void vscode.window.showWarningMessage(
      `Wrote ${CONTEXT_FILE_NAME} with warnings: ${warns.join("; ")}`,
    );
  }
}

// buildContextSummary assembles the prompt material the model
// needs: a brief file-count summary, the README excerpt, and a
// dependency manifest if one is present.
async function buildContextSummary(root: string): Promise<string> {
  const parts: string[] = ["Codebase summary:"];
  try {
    const branch = (await execFileP("git", ["rev-parse", "--abbrev-ref", "HEAD"], { cwd: root })).stdout.trim();
    if (branch) parts.push(`Branch: ${branch}`);
  } catch {
    // not a git repo — fine
  }
  try {
    const repo = (await execFileP("git", ["remote", "get-url", "origin"], { cwd: root })).stdout.trim();
    if (repo) parts.push(`Remote: ${repo}`);
  } catch {
    // no remote — fine
  }
  const readme = await tryReadFirstN(joinPath(root, "README.md"), 2000);
  if (readme) {
    parts.push("\nREADME excerpt:");
    parts.push(readme);
  }
  for (const manifest of ["go.mod", "package.json", "Cargo.toml", "requirements.txt", "Gemfile"]) {
    const body = await tryReadFirstN(joinPath(root, manifest), 2000);
    if (body) {
      parts.push(`\nDependencies (${manifest}):`);
      parts.push(body);
      break;
    }
  }
  return parts.join("\n");
}

async function tryReadFirstN(path: string, n: number): Promise<string> {
  try {
    const bytes = await vscode.workspace.fs.readFile(vscode.Uri.file(path));
    const decoded = new TextDecoder().decode(bytes);
    return decoded.length > n ? decoded.slice(0, n) : decoded;
  } catch {
    return "";
  }
}

function joinPath(root: string, name: string): string {
  if (!root) return name;
  const sep = root.endsWith("/") || root.endsWith("\\") ? "" : "/";
  return root + sep + name;
}

function stripFences(s: string): string {
  let out = s.trim();
  if (out.startsWith("```")) {
    const nl = out.indexOf("\n");
    if (nl >= 0) out = out.slice(nl + 1);
  }
  if (out.endsWith("```")) {
    out = out.slice(0, -3).replace(/\n+$/, "");
  }
  return out.trim();
}

function formatJSON(s: string): string {
  try {
    return JSON.stringify(JSON.parse(s), null, 2);
  } catch {
    return s;
  }
}
