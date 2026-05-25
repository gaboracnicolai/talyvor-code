// generateShellCommand wires the Cmd Palette → input box →
// Lens → notification flow. The notification offers Copy and
// Open Terminal — the terminal flow paste-and-stays (does not
// auto-execute) so the user still confirms in their shell.

import * as vscode from "vscode";
import type { LensClient } from "../lens/client";
import type { LensConfig } from "../lens/types";
import { CostTracker, estimateCostUSD } from "../providers/cost-tracker";
import { buildShellPrompt, detectOS, detectShell, isCommandSafe, stripGenerated } from "./shell-pure";

const SHELL_MODEL = "claude-haiku-4-6";

export async function generateShellCommand(
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
  const description = await vscode.window.showInputBox({
    title: "Generate shell command",
    prompt: "Describe the shell command you need (Haiku-routed for speed)",
    placeHolder: "kill the process listening on port 8080",
  });
  if (!description || description.trim() === "") return;

  const shellName = detectShell(
    vscode.env.shell || process.env.SHELL || (process.platform === "win32" ? "powershell" : "bash"),
  );
  const osName = detectOS(process.platform);
  const systemPrompt = buildShellPrompt(description.trim(), shellName, osName);

  const command = await vscode.window.withProgress(
    {
      location: vscode.ProgressLocation.Notification,
      title: "Generating shell command…",
      cancellable: false,
    },
    async (): Promise<string | undefined> => {
      try {
        const res = await lens.completeWithUsage(
          [{ role: "user", content: systemPrompt }],
          SHELL_MODEL,
          "shell",
          config.workspaceId,
          config.activeIssue,
          512,
        );
        const cost = estimateCostUSD(res.inputTokens, res.outputTokens);
        tracker.recordCompletion(
          res.inputTokens + res.outputTokens,
          cost,
          config.activeIssue,
          "shell",
        );
        return stripGenerated(res.text);
      } catch (err) {
        const msg = err instanceof Error ? err.message : String(err);
        void vscode.window.showErrorMessage("Shell generation failed: " + msg);
        return undefined;
      }
    },
  );
  if (!command) return;

  const warning = isCommandSafe(command)
    ? ""
    : "\n\n⚠️ This command may be destructive — review before running.";
  const action = await vscode.window.showInformationMessage(
    `$ ${command}${warning}`,
    "Copy",
    "Open Terminal",
  );
  if (action === "Copy") {
    await vscode.env.clipboard.writeText(command);
    void vscode.window.showInformationMessage("Command copied.");
  } else if (action === "Open Terminal") {
    const term =
      vscode.window.activeTerminal ?? vscode.window.createTerminal("Talyvor Shell");
    term.show();
    // sendText with addNewLine=false leaves the command staged
    // at the prompt — the user hits Enter to execute. This
    // honours the "user always confirms" invariant.
    term.sendText(command, false);
  }
}
