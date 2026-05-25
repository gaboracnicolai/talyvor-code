// TS port of agent/internal/github/pr.go. Pure helpers so the
// QuickPick + AgentPanel can render previews and the test runner
// can validate URL parsing / body shaping without a vscode host.

export interface ParsedRepo {
  owner: string;
  repo: string;
}

const HTTPS_RE = /^https?:\/\/github\.com\/([^/]+)\/([^/]+?)(?:\.git)?\/?$/;
const SSH_RE = /^git@github\.com:([^/]+)\/([^/]+?)(?:\.git)?$/;

// parseRepoFromURL extracts owner+repo from a GitHub remote URL.
// Accepts both HTTPS and SSH forms. Returns undefined for non-
// GitHub remotes so callers can skip the PR flow gracefully.
export function parseRepoFromURL(remoteURL: string): ParsedRepo | undefined {
  const url = (remoteURL ?? "").trim();
  if (!url) return undefined;
  const httpsMatch = HTTPS_RE.exec(url);
  if (httpsMatch) {
    const [, owner, repo] = httpsMatch;
    if (owner && repo) return { owner, repo };
  }
  const sshMatch = SSH_RE.exec(url);
  if (sshMatch) {
    const [, owner, repo] = sshMatch;
    if (owner && repo) return { owner, repo };
  }
  return undefined;
}

export function isGitHub(remoteURL: string): boolean {
  return (remoteURL ?? "").includes("github.com");
}

// MAX_BRANCH_SLUG_LEN matches the Go side. GitHub allows longer
// branch names but the UI elides past ~50 chars.
export const MAX_BRANCH_SLUG_LEN = 50;

// slugifyBranch turns a free-form description into a branch
// name. Picks "fix/" when the description leans bug-fix,
// "feat/" otherwise. Empty input returns a timestamped fallback.
export function slugifyBranch(description: string): string {
  const desc = (description ?? "").trim().toLowerCase();
  if (!desc) return `feat/talyvor-${Math.floor(Date.now() / 1000)}`;
  const kws = ["fix", "bug", "patch", "hotfix", "resolve"];
  let prefix = "feat/";
  for (const kw of kws) {
    if (desc.startsWith(kw) || ` ${desc} `.includes(` ${kw} `)) {
      prefix = "fix/";
      break;
    }
  }
  let clean = desc.replace(/[^a-z0-9\-_.]+/g, "-").replace(/-+/g, "-");
  clean = clean.replace(/^[-_.]+|[-_.]+$/g, "");
  if (!clean) clean = `talyvor-${Math.floor(Date.now() / 1000)}`;
  let out = prefix + clean;
  if (out.length > MAX_BRANCH_SLUG_LEN) {
    out = out.slice(0, MAX_BRANCH_SLUG_LEN).replace(/[-_.]+$/, "");
  }
  return out;
}

// generatePRBody renders the canonical PR description. Mirrors
// the Go side byte-for-byte so PRs opened from the CLI and IDE
// surfaces look identical.
export function generatePRBody(
  issueID: string,
  issueTitle: string,
  taskDesc: string,
  changedFiles: string[],
  costUSD: number,
): string {
  const out: string[] = [];
  out.push("## Summary", "");
  if (issueID) {
    const title = issueTitle || "(no title)";
    out.push(`Implements: ${issueID} — ${title}`, "");
  }
  if (taskDesc.trim()) {
    out.push(taskDesc.trim(), "");
  }
  if (changedFiles.length > 0) {
    out.push("## Changes", "");
    for (const f of changedFiles) out.push(`- \`${f}\``);
    out.push("");
  }
  out.push("## AI Cost Attribution", "");
  out.push("This PR was implemented with Talyvor Code.");
  if (issueID) {
    out.push(`AI cost: $${costUSD.toFixed(2)} (attributed to ${issueID})`);
  } else {
    out.push(`AI cost: $${costUSD.toFixed(2)}`);
  }
  out.push("");
  out.push("---");
  out.push("*Opened by [Talyvor Code](https://github.com/gaboracnicolai/talyvor-code)*");
  out.push("");
  return out.join("\n");
}

// PRCreateRequest is the body we POST to /repos/:owner/:repo/pulls.
export interface PRCreateRequest {
  title: string;
  body: string;
  head: string;
  base: string;
  draft: boolean;
}

export interface PRCreateResult {
  number: number;
  url: string;
  title: string;
  state: string;
}
