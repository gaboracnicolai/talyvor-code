// Pure unit tests for the model catalogue. Mirrors the VS Code
// extension's extension/test/model.test.ts and the Go agent's
// internal/model/selector_test.go so the three surfaces stay aligned.
//
// These tests touch no IntelliJ Platform API, so they run under
// Gradle's default `test` task as ordinary JVM tests — no IDE
// sandbox required.

package com.talyvor.code.model

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ModelsTest {

    @Test
    fun listContainsExpectedModels() {
        val ids = Models.list().map { it.id }
        for (want in listOf(
            "claude-haiku-4-5",
            "claude-sonnet-4-6",
            "claude-opus-4-6",
            "gpt-4o",
            "gpt-4o-mini",
            "mistral-large",
        )) {
            assertTrue("missing model $want", ids.contains(want))
        }
    }

    @Test
    fun getReturnsProfile() {
        val m = Models.get("claude-sonnet-4-6")
        assertTrue("expected profile", m != null)
        assertEquals("Anthropic", m!!.provider)
        assertEquals(SpeedTier.BALANCED, m.speedTier)
    }

    @Test
    fun getUnknownReturnsNull() {
        assertNull("null for unknown", Models.get("nope"))
        assertNull("null for empty", Models.get(""))
        assertNull("null for whitespace", Models.get("   "))
        assertNull("null for null", Models.get(null))
    }

    @Test
    fun defaultsMatchAgentBehavior() {
        // These pairings must match agent/internal/model/selector.go
        // and models-pure.ts exactly so no surface drifts.
        val cases = mapOf(
            "completion" to "claude-haiku-4-5",
            "completions" to "claude-haiku-4-5",
            "shell" to "claude-haiku-4-5",
            "commit" to "claude-haiku-4-5",
            "ask" to "claude-haiku-4-5",
            "chat" to "claude-sonnet-4-6",
            "test" to "claude-sonnet-4-6",
            "tests" to "claude-sonnet-4-6",
            "test-gen" to "claude-sonnet-4-6",
            "review" to "claude-sonnet-4-6",
            "run" to "claude-sonnet-4-6",
            "agent" to "claude-sonnet-4-6",
            "unknown" to "claude-haiku-4-5",
        )
        for ((cmd, want) in cases) {
            assertEquals("defaultForCommand($cmd)", want, Models.defaultForCommand(cmd))
        }
    }

    @Test
    fun resolveModelPriority() {
        // 1. Explicit setting wins over the per-command default.
        assertEquals("gpt-4o", Models.resolveModel("gpt-4o", "chat"))
        // 2. Default applied when the setting is empty.
        assertEquals("claude-sonnet-4-6", Models.resolveModel("", "tests"))
        // 3. Whitespace-only setting is treated as unset.
        assertEquals("claude-haiku-4-5", Models.resolveModel("   ", "completions"))
        // 4. A surrounding-whitespace setting is trimmed and wins.
        assertEquals("gpt-4o", Models.resolveModel("  gpt-4o  ", "chat"))
        // 5. A null setting falls back to the default.
        assertEquals("claude-sonnet-4-6", Models.resolveModel(null, "agent"))
    }

    @Test
    fun catalogueShapeIsComplete() {
        for (m in Models.KNOWN) {
            assertTrue("id empty", m.id.isNotEmpty())
            assertTrue("displayName empty: ${m.id}", m.displayName.isNotEmpty())
            assertTrue("provider empty: ${m.id}", m.provider.isNotEmpty())
            assertTrue("bestFor empty: ${m.id}", m.bestFor.isNotEmpty())
        }
    }
}
