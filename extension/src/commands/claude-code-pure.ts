// Pure helpers for `Talyvor: Run Claude Code (metered by Lens)`. The command starts the Talyvor Code
// CLI's sidecar — `talyvor-code exec -- claude` — in a VS Code terminal; this file decides where that
// binary is and what it is handed, so `node --test` can reach both without the editor.

import * as path from "path";

export const CLI_NAME = "talyvor-code";

// The arguments the terminal runs the CLI with. The CLI starts a loopback proxy, points Claude Code at
// it and attaches the issue and the Lens credential to every request (agent/internal/sidecar).
export const CLAUDE_EXEC_ARGS: readonly string[] = ["exec", "--", "claude"];

// findOnPath resolves a bare command name the way a shell would: the first PATH entry holding an
// executable of that name. On Windows each PATHEXT suffix is tried as well, since the release asset is
// talyvor-code.exe and PATH lookups there never match a bare name.
export function findOnPath(
  name: string,
  pathEnv: string | undefined,
  platform: NodeJS.Platform | string,
  isExecutable: (file: string) => boolean,
  pathext?: string,
): string | undefined {
  const win = platform === "win32";
  const p = win ? path.win32 : path.posix;
  const suffixes = win ? ["", ...(pathext || ".EXE;.CMD;.BAT").split(";").filter((s) => s !== "")] : [""];
  for (const dir of (pathEnv ?? "").split(win ? ";" : ":")) {
    if (dir === "") continue; // an empty entry would mean the current directory — never trusted here
    for (const ext of suffixes) {
      const candidate = p.join(dir, name + ext);
      if (isExecutable(candidate)) return candidate;
    }
  }
  return undefined;
}

export interface ClaudeCodeLensSettings {
  url: string;
  apiKey: string;
  workspaceId: string;
  activeIssue: string;
}

// cliEnv is the environment the CLI reads its Lens settings from (agent/internal/config). The key
// travels in the terminal's environment, never on its command line, so it is in neither shell history
// nor `ps` output. An unset active issue is left out rather than sent empty, so the CLI falls back to
// detecting the issue from the branch name.
export function cliEnv(s: ClaudeCodeLensSettings): Record<string, string> {
  const env: Record<string, string> = {
    TALYVOR_LENS_URL: s.url,
    TALYVOR_LENS_API_KEY: s.apiKey,
  };
  if (s.workspaceId.trim() !== "") env.TALYVOR_WORKSPACE_ID = s.workspaceId.trim();
  if (s.activeIssue.trim() !== "") env.TALYVOR_ISSUE = s.activeIssue.trim();
  return env;
}
