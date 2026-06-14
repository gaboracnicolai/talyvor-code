// Pure shell-command helpers — Kotlin port of the VS Code extension's
// commands/shell-pure.ts (which mirrors the Go agent's internal/shell).
// No IntelliJ Platform dependency, so shell detection, OS mapping,
// prompt building, output post-processing, and the advisory safety
// screen are all unit-tested with plain JUnit (see ShellPureTest).
//
// isCommandSafe is ADVISORY — a match drives an extra "review
// carefully" warning, never a hard block — matching the other surfaces.

package com.talyvor.code.shell

object ShellPure {

    /**
     * detectShell extracts the shell name from a $SHELL-style path or a
     * raw shell token, defaulting to "bash". Whitespace is not used to
     * split the path (Windows shell paths legitimately contain spaces);
     * a trailing argv token (e.g. "-i") is stripped after taking the
     * basename, and a ".exe" suffix is removed.
     */
    fun detectShell(shellPath: String?): String {
        if (shellPath.isNullOrEmpty()) return "bash"
        val base = shellPath.split(Regex("[\\\\/]")).filter { it.isNotEmpty() }.lastOrNull()
            ?: return "bash"
        val head = base.split(Regex("\\s+"))[0]
        return head.replace(Regex("\\.exe$", RegexOption.IGNORE_CASE), "")
    }

    /** detectOS maps a process.platform-style token to a friendly name. */
    fun detectOS(platform: String): String =
        when (platform) {
            "darwin" -> "macOS"
            "linux" -> "Linux"
            "win32", "windows" -> "Windows"
            "freebsd" -> "FreeBSD"
            "openbsd" -> "OpenBSD"
            else -> platform
        }

    /**
     * buildShellPrompt constrains the model to a single command, no
     * fences, no prose — the same rules the Go + VS Code sides use so
     * output is consistent across surfaces.
     */
    fun buildShellPrompt(description: String, shell: String, osName: String): String =
        """You are an expert shell command generator.
Generate a single shell command for the given task.

Rules:
- Return ONLY the command, nothing else
- No markdown, no backticks, no explanation
- Use $shell syntax
- The command must work on $osName
- Prefer safe commands (no -rf without confirmation)
- For complex tasks: pipe commands together
- If the task is impossible in one command, return the most practical approach

Task: $description"""

    // preambles mirror the TS list. Order matters — the stripping loop
    // runs each in turn until the string stabilises.
    private val preambles = listOf(
        Regex("^(here(?:'s| is)?\\s+(the\\s+)?(command|shell command)\\s*:?\\s*)", RegexOption.IGNORE_CASE),
        Regex("^(the command (you want|is)\\s*:?\\s*)", RegexOption.IGNORE_CASE),
        Regex("^(command\\s*:?\\s*)", RegexOption.IGNORE_CASE),
    )

    /**
     * stripGenerated removes fences, preambles, and surrounding
     * whitespace, then returns the first non-empty line (commands should
     * be single-line). Loops until stable because a preamble can hide a
     * fence behind it.
     */
    fun stripGenerated(s: String): String {
        var out = s.trim()
        repeat(4) {
            var next = out
            for (re in preambles) next = next.replace(re, "")
            next = next.trim()
            if (next.startsWith("```")) {
                val nl = next.indexOf("\n")
                if (nl >= 0) next = next.substring(nl + 1)
            }
            if (next.endsWith("```")) next = next.substring(0, next.length - 3)
            next = next.trim()
            if (next.startsWith("`") && next.endsWith("`") && next.length > 1) {
                next = next.substring(1, next.length - 1).trim()
            }
            if (next == out) return@repeat
            out = next
        }
        for (line in out.split("\n")) {
            if (line.trim().isNotEmpty()) return line.trim()
        }
        return out.trim()
    }

    // dangerousPatterns mirrors the TS/Go list. Advisory only — a match
    // triggers an extra warning, never a hard block. Braces are escaped
    // (\{ \}) for Java-regex safety; the literal `$` in the second
    // pattern is embedded via ${'$'}.
    private val dangerousPatterns = listOf(
        Regex("""\brm\b[^|;\n]*\s-[rR]?[fF]?[rR]?\s+(/|~|/\*|/etc\b|/usr\b|/var\b|/home\b)"""),
        Regex("""\brm\b\s+-[a-zA-Z]*[fF][a-zA-Z]*\s+/(\s|${'$'}|\*)"""),
        Regex("""\bdd\b[^|;\n]*\bof=/dev/"""),
        Regex("""\bchmod\b\s+(-[a-zA-Z]+\s+)?777\b\s+(/|~|/etc\b|/usr\b)"""),
        Regex("""\bmkfs\.[a-z0-9]+\b\s+/dev/"""),
        Regex(""">\s*/dev/sd[a-z]"""),
        Regex(""":\(\)\s*\{\s*:\|:&\s*\}\s*;\s*:"""),
        Regex("""\bshutdown\b\s+-[a-zA-Z]*[hH]"""),
        Regex("""\bcurl\b[^|;\n]*\|\s*(sudo\s+)?sh\b"""),
    )

    /** isCommandSafe returns false when the command matches a known-dangerous pattern. */
    fun isCommandSafe(command: String): Boolean =
        dangerousPatterns.none { it.containsMatchIn(command) }
}
