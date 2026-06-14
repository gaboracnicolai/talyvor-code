// Pure SSE-parsing helpers — Kotlin port of the VS Code extension's
// lens/sse-pure.ts (which itself mirrors the Go agent's
// internal/lens SSE readers). vscode/IDE-free so the buffer, the
// per-format event parsers, and the provider router can be exercised
// with plain JUnit (see SsePureTest) without an IntelliJ sandbox.
//
// JSON parsing uses org.json (already a plugin dependency) — no new
// dependency is introduced.

package com.talyvor.code.lens

import org.json.JSONObject

enum class StreamProvider { ANTHROPIC, OPENAI }

enum class StreamEventKind { TEXT, DONE, ERROR }

/**
 * StreamEvent mirrors the StreamEvent interface in sse-pure.ts. Token
 * counts default to 0 (the TS optional-undefined equivalent for the
 * caller's `if (ev.inputTokens)` accumulation pattern).
 */
data class StreamEvent(
    val kind: StreamEventKind,
    val text: String? = null,
    val inputTokens: Int = 0,
    val outputTokens: Int = 0,
    val message: String? = null,
)

/**
 * SseBuffer accumulates chunks from a streamed response and splits
 * them on the SSE blank-line (`\n\n`) delimiter. Each `push` returns
 * the events finalised by the new bytes; `flush` returns any trailing
 * event a server ended without the canonical terminator.
 */
class SseBuffer {
    private val buffer = StringBuilder()

    fun push(chunk: String): List<String> {
        buffer.append(chunk)
        val out = mutableListOf<String>()
        while (true) {
            val idx = buffer.indexOf("\n\n")
            if (idx < 0) break
            val raw = buffer.substring(0, idx)
            buffer.delete(0, idx + 2)
            if (raw.trim().isEmpty()) continue
            out.add(raw)
        }
        return out
    }

    fun flush(): List<String> {
        val tail = buffer.toString()
        buffer.setLength(0)
        return if (tail.trim().isNotEmpty()) listOf(tail) else emptyList()
    }
}

object SsePure {
    private val LINE_SPLIT = Regex("\\r?\\n")

    /**
     * extractData pulls the `data:` payload from a raw SSE event block
     * (which may include other lines like `event: foo`). Returns "" when
     * no data line is present.
     */
    fun extractData(raw: String): String {
        for (line in raw.split(LINE_SPLIT)) {
            if (line.startsWith("data:")) {
                return line.substring(5).trim()
            }
        }
        return ""
    }

    /**
     * parseEvent maps one SSE payload to a StreamEvent for the supplied
     * provider. `[DONE]` always yields a done event; malformed JSON
     * yields null so callers can skip it.
     */
    fun parseEvent(payload: String, provider: StreamProvider): StreamEvent? {
        if (payload == "[DONE]") return StreamEvent(StreamEventKind.DONE)
        val obj: JSONObject = try {
            JSONObject(payload)
        } catch (_: Exception) {
            return null
        }
        return if (provider == StreamProvider.ANTHROPIC) parseAnthropic(obj) else parseOpenAI(obj)
    }

    private fun parseAnthropic(obj: JSONObject): StreamEvent? {
        when (obj.optString("type", "")) {
            "content_block_delta" -> {
                val text = obj.optJSONObject("delta")?.optString("text", "").orEmpty()
                return if (text.isNotEmpty()) StreamEvent(StreamEventKind.TEXT, text = text) else null
            }
            "message_start" -> {
                val inputTokens = obj.optJSONObject("message")
                    ?.optJSONObject("usage")
                    ?.optInt("input_tokens", 0) ?: 0
                return if (inputTokens > 0) {
                    StreamEvent(StreamEventKind.DONE, inputTokens = inputTokens)
                } else {
                    null
                }
            }
            "message_delta" -> {
                val outputTokens = obj.optJSONObject("usage")?.optInt("output_tokens", 0) ?: 0
                return if (outputTokens > 0) {
                    StreamEvent(StreamEventKind.DONE, outputTokens = outputTokens)
                } else {
                    null
                }
            }
            "message_stop" -> return StreamEvent(StreamEventKind.DONE)
            "error" -> {
                val message = obj.optJSONObject("error")?.optString("message", "stream error")
                    ?: "stream error"
                return StreamEvent(StreamEventKind.ERROR, message = message)
            }
        }
        return null
    }

    private fun parseOpenAI(obj: JSONObject): StreamEvent? {
        if (obj.has("error") && !obj.isNull("error")) {
            val message = obj.optJSONObject("error")?.optString("message", "stream error")
                ?: "stream error"
            return StreamEvent(StreamEventKind.ERROR, message = message)
        }
        val choices = obj.optJSONArray("choices")
        if (choices != null) {
            for (i in 0 until choices.length()) {
                val content = choices.optJSONObject(i)
                    ?.optJSONObject("delta")
                    ?.optString("content", "")
                    .orEmpty()
                if (content.isNotEmpty()) {
                    return StreamEvent(StreamEventKind.TEXT, text = content)
                }
            }
        }
        val usage = obj.optJSONObject("usage")
        if (usage != null) {
            return StreamEvent(
                StreamEventKind.DONE,
                inputTokens = usage.optInt("prompt_tokens", 0),
                outputTokens = usage.optInt("completion_tokens", 0),
            )
        }
        return null
    }

    /**
     * providerForModel mirrors agent/internal/lens.isOpenAIModel and the
     * TS providerForModel so the IDE streams from the same endpoint the
     * CLI would pick.
     */
    fun providerForModel(model: String?): StreamProvider {
        val m = (model ?: "").trim().lowercase()
        return if (m.startsWith("gpt-") || m.startsWith("o1") || m.startsWith("o3")) {
            StreamProvider.OPENAI
        } else {
            StreamProvider.ANTHROPIC
        }
    }
}
