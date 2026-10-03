// `Talyvor: Run Claude Code (metered by Lens)` — opens a terminal running Claude Code under the
// Talyvor Code CLI's sidecar, so its spend reaches Lens attributed to the active issue. The extension
// does not meter Claude Code itself; the CLI does, and this command is how a user starts it from here.

import * as fs from "fs";
import * as vscode from "vscode";
import { TalyvorConfig } from "../config";
import { CLAUDE_EXEC_ARGS, CLI_NAME, cliEnv, findOnPath } from "./claude-code-pure";

const CLI_INSTALL_URL = "https://github.com/gaboracnicolai/talyvor-code#install";

function isExecutable(file: string): boolean {
  try {
    if (!fs.statSync(file).isFile()) return false;
    if (process.platform !== "win32") fs.accessSync(file, fs.constants.X_OK);
    return true;
  } catch {
    return false;
  }
}

export async function runClaudeCodeCommand(): Promise<void> {
  if (!TalyvorConfig.isConfigured()) {
    void vscode.window.showErrorMessage(
      "Talyvor is not configured. Set talyvor.lensUrl and talyvor.lensApiKey.",
    );
    return;
  }

  const cli = findOnPath(CLI_NAME, process.env.PATH, process.platform, isExecutable, process.env.PATHEXT);
  if (!cli) {
    const pick = await vscode.window.showErrorMessage(
      "Talyvor: Claude Code is metered by the Talyvor Code CLI, and `talyvor-code` is not on your PATH. Install it, then run this command again.",
      "How to install",
    );
    if (pick) void vscode.env.openExternal(vscode.Uri.parse(CLI_INSTALL_URL));
    return;
  }

  const terminal = vscode.window.createTerminal({
    name: "Claude Code · Talyvor",
    shellPath: cli,
    shellArgs: [...CLAUDE_EXEC_ARGS],
    cwd: vscode.workspace.workspaceFolders?.[0]?.uri,
    env: cliEnv(TalyvorConfig.getLensConfig()),
  });
  terminal.show();
}
