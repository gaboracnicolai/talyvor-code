// OpenChatAction reveals (or activates) the Talyvor Code tool
// window. Lives under the same Talyvor menu group as the other
// editor actions so users can launch chat without leaving
// keyboard flow.

package com.talyvor.code.actions

import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.wm.ToolWindowManager

class OpenChatAction : AnAction() {
    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val tw = ToolWindowManager.getInstance(project).getToolWindow("Talyvor Code")
        tw?.activate(null, true)
    }
}
