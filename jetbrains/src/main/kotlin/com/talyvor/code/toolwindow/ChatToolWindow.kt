// ChatToolWindow — Phase 1 chat surface for the JetBrains side.
// Three parts:
//
//   - ChatToolWindowFactory (plugin.xml entry point)
//   - ChatPanel (Swing UI: header + transcript + composer)
//   - ChatTurn (one user/assistant exchange, surfaced in the list)
//
// All network calls run on a background thread via swingworker;
// the EDT is only touched to mutate the document model. Phase 2
// adds streaming + code-block actions + multi-turn history.

package com.talyvor.code.toolwindow

import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.project.Project
import com.intellij.openapi.ui.Messages
import com.intellij.openapi.wm.ToolWindow
import com.intellij.openapi.wm.ToolWindowFactory
import com.intellij.ui.JBColor
import com.intellij.ui.components.JBLabel
import com.intellij.ui.components.JBScrollPane
import com.intellij.ui.components.JBTextArea
import com.talyvor.code.LensClient
import com.talyvor.code.StreamCallbacks
import com.talyvor.code.TalyvorSettings
import java.awt.BorderLayout
import java.awt.Dimension
import java.awt.event.ActionEvent
import javax.swing.BorderFactory
import javax.swing.Box
import javax.swing.BoxLayout
import javax.swing.JButton
import javax.swing.JPanel

// Every colour comes from Brand, as a JBColor that follows the IDE's light or dark theme.
private fun BrandColor.jb() = JBColor(light, dark)

// A chat bubble: a raised panel with a 1px hairline, sitting on the transcript's surface.
private fun bubbleArea(text: String, bg: JBColor) = JBTextArea(text).apply {
    isEditable = false
    background = bg
    foreground = Brand.ink.jb()
    lineWrap = true
    wrapStyleWord = true
    border = BorderFactory.createCompoundBorder(
        BorderFactory.createLineBorder(Brand.line.jb()),
        BorderFactory.createEmptyBorder(8, 10, 8, 10),
    )
}

private fun bubbleRow(area: JBTextArea) = JPanel(BorderLayout()).apply {
    background = Brand.surface.jb()
    add(area, BorderLayout.CENTER)
    border = BorderFactory.createEmptyBorder(2, 6, 2, 6)
}

class ChatToolWindowFactory : ToolWindowFactory {
    override fun createToolWindowContent(project: Project, toolWindow: ToolWindow) {
        val panel = ChatPanel(project)
        val contentFactory = toolWindow.contentManager.factory
        val content = contentFactory.createContent(panel, "", false)
        toolWindow.contentManager.addContent(content)
    }
}

private class ChatPanel(private val project: Project) : JPanel(BorderLayout()) {
    private val transcript = JPanel()
    private val transcriptScroll = JBScrollPane(transcript)
    private val input = JBTextArea(3, 40)
    private val sendBtn = JButton("Send")
    private val header = JBLabel(" ")
    private val turns = mutableListOf<ChatTurn>()

    init {
        border = BorderFactory.createEmptyBorder(6, 6, 6, 6)
        transcript.layout = BoxLayout(transcript, BoxLayout.Y_AXIS)
        transcript.background = Brand.surface.jb()
        transcriptScroll.preferredSize = Dimension(0, 400)
        transcriptScroll.viewport.background = Brand.surface.jb()

        val headerRow = Box.createHorizontalBox().apply {
            add(JBLabel(" Talyvor Chat ").apply {
                foreground = Brand.accent.jb()
            })
            add(Box.createHorizontalGlue())
            add(header)
        }
        add(headerRow, BorderLayout.NORTH)
        add(transcriptScroll, BorderLayout.CENTER)

        val composer = JPanel(BorderLayout()).apply {
            add(JBScrollPane(input), BorderLayout.CENTER)
            add(sendBtn, BorderLayout.EAST)
        }
        add(composer, BorderLayout.SOUTH)

        sendBtn.addActionListener(::onSend)
        input.lineWrap = true
        input.wrapStyleWord = true
        refreshHeader()
    }

    private fun refreshHeader() {
        val s = TalyvorSettings.getInstance()
        val issue = s.activeIssue.ifEmpty { "(no issue)" }
        val model = s.model
        header.text = " $issue · $model "
        sendBtn.isEnabled = s.lensUrl.isNotEmpty() && s.lensApiKey.isNotEmpty()
        if (!sendBtn.isEnabled) {
            header.text = " Not configured — Settings → Tools → Talyvor Code "
            header.foreground = Brand.critical.jb()
        } else {
            header.foreground = Brand.inkMuted.jb()
        }
    }

    private fun onSend(@Suppress("UNUSED_PARAMETER") e: ActionEvent) {
        val text = input.text.trim()
        if (text.isEmpty()) return
        refreshHeader()
        val s = TalyvorSettings.getInstance()
        if (s.lensUrl.isEmpty() || s.lensApiKey.isEmpty()) return
        val client = LensClient(s.lensUrl, s.lensApiKey)

        // Record + render the user turn, then snapshot the rolling
        // history (which now includes it) for the model.
        val userTurn = ChatTurn(role = "user", text = text)
        turns.add(userTurn)
        renderComponent(userTurn.toComponent())
        input.text = ""
        val history = turns.map { mapOf("role" to it.role, "content" to it.text) }

        // Render a live assistant bubble that grows as deltas arrive.
        val live = LiveAssistantTurn()
        renderComponent(live.component)
        sendBtn.isEnabled = false

        // acc accumulates the streamed reply so the final text can be
        // committed to history once the stream completes. Mutated on
        // the stream thread, read on the EDT inside invokeLater — the
        // ordering (append → invokeLater) keeps the EDT read consistent.
        val acc = StringBuilder()
        Thread {
            client.completeStream(
                messages = history,
                model = s.model,
                feature = "chat",
                workspaceId = s.workspaceId,
                issueId = s.activeIssue,
                callbacks = StreamCallbacks(
                    onChunk = { delta ->
                        acc.append(delta)
                        val snapshot = acc.toString()
                        ApplicationManager.getApplication().invokeLater {
                            live.setText(snapshot)
                            scrollToBottom()
                        }
                    },
                    onDone = { _, _ ->
                        val finalText = acc.toString()
                        ApplicationManager.getApplication().invokeLater {
                            // Commit the finished reply to history so the
                            // next turn carries the conversation context.
                            turns.add(ChatTurn(role = "assistant", text = finalText))
                            sendBtn.isEnabled = true
                        }
                    },
                    onError = { ex ->
                        ApplicationManager.getApplication().invokeLater {
                            sendBtn.isEnabled = true
                            Messages.showMessageDialog(
                                project,
                                ex.message ?: ex.toString(),
                                "Talyvor Error",
                                Messages.getErrorIcon(),
                            )
                        }
                    },
                ),
            )
        }.start()
    }

    // renderComponent appends a bubble (finished or live) to the
    // transcript on the EDT and keeps the view pinned to the bottom.
    private fun renderComponent(component: JPanel) {
        ApplicationManager.getApplication().invokeLater {
            transcript.add(component)
            transcript.add(Box.createVerticalStrut(6))
            transcript.revalidate()
            transcript.repaint()
            scrollToBottom()
        }
    }

    private fun scrollToBottom() {
        val bar = transcriptScroll.verticalScrollBar
        bar.value = bar.maximum
    }
}

// LiveAssistantTurn is a mutable assistant bubble whose text grows as
// streaming deltas arrive. Mirrors ChatTurn's assistant styling so a
// finished turn and a streaming one look identical.
private class LiveAssistantTurn {
    private val area = bubbleArea("", Brand.raised.jb())
    val component: JPanel = bubbleRow(area)

    fun setText(text: String) {
        area.text = text
    }
}

private data class ChatTurn(val role: String, val text: String) {
    fun toComponent(): JPanel {
        val bg = if (role == "user") Brand.accentTint.jb() else Brand.raised.jb()
        return bubbleRow(bubbleArea(text, bg))
    }
}
