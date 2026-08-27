// TestConnectionAction — Tools → Talyvor → Test Lens Connection.
// Probes /healthz via LensClient.getStatus on a background thread and
// reports a fast yes/no, mirroring the VS Code testConnection command.

package com.talyvor.code.actions

import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.ui.Messages
import com.talyvor.code.ConnReportPure
import com.talyvor.code.CredentialVerdict
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
                // ⚠ TWO PROBES, NOT ONE. getStatus hits /healthz, which Lens serves
                // UNAUTHENTICATED — on its own it reported "✅ Connected" for a wrong, revoked or
                // expired key. The wording lives in ConnReportPure so it can be tested at all.
                val status = client.getStatus()
                val (verdict, code) = if (status.available) {
                    client.verifyCredential()
                } else {
                    Pair(CredentialVerdict.UNKNOWN, 0)
                }
                ConnReportPure.connectionReport(status.available, status.version, verdict, code)
            },
            onSuccess = { report ->
                val icon = if (report.isError) Messages.getErrorIcon() else Messages.getInformationIcon()
                Messages.showMessageDialog(project, report.message, "Talyvor Connection", icon)
            },
        )
    }
}
