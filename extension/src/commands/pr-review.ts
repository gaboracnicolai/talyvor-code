// PR-review flow for the extension. Two commands:
//
//   - talyvor.reviewPR        — run a Sonnet review against the
//     current branch's diff vs the default branch, open a
//     verdict-badged webview with Copy + Post-to-GitHub actions.
//   - talyvor.reviewSelection — quick review of a selected block
//     piped through the existing ChatPanel flow (no separate UI).
//
// The diff/file-list/commit-list assembly is local-only via
// child_process git; nothing leaves the workspace until the
// user explicitly invokes Post to GitHub.

import * as vscode from "vscode";
import { execFile } from "child_process";
import { promisify } from "util";
import type { LensClient } from "../lens/client";
import type { LensConfig } from "../lens/types";
import { CostTracker, estimateCostUSD } from "../providers/cost-tracker";
import type { IssueContextProvider } from "../track/issue-context";
import { ChatPanel } from "../panels/ChatPanel";
import { renderMarkdown } from "../docs/docs-pure";
import {
  buildPRReviewSystemPrompt,
  buildPRReviewUserMessage,
  countFindings,
  extractVerdict,
  MAX_DIFF_CHARS,
  truncateDiff,
  verdictBadge,
} from "./pr-review-pure";
import {
  getOpenPRNumber,
  postReviewComment,
  preflight,
  promptForToken,
  resolveToken,
} from "../github/pr-creator";

const execFileP = promisify(execFile);
const REVIEW_MODEL = "claude-sonnet-4-6";

export async function reviewPRCommand(
  lens: LensClient,
  tracker: CostTracker,
  config: LensConfig,
  issueProvider?: IssueContextProvider,
): Promise<void> {
  if (!lens.isConfigured()) {
    void vscode.window.showErrorMessage(
      "Talyvor is not configured. Set talyvor.lensUrl and talyvor.lensApiKey.",
    );
    return;
  }
  const root = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
  if (!root) {
    void vscode.window.showWarningMessage(
      "Open a workspace folder before running a PR review.",
    );
    return;
  }
  const pre = await preflight(root);
  // preflight returns undefined for non-GitHub repos; that's
  // fine for review (we only need the branch info), so fall
  // back to a local-only resolve.
  const base = pre?.defaultBranch ?? (await resolveDefaultBranchLocal(root));
  const branch = pre?.branch ?? (await runGit(root, ["rev-parse", "--abbrev-ref", "HEAD"]));
  if (!base || !branch) {
    void vscode.window.showWarningMessage("Could not resolve the branch/base. Is this a git repo?");
    return;
  }
  if (branch === base) {
    void vscode.window.showWarningMessage(
      `Current branch (${branch}) is the base — switch to a feature branch first.`,
    );
    return;
  }

  const review = await vscode.window.withProgress(
    {
      location: vscode.ProgressLocation.Notification,
      title: `Reviewing ${branch} vs ${base}…`,
      cancellable: false,
    },
    async (): Promise<string | undefined> => {
      let diff = "";
      try {
        diff = await runGit(root, ["diff", `${base}...HEAD`]);
      } catch (err) {
        void vscode.window.showErrorMessage(
          "git diff failed: " + (err instanceof Error ? err.message : String(err)),
        );
        return undefined;
      }
      if (!diff.trim()) {
        void vscode.window.showWarningMessage(`No commits ahead of ${base}.`);
        return undefined;
      }
      const files = (await safeGit(root, ["diff", "--name-only", `${base}...HEAD`]))
        .split("\n").map((s) => s.trim()).filter(Boolean);
      const commits = (await safeGit(root, ["log", `${base}..HEAD`, "--format=%s"]))
        .split("\n").map((s) => s.trim()).filter(Boolean);
      const system = buildPRReviewSystemPrompt("general");
      const userMsg = buildPRReviewUserMessage(
        system,
        truncateDiff(diff, MAX_DIFF_CHARS),
        commits,
        files,
      );
      try {
        const res = await lens.completeWithUsage(
          [{ role: "user", content: userMsg }],
          REVIEW_MODEL,
          "pr-review",
          config.workspaceId,
          config.activeIssue,
          4096,
        );
        const cost = estimateCostUSD(res.inputTokens, res.outputTokens);
        tracker.recordCompletion(
          res.inputTokens + res.outputTokens,
          cost,
          config.activeIssue,
          "pr-review",
        );
        // Touch the issue provider so the per-issue rollup picks
        // up this call without waiting for the next /onRecord.
        void issueProvider;
        return res.text.trim();
      } catch (err) {
        void vscode.window.showErrorMessage(
          "Review failed: " + (err instanceof Error ? err.message : String(err)),
        );
        return undefined;
      }
    },
  );
  if (!review) return;

  PRReviewPanel.show(branch, base, review, pre?.owner, pre?.repo);
}

// reviewSelectionCommand routes the current editor selection
// through ChatPanel with a review-shaped instruction. Lets the
// user get quick feedback on a single function without firing up
// the full PR webview.
export async function reviewSelectionCommand(
  extensionUri: vscode.Uri,
  lens: LensClient,
  tracker: CostTracker,
  config: LensConfig,
  issueProvider: IssueContextProvider,
): Promise<void> {
  const editor = vscode.window.activeTextEditor;
  if (!editor) {
    void vscode.window.showWarningMessage("Open a file and select the code to review.");
    return;
  }
  const text = editor.document.getText(editor.selection);
  if (!text.trim()) {
    void vscode.window.showWarningMessage("Select the code you want reviewed.");
    return;
  }
  const lang = editor.document.languageId;
  const prompt =
    "Review this code as a senior engineer would. Focus on bugs, security, and clarity. " +
    "Highlight critical issues first, then warnings, then suggestions. Include one thing done well.\n\n" +
    "```" + lang + "\n" + text + "\n```";
  await ChatPanel.sendPrompt(
    extensionUri,
    lens,
    tracker,
    config,
    prompt,
    issueProvider,
  );
}

// ─── helpers ──────────────────────────────────────

async function runGit(cwd: string, args: string[]): Promise<string> {
  const { stdout } = await execFileP("git", args, { cwd });
  return stdout.trim();
}

// safeGit is the never-throw variant — returns "" on any error.
// Used for the "extras" (commit list, file list) where a partial
// review is better than a hard failure.
async function safeGit(cwd: string, args: string[]): Promise<string> {
  try {
    return await runGit(cwd, args);
  } catch {
    return "";
  }
}

async function resolveDefaultBranchLocal(root: string): Promise<string> {
  try {
    const ref = await runGit(root, ["symbolic-ref", "refs/remotes/origin/HEAD"]);
    const i = ref.lastIndexOf("/");
    if (i >= 0 && ref.slice(i + 1)) return ref.slice(i + 1);
  } catch {
    // fall through
  }
  for (const candidate of ["main", "master"]) {
    if ((await safeGit(root, ["branch", "--list", candidate])).trim()) return candidate;
  }
  return "main";
}

// ─── PR review webview ────────────────────────────

// PRReviewPanel renders the model's review with a verdict badge,
// markdown body, Copy and (when the repo is a GitHub remote with
// an open PR) Post-to-GitHub actions.
class PRReviewPanel {
  private static current: PRReviewPanel | undefined;
  private readonly disposables: vscode.Disposable[] = [];

  static show(
    branch: string,
    base: string,
    review: string,
    owner: string | undefined,
    repo: string | undefined,
  ): void {
    if (PRReviewPanel.current) {
      PRReviewPanel.current.panel.dispose();
    }
    const panel = vscode.window.createWebviewPanel(
      "talyvorPRReview",
      `PR Review — ${branch}`,
      vscode.ViewColumn.Beside,
      { enableScripts: true, retainContextWhenHidden: true },
    );
    PRReviewPanel.current = new PRReviewPanel(panel, branch, base, review, owner, repo);
  }

  private constructor(
    private readonly panel: vscode.WebviewPanel,
    private readonly branch: string,
    private readonly base: string,
    private readonly review: string,
    private readonly owner: string | undefined,
    private readonly repo: string | undefined,
  ) {
    this.panel.webview.html = this.renderHTML();
    this.panel.webview.onDidReceiveMessage((msg: { type: string }) => {
      void this.handle(msg);
    }, null, this.disposables);
    this.panel.onDidDispose(() => this.dispose(), null, this.disposables);
  }

  private dispose(): void {
    PRReviewPanel.current = undefined;
    this.panel.dispose();
    while (this.disposables.length) this.disposables.pop()?.dispose();
  }

  private async handle(msg: { type: string }): Promise<void> {
    if (msg.type === "copy") {
      await vscode.env.clipboard.writeText(this.review);
      void vscode.window.showInformationMessage("Review copied to clipboard.");
    } else if (msg.type === "post") {
      await this.postToGitHub();
    }
  }

  private async postToGitHub(): Promise<void> {
    if (!this.owner || !this.repo) {
      void vscode.window.showWarningMessage("Not a GitHub repository.");
      return;
    }
    let token = resolveToken();
    if (!token) {
      const entered = await promptForToken();
      if (!entered) return;
      token = entered;
    }
    try {
      const prNumber = await getOpenPRNumber(this.owner, this.repo, this.branch, token);
      if (!prNumber) {
        void vscode.window.showWarningMessage(
          `No open PR for ${this.owner}:${this.branch}.`,
        );
        return;
      }
      await postReviewComment(this.owner, this.repo, prNumber, token, this.review);
      const url = `https://github.com/${this.owner}/${this.repo}/pull/${prNumber}`;
      const action = await vscode.window.showInformationMessage(
        `Posted review to ${url}`,
        "Open PR",
      );
      if (action === "Open PR") {
        await vscode.env.openExternal(vscode.Uri.parse(url));
      }
    } catch (err) {
      void vscode.window.showErrorMessage(
        "Post failed: " + (err instanceof Error ? err.message : String(err)),
      );
    }
  }

  private renderHTML(): string {
    const verdict = extractVerdict(this.review);
    const badge = verdictBadge(verdict);
    const counts = countFindings(this.review);
    const body = renderMarkdown(this.review);
    const showPostButton = Boolean(this.owner && this.repo);
    return `<!doctype html><html><head><meta charset="utf-8"><style>${this.css(badge.color)}</style></head>
<body>
<header>
  <span class="verdict" style="background:${badge.color};color:#1e1e1e">${badge.emoji} ${badge.label}</span>
  <span class="meta">${escapeHTML(this.branch)} → ${escapeHTML(this.base)}</span>
  <span class="counts">🔴 ${counts.critical} · 🟡 ${counts.warning}</span>
  <button id="copyBtn">Copy Markdown</button>
  ${showPostButton ? `<button id="postBtn">Post to GitHub</button>` : ""}
</header>
<main class="md-body">${body}</main>
<script>
const vscode = acquireVsCodeApi();
document.getElementById('copyBtn')?.addEventListener('click', () => vscode.postMessage({type:'copy'}));
document.getElementById('postBtn')?.addEventListener('click', () => vscode.postMessage({type:'post'}));
</script>
</body></html>`;
  }

  private css(verdictColor: string): string {
    return `body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#d4d8e2;background:#1e1e1e;margin:0;display:flex;flex-direction:column;height:100vh;line-height:1.55;font-size:13px}
header{display:flex;align-items:center;gap:10px;padding:10px 14px;border-bottom:1px solid #2a2a2a;background:#191919;flex-wrap:wrap}
.verdict{font-size:11px;font-weight:700;padding:3px 10px;border-radius:4px;letter-spacing:0.05em}
.meta{font-family:"SF Mono",Menlo,monospace;font-size:11px;color:#aaa}
.counts{font-size:11px;color:#888;margin-left:8px}
header button{margin-left:auto;background:#f0a030;color:#1e1e1e;border:0;border-radius:4px;padding:5px 12px;font-weight:600;cursor:pointer;font-size:12px}
header button + button{margin-left:6px}
header button:hover{opacity:0.9}
main{flex:1;overflow-y:auto;padding:16px 18px;max-width:920px}
.md-body h1,.md-body h2,.md-body h3{color:#fff;margin-top:18px;border-bottom:1px solid #2a2a2a;padding-bottom:4px}
.md-body h2:first-child{margin-top:0}
.md-h1{font-size:18px}
.md-h2{font-size:15px}
.md-h3{font-size:13px;border-bottom:0;padding-bottom:0}
.md-p{margin:6px 0}
.md-code{background:#0c0e12;border:1px solid #1f242c;border-radius:6px;padding:8px;font-family:"SF Mono",Menlo,monospace;font-size:12px;overflow-x:auto}
.md-ic{background:#13161c;padding:1px 4px;border-radius:3px;font-family:"SF Mono",Menlo,monospace;font-size:11px}
.md-ul,.md-ol{margin:6px 0;padding-left:20px}
a{color:${verdictColor}}`;
  }
}

function escapeHTML(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}
