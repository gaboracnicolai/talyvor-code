// TalyvorCodePlugin is the per-project service used by the
// action handlers and tool window. Centralises the "is the user
// configured?" check so each action site stays small.

package com.talyvor.code

import com.intellij.openapi.components.Service
import com.intellij.openapi.project.Project

@Service(Service.Level.PROJECT)
class TalyvorCodePlugin(@Suppress("UNUSED_PARAMETER") private val project: Project) {

    fun getLensUrl(): String =
        TalyvorSettings.getInstance().lensUrl

    fun getLensApiKey(): String =
        TalyvorSettings.getInstance().lensApiKey

    fun getActiveIssue(): String =
        TalyvorSettings.getInstance().activeIssue

    fun getWorkspaceId(): String =
        TalyvorSettings.getInstance().workspaceId

    fun getModel(): String =
        TalyvorSettings.getInstance().model

    fun isConfigured(): Boolean =
        getLensUrl().isNotEmpty() && getLensApiKey().isNotEmpty()

    fun lensClient(): LensClient =
        LensClient(getLensUrl(), getLensApiKey())

    companion object {
        fun getInstance(project: Project): TalyvorCodePlugin =
            project.getService(TalyvorCodePlugin::class.java)
    }
}
