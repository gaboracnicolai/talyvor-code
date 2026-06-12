// SelectModelAction — Tools → Talyvor → Select AI Model. Presents the
// shared model catalogue (Models.list) in a chooser and writes the
// pick to the persisted settings, mirroring the VS Code selectModel
// command. No network call — purely a settings mutation.

package com.talyvor.code.actions

import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.ui.Messages
import com.talyvor.code.model.Models

class SelectModelAction : AnAction() {
    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val s = settings()
        val models = Models.list()
        val labels = models
            .map {
                "${it.displayName} — ${it.provider} · " +
                    "${it.speedTier.name.lowercase()}/${it.costTier.name.lowercase()} · ${it.id}"
            }
            .toTypedArray()
        val currentIdx = models.indexOfFirst { it.id == s.model }.coerceAtLeast(0)

        val chosen = Messages.showChooseDialog(
            project,
            "Select the Talyvor AI model:",
            "Talyvor: Select Model",
            Messages.getQuestionIcon(),
            labels,
            labels[currentIdx],
        )
        if (chosen < 0) return

        val picked = models[chosen]
        s.model = picked.id
        Messages.showMessageDialog(
            project,
            "Model set to ${picked.displayName} (${picked.id}).",
            "Talyvor Model",
            Messages.getInformationIcon(),
        )
    }
}
