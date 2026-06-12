// TestConnectionAction — Tools → Talyvor → Test Lens Connection.
// Probes /healthz via LensClient.getStatus on a background thread and
// reports a fast yes/no, mirroring the VS Code testConnection command.

package com.talyvor.code.actions

import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.ui.Messages
import com.talyvor.code.LensClient

class TestConnectionAction : AnAction() {
    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val s = settings()
        val client = LensClient(s.lensUrl, s.lensApiKey)
        if (!requireConfigured(project, client)) return

        runOnBackground(
            project,
            "Talyvor: testing Lens connection…",
            body = {
                val status = client.getStatus()
                if (status.available) {
                    "✅ Connected to Lens v${status.version}"
                } else {
                    "❌ Cannot connect to Lens — check the URL and your network."
                }
            },
            onSuccess = { message ->
                val icon = if (message.startsWith("✅")) {
                    Messages.getInformationIcon()
                } else {
                    Messages.getErrorIcon()
                }
                Messages.showMessageDialog(project, message, "Talyvor Connection", icon)
            },
        )
    }
}
