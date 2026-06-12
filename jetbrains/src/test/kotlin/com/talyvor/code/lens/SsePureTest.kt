// Pure unit tests for the SSE helpers. Mirrors the VS Code
// extension's extension/test/streaming.test.ts (and, transitively,
// the Go agent's internal/lens/client_test.go) one-for-one so the
// three surfaces parse identical streams identically.
//
// No IntelliJ Platform API is touched — these run under Gradle's
// default `test` task as ordinary JVM tests.

package com.talyvor.code.lens

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class SsePureTest {

    // ─── SseBuffer ────────────────────────────────────

    @Test
    fun bufferSplitsOnBlankLine() {
        val buf = SseBuffer()
        val a = buf.push("data: hello\n")
        assertEquals("no event yet", 0, a.size)
        val b = buf.push("\ndata: world\n\n")
        assertEquals("two events", 2, b.size)
        assertTrue("first event", b[0].contains("hello"))
        assertTrue("second event", b[1].contains("world"))
    }

    @Test
    fun bufferIgnoresEmptyBlocks() {
        assertEquals(0, SseBuffer().push("\n\n\n\n").size)
    }

    @Test
    fun bufferFlushesTrailingEvent() {
        val buf = SseBuffer()
        buf.push("data: tail")
        val tail = buf.flush()
        assertEquals(1, tail.size)
        assertTrue(tail[0].contains("tail"))
    }

    // ─── extractData ──────────────────────────────────

    @Test
    fun extractData() {
        assertEquals("hello", SsePure.extractData("data: hello"))
        assertEquals("payload", SsePure.extractData("event: ping\ndata: payload"))
        assertEquals("", SsePure.extractData("comment only"))
    }

    // ─── Anthropic events ─────────────────────────────

    @Test
    fun parseAnthropicTextDelta() {
        val ev = SsePure.parseEvent(
            """{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}""",
            StreamProvider.ANTHROPIC,
        )
        assertEquals(StreamEventKind.TEXT, ev?.kind)
        assertEquals("Hello", ev?.text)
    }

    @Test
    fun parseAnthropicMessageStartUsage() {
        val ev = SsePure.parseEvent(
            """{"type":"message_start","message":{"usage":{"input_tokens":120}}}""",
            StreamProvider.ANTHROPIC,
        )
        assertEquals(StreamEventKind.DONE, ev?.kind)
        assertEquals(120, ev?.inputTokens)
    }

    @Test
    fun parseAnthropicMessageDeltaUsage() {
        val ev = SsePure.parseEvent(
            """{"type":"message_delta","usage":{"output_tokens":42}}""",
            StreamProvider.ANTHROPIC,
        )
        assertEquals(StreamEventKind.DONE, ev?.kind)
        assertEquals(42, ev?.outputTokens)
    }

    @Test
    fun parseAnthropicErrorEvent() {
        val ev = SsePure.parseEvent(
            """{"type":"error","error":{"message":"upstream exploded"}}""",
            StreamProvider.ANTHROPIC,
        )
        assertEquals(StreamEventKind.ERROR, ev?.kind)
        assertTrue(ev?.message?.contains("exploded") == true)
    }

    @Test
    fun parseDoneSentinel() {
        assertEquals(StreamEventKind.DONE, SsePure.parseEvent("[DONE]", StreamProvider.ANTHROPIC)?.kind)
    }

    @Test
    fun parseInvalidJsonReturnsNull() {
        assertNull(SsePure.parseEvent("not json", StreamProvider.ANTHROPIC))
    }

    // ─── OpenAI events ────────────────────────────────

    @Test
    fun parseOpenAITextDelta() {
        val ev = SsePure.parseEvent(
            """{"choices":[{"delta":{"content":"Hello"}}]}""",
            StreamProvider.OPENAI,
        )
        assertEquals(StreamEventKind.TEXT, ev?.kind)
        assertEquals("Hello", ev?.text)
    }

    @Test
    fun parseOpenAIUsageFinal() {
        val ev = SsePure.parseEvent(
            """{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}""",
            StreamProvider.OPENAI,
        )
        assertEquals(StreamEventKind.DONE, ev?.kind)
        assertEquals(10, ev?.inputTokens)
        assertEquals(5, ev?.outputTokens)
    }

    @Test
    fun parseOpenAIError() {
        val ev = SsePure.parseEvent(
            """{"error":{"message":"rate limited"}}""",
            StreamProvider.OPENAI,
        )
        assertEquals(StreamEventKind.ERROR, ev?.kind)
        assertTrue(ev?.message?.contains("rate") == true)
    }

    // ─── providerForModel ─────────────────────────────

    @Test
    fun providerMapping() {
        assertEquals(StreamProvider.OPENAI, SsePure.providerForModel("gpt-4o"))
        assertEquals(StreamProvider.OPENAI, SsePure.providerForModel("gpt-4o-mini"))
        assertEquals(StreamProvider.OPENAI, SsePure.providerForModel("o1"))
        assertEquals(StreamProvider.OPENAI, SsePure.providerForModel("o3-mini"))
        assertEquals(StreamProvider.ANTHROPIC, SsePure.providerForModel("claude-sonnet-4-6"))
        assertEquals(StreamProvider.ANTHROPIC, SsePure.providerForModel("mistral-large"))
        assertEquals(StreamProvider.ANTHROPIC, SsePure.providerForModel(""))
        assertEquals(StreamProvider.ANTHROPIC, SsePure.providerForModel(null))
    }

    // ─── Streaming accumulation ───────────────────────

    @Test
    fun accumulatesTextAcrossChunks() {
        val buf = SseBuffer()
        val raw = listOf(
            """data: {"type":"message_start","message":{"usage":{"input_tokens":5}}}""",
            "",
            """data: {"type":"content_block_delta","delta":{"text":"Hello "}}""",
            "",
            """data: {"type":"content_block_delta","delta":{"text":"world"}}""",
            "",
            """data: {"type":"message_delta","usage":{"output_tokens":2}}""",
            "",
            "data: [DONE]",
            "",
        ).joinToString("\n")

        var text = ""
        var inputTokens = 0
        var outputTokens = 0
        var done = false
        for (block in buf.push(raw)) {
            val ev = SsePure.parseEvent(SsePure.extractData(block), StreamProvider.ANTHROPIC) ?: continue
            when (ev.kind) {
                StreamEventKind.TEXT -> ev.text?.let { text += it }
                StreamEventKind.DONE -> {
                    done = true
                    if (ev.inputTokens != 0) inputTokens = ev.inputTokens
                    if (ev.outputTokens != 0) outputTokens = ev.outputTokens
                }
                StreamEventKind.ERROR -> {}
            }
        }
        assertEquals("Hello world", text)
        assertTrue("done seen", done)
        assertEquals(5, inputTokens)
        assertEquals(2, outputTokens)
    }
}
