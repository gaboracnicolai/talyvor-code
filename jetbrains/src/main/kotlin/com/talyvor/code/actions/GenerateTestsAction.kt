// GenerateTestsAction — right-click → Talyvor → Generate Tests.
// Uses Sonnet by default (quality matters for test generation)
// but honours whatever model the user has configured. Phase 1
// just shows the result in a dialog so the user can copy it to
// the appropriate test file; Phase 2 lands a TestPanel that
// can write the file directly.

package com.talyvor.code.actions

import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.actionSystem.CommonDataKeys
import com.intellij.openapi.ui.Messages
import com.talyvor.code.model.Models
import com.talyvor.code.testing.TestGenPure

class GenerateTestsAction : AnAction() {
    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val editor = e.getData(CommonDataKeys.EDITOR) ?: return
        val selectedText = editor.selectionModel.selectedText ?: return
        if (selectedText.isBlank()) return

        val s = settings()
        val client = com.talyvor.code.LensClient(s.lensUrl, s.lensApiKey)
        if (!requireConfigured(project, client)) return

        // Derive the canonical language from the file extension so the
        // prompt + framework match the source — the VS Code generator
        // gets this for free from vscode.document.languageId.
        val fileName = e.getData(CommonDataKeys.VIRTUAL_FILE)?.name ?: ""
        val lang = TestGenPure.canonicalLanguageId(fileName)
        val systemPrompt = TestGenPure.systemPromptFor(lang)
        val userPrompt = TestGenPure.buildTestPrompt(selectedText, lang, fileName)
        val framework = TestGenPure.frameworkFor(lang)

        runOnBackground(
            project,
            "Talyvor: generating $framework tests…",
            body = {
                val raw = client.complete(
                    messages = listOf(
                        mapOf("role" to "user", "content" to "$systemPrompt\n\n$userPrompt"),
                    ),
                    // Test generation benefits from Sonnet's reasoning
                    // when the user is still on the cheap Haiku default.
                    // The upgrade target comes from the shared catalogue
                    // (Models.defaultForCommand) so the JetBrains, VS
                    // Code, and CLI surfaces never drift.
                    model = if (s.model.contains("haiku", ignoreCase = true)) {
                        Models.defaultForCommand("test-gen")
                    } else {
                        s.model
                    },
                    feature = "test-gen",
                    workspaceId = s.workspaceId,
                    issueId = s.activeIssue,
                )
                // Strip any preamble/fences the model adds despite the
                // "code only" instruction so the result pastes cleanly.
                TestGenPure.sanitiseGenerated(raw)
            },
            onSuccess = { tests ->
                Messages.showMessageDialog(
                    project,
                    tests,
                    "Talyvor Generated Tests ($framework)",
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
