// Pure test-generation helpers — Kotlin port of the VS Code
// extension's providers/test-generator-pure.ts. No IntelliJ Platform
// dependency, so the filename mapping, framework labels, language
// prompts, and the generated-output sanitiser are all unit-tested with
// plain JUnit (see TestGenPureTest).
//
// One addition over the TS source: canonicalLanguageId maps a file
// name to the same canonical language ids the TS side receives for
// free from vscode.document.languageId. The IntelliJ action derives
// the id from the file extension instead.

package com.talyvor.code.testing

object TestGenPure {

    /**
     * suggestTestFileName maps a source file path to the conventional
     * test-file companion for its language. The directory is preserved
     * (only the basename changes) so the new file lands beside the
     * source. Handles both POSIX and Windows separators.
     */
    fun suggestTestFileName(sourcePath: String, languageId: String): String {
        val (dir, base, ext) = splitPath(sourcePath)
        val stem = if (ext.isNotEmpty() && base.endsWith(ext)) {
            base.substring(0, base.length - ext.length)
        } else {
            base
        }
        return when (languageId) {
            "typescript" -> joinPath(dir, "$stem.test.ts")
            "typescriptreact" -> joinPath(dir, "$stem.test.tsx")
            "javascript" -> joinPath(dir, "$stem.test.js")
            "javascriptreact" -> joinPath(dir, "$stem.test.jsx")
            "go" -> joinPath(dir, "${stem}_test.go")
            "python" -> joinPath(dir, "test_$stem.py")
            "ruby" -> joinPath(dir, "${stem}_spec.rb")
            "rust" -> joinPath(dir, "${stem}_test.rs")
            "java" -> joinPath(dir, "${pascal(stem)}Test.java")
            "kotlin" -> joinPath(dir, "${pascal(stem)}Test.kt")
            "swift" -> joinPath(dir, "${pascal(stem)}Tests.swift")
            "c" -> joinPath(dir, "${stem}_test.c")
            "cpp" -> joinPath(dir, "${stem}_test.cpp")
            else -> joinPath(dir, "$stem.test$ext")
        }
    }

    /** frameworkFor returns the canonical framework label per language. */
    fun frameworkFor(languageId: String): String =
        when (languageId) {
            "typescript", "typescriptreact", "javascript", "javascriptreact" -> "Jest"
            "go" -> "Go testing"
            "python" -> "pytest"
            "ruby" -> "RSpec"
            "rust" -> "Rust #[cfg(test)]"
            "java" -> "JUnit"
            "kotlin" -> "JUnit (Kotlin)"
            "swift" -> "XCTest"
            "c", "cpp" -> "Generic test harness"
            else -> "Generic"
        }

    /**
     * systemPromptFor returns the language-tailored system prompt. Each
     * variant pins the framework and the "return only code" guard so the
     * sanitiser has less to clean up.
     */
    fun systemPromptFor(languageId: String): String =
        when (languageId) {
            "typescript", "typescriptreact", "javascript", "javascriptreact" ->
                "Generate Jest tests for the following code. Use describe/it blocks. " +
                    "Include happy-path, edge-case, and error-case tests. Use TypeScript " +
                    "syntax when the source is TypeScript. Import the module correctly. " +
                    "Return ONLY the test code — no prose, no fences, no explanation."
            "go" ->
                "Generate Go tests using the standard `testing` package. Prefer " +
                    "table-driven tests when there are multiple cases. Name tests " +
                    "Test<FunctionName>. Use t.Helper() in shared fixtures. Return " +
                    "ONLY the test code — no prose, no fences, no explanation."
            "python" ->
                "Generate pytest tests. Use descriptive test_* function names + " +
                    "fixtures where they make the suite cleaner. Cover happy path, " +
                    "edge cases, and error cases. Return ONLY the test code — no prose, " +
                    "no fences, no explanation."
            "ruby" ->
                "Generate RSpec tests. Use describe/context/it blocks. Return ONLY " +
                    "the test code — no prose, no fences, no explanation."
            "rust" ->
                "Generate a Rust #[cfg(test)] module covering the supplied code. " +
                    "Use #[test] functions and assert!/assert_eq!. Return ONLY the " +
                    "test code — no prose, no fences, no explanation."
            "java", "kotlin" ->
                "Generate JUnit 5 tests. One @Test method per scenario; use " +
                    "@DisplayName when the method name doesn't convey intent. Return " +
                    "ONLY the test code — no prose, no fences, no explanation."
            "swift" ->
                "Generate XCTest tests. Inherit from XCTestCase; use test* method " +
                    "names. Return ONLY the test code — no prose, no fences, no " +
                    "explanation."
            else ->
                "Generate a thorough test suite for the following code using the " +
                    "idiomatic testing framework for the language. Return ONLY the test " +
                    "code — no prose, no fences, no explanation."
        }

    /**
     * buildTestPrompt is the user-side payload that follows the system
     * prompt. The language fence helps the model lock onto syntax.
     */
    fun buildTestPrompt(code: String, languageId: String, fileName: String): String =
        listOf(
            "Generate tests for this $languageId file:",
            "File: $fileName",
            "",
            "```$languageId",
            code,
            "```",
        ).joinToString("\n")

    /**
     * sanitiseGenerated strips the boilerplate models add even when told
     * not to: leading "Here are the tests:" preambles, opening/closing
     * code fences, and trailing whitespace. Internal whitespace (the
     * test's indentation) is preserved.
     */
    fun sanitiseGenerated(text: String): String {
        var out = text
        out = out.replace(
            Regex(
                "^\\s*(here (are|is) (the )?tests?|tests?)\\s*:?\\s*\\n+",
                RegexOption.IGNORE_CASE,
            ),
            "",
        )
        Regex("^\\s*```[a-zA-Z0-9_+-]*\\n").find(out)?.let { out = out.substring(it.value.length) }
        Regex("\\n```\\s*$").find(out)?.let { out = out.substring(0, out.length - it.value.length) }
        return out.trimStart().replace(Regex("\\s+$"), "\n")
    }

    /**
     * canonicalLanguageId maps a file name to the canonical language id
     * the prompt + filename helpers expect. The VS Code side gets this
     * from vscode.document.languageId; the IntelliJ action derives it
     * from the extension instead. Unknown extensions return the raw
     * extension so the generic fallbacks still apply.
     */
    fun canonicalLanguageId(fileName: String): String {
        val dot = fileName.lastIndexOf('.')
        val ext = if (dot >= 0) fileName.substring(dot + 1).lowercase() else ""
        return when (ext) {
            "ts" -> "typescript"
            "tsx" -> "typescriptreact"
            "js", "mjs", "cjs" -> "javascript"
            "jsx" -> "javascriptreact"
            "go" -> "go"
            "py" -> "python"
            "rb" -> "ruby"
            "rs" -> "rust"
            "java" -> "java"
            "kt", "kts" -> "kotlin"
            "swift" -> "swift"
            "c", "h" -> "c"
            "cpp", "cc", "cxx", "hpp", "hxx" -> "cpp"
            else -> ext
        }
    }

    // ─── tiny path helpers ─────────────────────────────

    private data class PathParts(val dir: String, val base: String, val ext: String)

    private fun splitPath(p: String): PathParts {
        val sepIdx = maxOf(p.lastIndexOf('/'), p.lastIndexOf('\\'))
        val dir = if (sepIdx >= 0) p.substring(0, sepIdx) else ""
        val base = if (sepIdx >= 0) p.substring(sepIdx + 1) else p
        val dot = base.lastIndexOf('.')
        val ext = if (dot > 0) base.substring(dot) else ""
        return PathParts(dir, base, ext)
    }

    private fun joinPath(dir: String, file: String): String {
        if (dir.isEmpty()) return file
        val sep = if (dir.contains('\\') && !dir.contains('/')) "\\" else "/"
        return dir + sep + file
    }

    private fun pascal(s: String): String =
        s.split(Regex("[-_]+"))
            .filter { it.isNotEmpty() }
            .joinToString("") { w -> w.replaceFirstChar { it.uppercaseChar() } }
}
