// pr-creator.ts is the vscode-bound side of GitHub PR opening
// for the extension. Wraps:
//   - Remote URL discovery via `git remote get-url origin`
//   - Branch + diff lookup
//   - Token resolution: settings → env (GITHUB_TOKEN)
//   - REST call to api.github.com/repos/<owner>/<repo>/pulls
//
// The flow is intentionally minimal — the AgentPanel + selector
// handles the QuickPick UX; this file is the IO layer.

import * as vscode from "vscode";
import { execFile } from "child_process";
import { promisify } from "util";
import {
  generatePRBody,
  isGitHub,
  parseRepoFromURL,
  type PRCreateResult,
} from "./pr-pure";

const execFileP = promisify(execFile);

export interface PRRequest {
  workspaceRoot: string;
  title: string;
  body: string;
  head: string;
  base: string;
  draft: boolean;
}

export interface PRPreflight {
  owner: string;
  repo: string;
  branch: string;
  defaultBranch: string;
  remoteURL: string;
}

// preflight resolves everything the GitHub call needs without
// touching the network. Returns undefined when the workspace
// isn't a GitHub repo so callers can hide the PR UI gracefully.
export async function preflight(
  workspaceRoot: string,
): Promise<PRPreflight | undefined> {
  if (!workspaceRoot) return undefined;
  let remote: string;
  try {
    remote = (await execFileP("git", ["remote", "get-url", "origin"], {
      cwd: workspaceRoot,
    })).stdout.trim();
  } catch {
    return undefined;
  }
  if (!isGitHub(remote)) return undefined;
  const parsed = parseRepoFromURL(remote);
  if (!parsed) return undefined;
  const branch = await runGit(workspaceRoot, ["rev-parse", "--abbrev-ref", "HEAD"]);
  const defaultBranch = await resolveDefaultBranch(workspaceRoot);
  return { ...parsed, branch, defaultBranch, remoteURL: remote };
}

async function resolveDefaultBranch(root: string): Promise<string> {
  try {
    const ref = await runGit(root, ["symbolic-ref", "refs/remotes/origin/HEAD"]);
    const i = ref.lastIndexOf("/");
    if (i >= 0) {
      const tail = ref.slice(i + 1);
      if (tail) return tail;
    }
  } catch {
    // fall through
  }
  for (const candidate of ["main", "master"]) {
    try {
      const out = await runGit(root, ["branch", "--list", candidate]);
      if (out.trim()) return candidate;
    } catch {
      // ignore
    }
  }
  return "main";
}

async function runGit(cwd: string, args: string[]): Promise<string> {
  const { stdout } = await execFileP("git", args, { cwd });
  return stdout.trim();
}

// resolveToken reads the token from the `talyvor.githubToken`
// setting (workspace-scoped) and falls back to GITHUB_TOKEN.
// Returns empty when neither is set; callers must prompt or
// error out cleanly.
export function resolveToken(): string {
  const setting = vscode.workspace
    .getConfiguration("talyvor")
    .get<string>("githubToken", "");
  if (setting.trim() !== "") return setting.trim();
  return (process.env.GITHUB_TOKEN ?? "").trim();
}

// createPR posts to api.github.com/repos/<owner>/<repo>/pulls.
// The fetch is unsigned — auth lives in the Authorization header.
export async function createPR(
  owner: string,
  repo: string,
  token: string,
  req: { title: string; body: string; head: string; base: string; draft: boolean },
): Promise<PRCreateResult> {
  const res = await fetch(`https://api.github.com/repos/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/pulls`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "Accept": "application/vnd.github+json",
      "Authorization": `Bearer ${token}`,
      "X-GitHub-Api-Version": "2022-11-28",
    },
    body: JSON.stringify(req),
  });
  const text = await res.text();
  if (!res.ok) {
    throw new Error(`GitHub ${res.status} ${res.statusText}: ${text.trim()}`);
  }
  const parsed = JSON.parse(text) as { number?: number; html_url?: string; title?: string; state?: string };
  return {
    number: parsed.number ?? 0,
    url: parsed.html_url ?? "",
    title: parsed.title ?? "",
    state: parsed.state ?? "",
  };
}

// getOpenPRNumber looks up the open PR matching owner:branch.
// Returns undefined when no PR is open so callers can degrade
// gracefully instead of erroring.
export async function getOpenPRNumber(
  owner: string,
  repo: string,
  branch: string,
  token: string,
): Promise<number | undefined> {
  const q = new URLSearchParams({ state: "open", head: `${owner}:${branch}` });
  const res = await fetch(
    `https://api.github.com/repos/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/pulls?${q.toString()}`,
    {
      headers: {
        "Accept": "application/vnd.github+json",
        "Authorization": `Bearer ${token}`,
        "X-GitHub-Api-Version": "2022-11-28",
      },
    },
  );
  if (!res.ok) {
    throw new Error(`GitHub ${res.status} ${res.statusText}`);
  }
  const list = (await res.json()) as Array<{ number?: number }>;
  if (list.length === 0) return undefined;
  return list[0].number;
}

// postReviewComment publishes a PR review with event=COMMENT.
// Mirrors the Go-side PostPRReview — passive note, never
// auto-approving or auto-blocking from the IDE.
export async function postReviewComment(
  owner: string,
  repo: string,
  prNumber: number,
  token: string,
  body: string,
): Promise<void> {
  const res = await fetch(
    `https://api.github.com/repos/${encodeURIComponent(owner)}/${encodeURIComponent(repo)}/pulls/${prNumber}/reviews`,
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Accept": "application/vnd.github+json",
        "Authorization": `Bearer ${token}`,
        "X-GitHub-Api-Version": "2022-11-28",
      },
      body: JSON.stringify({ body, event: "COMMENT" }),
    },
  );
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`GitHub ${res.status} ${res.statusText}: ${text.trim()}`);
  }
}

// promptForToken opens an inputBox so the user can paste a
// token when neither the setting nor the env supplies one. We
// don't persist it — callers can offer to save when appropriate.
export async function promptForToken(): Promise<string | undefined> {
  const value = await vscode.window.showInputBox({
    title: "GitHub token",
    prompt: "Paste a personal-access token with `repo` scope (not stored)",
    password: true,
    ignoreFocusOut: true,
  });
  return value?.trim() || undefined;
}

// renderPRBody is a thin re-export so command sites don't import
// from two modules when they only need the body.
export const renderPRBody = generatePRBody;
