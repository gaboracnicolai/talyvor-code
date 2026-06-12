// GenerateShellCommandAction — Tools → Talyvor → Generate Shell
// Command. Prompts for a natural-language description, asks Lens for a
// single command, strips any preamble/fences, and shows the result.
// The advisory safety screen (ShellPure.isCommandSafe) flips the dialog
// to a warning when the command matches a known-dangerous pattern.
//
// Deliberately DISPLAY-ONLY — it never executes the command, matching
// the conservative posture of the other surfaces; the user copies it
// into a terminal themselves.

package com.talyvor.code.actions

import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.ui.Messages
import com.talyvor.code.LensClient
import com.talyvor.code.model.Models
import com.talyvor.code.shell.ShellPure

class GenerateShellCommandAction : AnAction() {
    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val s = settings()
        val client = LensClient(s.lensUrl, s.lensApiKey)
        if (!requireConfigured(project, client)) return

        val description = Messages.showInputDialog(
            project,
            "Describe the shell command you want:",
            "Talyvor: Generate Shell Command",
            Messages.getQuestionIcon(),
        )?.trim()
        if (description.isNullOrEmpty()) return

        val shell = ShellPure.detectShell(System.getenv("SHELL"))
        val osName = ShellPure.detectOS(platformToken())
        val prompt = ShellPure.buildShellPrompt(description, shell, osName)

        runOnBackground(
            project,
            "Talyvor: generating shell command…",
            body = {
                val raw = client.complete(
                    messages = listOf(mapOf("role" to "user", "content" to prompt)),
                    // Shell generation defaults to the cheap model; an
                    // explicit user choice still wins (Models.resolveModel).
                    model = Models.resolveModel(s.model, "shell"),
                    feature = "shell",
                    workspaceId = s.workspaceId,
                    issueId = s.activeIssue,
                )
                ShellPure.stripGenerated(raw)
            },
            onSuccess = { command ->
                val safe = ShellPure.isCommandSafe(command)
                if (safe) {
                    Messages.showMessageDialog(
                        project,
                        command,
                        "Talyvor Shell Command",
                        Messages.getInformationIcon(),
                    )
                } else {
                    Messages.showMessageDialog(
                        project,
                        "$command\n\n⚠ This command matches a potentially destructive " +
                            "pattern. Review it carefully before running.",
                        "Talyvor Shell Command — review carefully",
                        Messages.getWarningIcon(),
                    )
                }
            },
        )
    }

    // platformToken maps the JVM os.name to the process.platform-style
    // token ShellPure.detectOS understands, so the friendly OS name in
    // the prompt is derived through the same shared mapping.
    private fun platformToken(): String {
        val os = (System.getProperty("os.name") ?: "").lowercase()
        return when {
            os.contains("mac") || os.contains("darwin") -> "darwin"
            os.contains("win") -> "win32"
            os.contains("freebsd") -> "freebsd"
            os.contains("openbsd") -> "openbsd"
            os.contains("nux") || os.contains("nix") -> "linux"
            else -> os
        }
    }
}
