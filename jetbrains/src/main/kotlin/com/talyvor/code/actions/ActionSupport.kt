// Shared helpers for the three editor actions. Keeps each action
// file focused on its prompt + result handling and centralises
// the "not configured" dialog + the background-task harness.

package com.talyvor.code.actions

import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.progress.ProgressIndicator
import com.intellij.openapi.progress.ProgressManager
import com.intellij.openapi.progress.Task
import com.intellij.openapi.project.Project
import com.intellij.openapi.ui.Messages
import com.talyvor.code.LensClient
import com.talyvor.code.TalyvorSettings

internal fun requireConfigured(project: Project, client: LensClient): Boolean {
    if (client.isConfigured()) return true
    Messages.showMessageDialog(
        project,
        "Configure Talyvor in Settings → Tools → Talyvor Code.",
        "Talyvor Not Configured",
        Messages.getWarningIcon(),
    )
    return false
}

/**
 * runOnBackground spawns a modal-friendly background task and
 * pipes the result back to the EDT for display. Errors land in
 * a Messages dialog so the user always sees them.
 */
internal fun runOnBackground(
    project: Project,
    title: String,
    body: () -> String,
    onSuccess: (String) -> Unit,
) {
    val task = object : Task.Backgroundable(project, title, true) {
        override fun run(indicator: ProgressIndicator) {
            indicator.isIndeterminate = true
            try {
                val result = body()
                ApplicationManager.getApplication().invokeLater {
                    onSuccess(result)
                }
            } catch (e: Exception) {
                val message = e.message ?: e.toString()
                ApplicationManager.getApplication().invokeLater {
                    Messages.showMessageDialog(
                        project,
                        message,
                        "Talyvor Error",
                        Messages.getErrorIcon(),
                    )
                }
            }
        }
    }
    ProgressManager.getInstance().run(task)
}

internal fun settings(): TalyvorSettings = TalyvorSettings.getInstance()
