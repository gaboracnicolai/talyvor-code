// Persisted plugin settings + the Settings → Tools → Talyvor
// Code page. Two classes:
//
//   - TalyvorSettings           — PersistentStateComponent backing
//     the values. Lives as an application-level service so the
//     same values apply across every open project.
//   - TalyvorSettingsConfigurable — the Configurable that renders
//     the form and applies edits back to the state.

package com.talyvor.code

import com.intellij.credentialStore.CredentialAttributes
import com.intellij.credentialStore.generateServiceName
import com.intellij.ide.passwordSafe.PasswordSafe
import com.intellij.openapi.components.PersistentStateComponent
import com.intellij.openapi.components.Service
import com.intellij.openapi.components.State
import com.intellij.openapi.components.Storage
import com.intellij.openapi.components.service
import com.intellij.openapi.options.Configurable
import java.net.URI
import com.intellij.ui.components.JBPasswordField
import com.intellij.ui.components.JBTextField
import com.intellij.util.ui.FormBuilder
import javax.swing.JComponent
import javax.swing.JPanel

@State(
    name = "TalyvorSettings",
    storages = [Storage("TalyvorSettings.xml")]
)
@Service(Service.Level.APP)
class TalyvorSettings : PersistentStateComponent<TalyvorSettings.State> {

    // B-Code-creds (b): API keys are NOT persisted in State — that would write them in plaintext into
    // TalyvorSettings.xml. They live in the IDE credential store (PasswordSafe). State holds only
    // non-secret prefs.
    data class State(
        var lensUrl: String = "http://localhost:8080",
        var trackUrl: String = "",
        var workspaceId: String = "",
        var activeIssue: String = "",
        var model: String = "claude-haiku-4-5"
    )

    private var state = State()

    override fun getState(): State = state
    override fun loadState(state: State) {
        this.state = state
    }

    // B-Code-creds (a): sanitize the base URL on read — reject non-https-remote / link-local / metadata,
    // so a hostile config can't make the client send the API key to an attacker or internal host.
    var lensUrl: String
        get() = sanitizeBaseUrl(state.lensUrl)
        set(v) {
            state.lensUrl = v
        }

    var lensApiKey: String
        get() = PasswordSafe.instance.getPassword(keyAttrs("lensApiKey")) ?: ""
        set(v) {
            PasswordSafe.instance.setPassword(keyAttrs("lensApiKey"), v.ifBlank { null })
        }

    var trackUrl: String
        get() = sanitizeBaseUrl(state.trackUrl)
        set(v) {
            state.trackUrl = v
        }

    var trackApiKey: String
        get() = PasswordSafe.instance.getPassword(keyAttrs("trackApiKey")) ?: ""
        set(v) {
            PasswordSafe.instance.setPassword(keyAttrs("trackApiKey"), v.ifBlank { null })
        }

    var workspaceId: String
        get() = state.workspaceId
        set(v) {
            state.workspaceId = v
        }

    var activeIssue: String
        get() = state.activeIssue
        set(v) {
            state.activeIssue = v
        }

    var model: String
        get() = state.model
        set(v) {
            state.model = v
        }

    companion object {
        fun getInstance(): TalyvorSettings = service()

        private fun keyAttrs(id: String): CredentialAttributes =
            CredentialAttributes(generateServiceName("Talyvor Code", id))

        private fun sanitizeBaseUrl(raw: String): String {
            if (raw.isBlank()) return ""
            val u = try {
                URI(raw)
            } catch (e: Exception) {
                return ""
            }
            val host = u.host ?: return ""
            val isLocal = host == "localhost" || host == "127.0.0.1" || host == "::1"
            if (u.scheme != "https" && !(u.scheme == "http" && isLocal)) return ""
            if (host == "0.0.0.0" || host.startsWith("169.254.") || host.startsWith("fe80")) return ""
            return raw
        }
    }
}

class TalyvorSettingsConfigurable : Configurable {
    private val lensUrlField = JBTextField()
    private val lensApiKeyField = JBPasswordField()
    private val workspaceIdField = JBTextField()
    private val activeIssueField = JBTextField()
    private val modelField = JBTextField()

    override fun getDisplayName(): String = "Talyvor Code"

    override fun createComponent(): JComponent {
        val settings = TalyvorSettings.getInstance()
        lensUrlField.text = settings.lensUrl
        lensApiKeyField.text = settings.lensApiKey
        workspaceIdField.text = settings.workspaceId
        activeIssueField.text = settings.activeIssue
        modelField.text = settings.model

        return FormBuilder.createFormBuilder()
            .addLabeledComponent("Lens URL:", lensUrlField)
            .addLabeledComponent("Lens API key:", lensApiKeyField)
            .addLabeledComponent("Workspace ID:", workspaceIdField)
            .addLabeledComponent("Active issue:", activeIssueField)
            .addLabeledComponent("Model:", modelField)
            .addComponentFillVertically(JPanel(), 0)
            .panel
    }

    override fun isModified(): Boolean {
        val s = TalyvorSettings.getInstance()
        return lensUrlField.text != s.lensUrl ||
            String(lensApiKeyField.password) != s.lensApiKey ||
            workspaceIdField.text != s.workspaceId ||
            activeIssueField.text != s.activeIssue ||
            modelField.text != s.model
    }

    override fun apply() {
        val s = TalyvorSettings.getInstance()
        s.lensUrl = lensUrlField.text.trim()
        s.lensApiKey = String(lensApiKeyField.password).trim()
        s.workspaceId = workspaceIdField.text.trim()
        s.activeIssue = activeIssueField.text.trim()
        s.model = modelField.text.trim().ifEmpty { "claude-haiku-4-5" }
    }

    override fun reset() {
        val s = TalyvorSettings.getInstance()
        lensUrlField.text = s.lensUrl
        lensApiKeyField.text = s.lensApiKey
        workspaceIdField.text = s.workspaceId
        activeIssueField.text = s.activeIssue
        modelField.text = s.model
    }
}
