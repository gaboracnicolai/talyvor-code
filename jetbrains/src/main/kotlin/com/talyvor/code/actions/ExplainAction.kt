// ExplainAction — right-click → Talyvor → Explain Code. Sends
// the selection to Lens with the "explain" feature tag and
// shows the response in a modal dialog. Phase 1 keeps the UI
// simple — Phase 2 will route this through the tool window for
// continued conversation.

package com.talyvor.code.actions

import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.actionSystem.CommonDataKeys
import com.intellij.openapi.ui.Messages

class ExplainAction : AnAction() {
    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val editor = e.getData(CommonDataKeys.EDITOR) ?: return
        val selectedText = editor.selectionModel.selectedText ?: return
        if (selectedText.isBlank()) return

        val s = settings()
        val client = com.talyvor.code.LensClient(s.lensUrl, s.lensApiKey)
        if (!requireConfigured(project, client)) return

        runOnBackground(
            project,
            "Talyvor: explaining code…",
            body = {
                client.complete(
                    messages = listOf(
                        mapOf(
                            "role" to "user",
                            "content" to "Explain this code clearly and concisely:\n```\n$selectedText\n```",
                        ),
                    ),
                    model = s.model,
                    feature = "explain",
                    workspaceId = s.workspaceId,
                    issueId = s.activeIssue,
                )
            },
            onSuccess = { response ->
                Messages.showMessageDialog(
                    project,
                    response,
                    "Talyvor Explanation",
                    Messages.getInformationIcon(),
                )
            },
        )
    }

    override fun update(e: AnActionEvent) {
        val editor = e.getData(CommonDataKeys.EDITOR)
        e.presentation.isEnabled = editor?.selectionModel?.hasSelection() == true
    }
}
