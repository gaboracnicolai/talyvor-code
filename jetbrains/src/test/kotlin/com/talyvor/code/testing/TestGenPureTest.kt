// Pure unit tests for the test-generation helpers. Mirrors the VS
// Code extension's extension/test/test-generator.test.ts so the
// filename mapping, framework labels, prompts, and the sanitiser
// behave identically across surfaces. Adds coverage for the
// JetBrains-only canonicalLanguageId derivation.

package com.talyvor.code.testing

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class TestGenPureTest {

    // ─── suggestTestFileName ────────────────────────────

    @Test
    fun suggestTypeScriptFiles() {
        assertEquals("src/auth.test.ts", TestGenPure.suggestTestFileName("src/auth.ts", "typescript"))
        assertEquals(
            "/repo/src/auth.test.tsx",
            TestGenPure.suggestTestFileName("/repo/src/auth.tsx", "typescriptreact"),
        )
    }

    @Test
    fun suggestJavaScriptFiles() {
        assertEquals("util.test.js", TestGenPure.suggestTestFileName("util.js", "javascript"))
        assertEquals("App.test.jsx", TestGenPure.suggestTestFileName("App.jsx", "javascriptreact"))
    }

    @Test
    fun suggestGoFile() {
        assertEquals("/x/foo_test.go", TestGenPure.suggestTestFileName("/x/foo.go", "go"))
    }

    @Test
    fun suggestPythonFile() {
        assertEquals("pkg/test_auth.py", TestGenPure.suggestTestFileName("pkg/auth.py", "python"))
    }

    @Test
    fun suggestRubyFile() {
        assertEquals("auth_spec.rb", TestGenPure.suggestTestFileName("auth.rb", "ruby"))
    }

    @Test
    fun suggestRustFile() {
        assertEquals("src/lib_test.rs", TestGenPure.suggestTestFileName("src/lib.rs", "rust"))
    }

    @Test
    fun suggestJavaFilePascalCases() {
        assertEquals("src/AuthTest.java", TestGenPure.suggestTestFileName("src/Auth.java", "java"))
        assertEquals(
            "AuthServiceTest.java",
            TestGenPure.suggestTestFileName("auth_service.java", "java"),
        )
    }

    @Test
    fun suggestSwiftFile() {
        assertEquals("AuthTests.swift", TestGenPure.suggestTestFileName("auth.swift", "swift"))
    }

    @Test
    fun suggestFallbackFile() {
        assertEquals(
            "script.test.sh",
            TestGenPure.suggestTestFileName("script.sh", "shellscript"),
        )
    }

    @Test
    fun suggestWindowsSeparatorRetained() {
        assertEquals(
            "C:\\repo\\src\\auth.test.ts",
            TestGenPure.suggestTestFileName("C:\\repo\\src\\auth.ts", "typescript"),
        )
    }

    // ─── frameworkFor ───────────────────────────────────

    @Test
    fun frameworkForKnownAndFallback() {
        assertEquals("Jest", TestGenPure.frameworkFor("typescript"))
        assertEquals("Go testing", TestGenPure.frameworkFor("go"))
        assertEquals("pytest", TestGenPure.frameworkFor("python"))
        assertEquals("JUnit", TestGenPure.frameworkFor("java"))
        assertEquals("Generic", TestGenPure.frameworkFor("unknown"))
    }

    // ─── systemPromptFor ────────────────────────────────

    @Test
    fun systemPromptForKnownLanguages() {
        assertTrue(TestGenPure.systemPromptFor("typescript").contains("Jest"))
        assertTrue(TestGenPure.systemPromptFor("go").contains("testing"))
        assertTrue(TestGenPure.systemPromptFor("python").contains("pytest"))
        for (lang in listOf("typescript", "go", "python", "ruby", "rust", "java", "swift")) {
            assertTrue(
                "$lang prompt missing ONLY guard",
                TestGenPure.systemPromptFor(lang).contains("ONLY"),
            )
        }
    }

    // ─── buildTestPrompt ────────────────────────────────

    @Test
    fun buildTestPromptIncludesContext() {
        val code = "export function add(a: number, b: number) { return a + b }"
        val p = TestGenPure.buildTestPrompt(code, "typescript", "math.ts")
        assertTrue("language tag", p.contains("typescript"))
        assertTrue("file name", p.contains("math.ts"))
        assertTrue("code body", p.contains(code))
        assertTrue("code fence", p.contains("```typescript"))
    }

    // ─── sanitiseGenerated ──────────────────────────────

    @Test
    fun sanitiseStripsLeadingFence() {
        val out = TestGenPure.sanitiseGenerated("```ts\nconst x = 1;\n```")
        assertFalse("fence still present: $out", out.contains("```"))
        assertTrue("body lost", out.contains("const x = 1;"))
    }

    @Test
    fun sanitiseStripsPreamble() {
        val out = TestGenPure.sanitiseGenerated("Here are the tests:\n```ts\nconst x = 1;\n```")
        assertFalse("preamble survived", out.lowercase().contains("here are the tests"))
        assertTrue(out.contains("const x = 1;"))
    }

    @Test
    fun sanitiseHandlesNoFences() {
        val out = TestGenPure.sanitiseGenerated("const x = 1;\nconst y = 2;")
        assertTrue("first line lost", out.contains("const x = 1;"))
        assertTrue("second line lost", out.contains("const y = 2;"))
    }

    // ─── canonicalLanguageId (JetBrains-only) ───────────

    @Test
    fun canonicalLanguageIdMapsExtensions() {
        assertEquals("typescript", TestGenPure.canonicalLanguageId("auth.ts"))
        assertEquals("typescriptreact", TestGenPure.canonicalLanguageId("App.tsx"))
        assertEquals("javascript", TestGenPure.canonicalLanguageId("util.mjs"))
        assertEquals("go", TestGenPure.canonicalLanguageId("/x/main.go"))
        assertEquals("python", TestGenPure.canonicalLanguageId("svc.py"))
        assertEquals("kotlin", TestGenPure.canonicalLanguageId("Main.kt"))
        assertEquals("cpp", TestGenPure.canonicalLanguageId("engine.cc"))
        // Unknown extension passes through; no extension yields "".
        assertEquals("sh", TestGenPure.canonicalLanguageId("deploy.sh"))
        assertEquals("", TestGenPure.canonicalLanguageId("Makefile"))
    }
}
