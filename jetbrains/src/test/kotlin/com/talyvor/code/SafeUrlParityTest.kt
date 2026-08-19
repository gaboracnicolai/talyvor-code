// ONE RULE, THREE PORTS — AND THE THIRD WAS NOT IN THE HARNESS THAT UNIFIED THE OTHER TWO.
//
// testdata/safeurl-cases.json records the verdicts "a base URL it is safe to attach a Talyvor API key
// to" must produce. Its own header says the rule "is implemented TWICE", and so does the Go package
// doc. Both were written while a THIRD implementation shipped in this plugin — TalyvorSettings'
// private sanitizeBaseUrl, now SafeUrlPure — guarding the same key against the same hostile-config
// attack, asserted by nothing.
//
// This file asserts the Kotlin port against that shared file, exactly as
// agent/internal/safeurl/cases_parity_test.go and extension/src/safeurl-pure.test.ts assert theirs.
// Fixing one port alone now reds that port; editing the shared file alone reds all three.
//
// NOT IN THE TABLE, DELIBERATELY (inherited from the Go port's note): the empty URL. Go returns nil
// for it, and both string-returning ports return "" — one meaning in three type systems, not a
// disagreement in verdict.

package com.talyvor.code

import org.json.JSONObject
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import java.io.File

class SafeUrlParityTest {

    private data class SafeUrlCase(val url: String, val safe: Boolean, val why: String)

    // loadCases walks up for the shared file and FAILS LOUDLY when it is missing or short. A moved or
    // truncated manifest would otherwise leave this test asserting nothing — which is precisely the
    // state this port was already in, and the state the extension's half was in before the shared
    // file existed. A guard that reads no cases must not look like a guard that read them all.
    private fun loadCases(): List<SafeUrlCase> {
        var dir: File? = File(System.getProperty("user.dir")).absoluteFile
        repeat(10) {
            val d = dir ?: return@repeat
            val p = File(File(d, "testdata"), "safeurl-cases.json")
            if (p.isFile) {
                val root = JSONObject(p.readText())
                val arr = root.getJSONArray("cases")
                val out = ArrayList<SafeUrlCase>(arr.length())
                for (i in 0 until arr.length()) {
                    val c = arr.getJSONObject(i)
                    out.add(SafeUrlCase(c.getString("url"), c.getBoolean("safe"), c.optString("why")))
                }
                // A floor, not a count: the file may grow, but a shrunken table must not pass.
                // 29 cases at the commit that introduced it.
                if (out.size < 29) {
                    fail("${p.path} has ${out.size} cases, expected at least 29 — a shrunken table proves less than it claims")
                }
                return out
            }
            dir = d.parentFile
        }
        fail("testdata/safeurl-cases.json not found walking up from ${System.getProperty("user.dir")}")
        return emptyList()
    }

    @Test
    fun kotlinPortMatchesTheSharedCases() {
        val cases = loadCases()

        // Both verdicts must be represented, or a port that answered one way for everything would pass.
        val safe = cases.count { it.safe }
        val unsafe = cases.size - safe
        if (safe == 0 || unsafe == 0) {
            fail("table is one-sided ($safe safe, $unsafe unsafe) — it could not catch a constant answer")
        }

        val disagreements = ArrayList<String>()
        for (c in cases) {
            // The shipping path: TalyvorSettings' getters delegate to exactly this function.
            val got = SafeUrlPure.sanitizeBaseUrl(c.url) != ""
            if (got != c.safe) {
                disagreements.add(
                    "sanitizeBaseUrl(${quote(c.url)}) = safe:$got, shared table says safe:${c.safe}\n    why: ${c.why}"
                )
            }
        }
        assertTrue(
            "the Kotlin port disagrees with the shared table on ${disagreements.size} of ${cases.size} cases:\n  " +
                disagreements.joinToString("\n  "),
            disagreements.isEmpty()
        )
    }

    // THE RULE BEING RIGHT AND THE PLUGIN USING IT ARE TWO CLAIMS, AND THE TEST ABOVE MAKES ONLY THE
    // FIRST. Measured: with SafeUrlPure correct and TalyvorSettings holding a private copy of the old
    // broken rule instead of delegating, the case above stays GREEN while every URL the plugin
    // actually reads is sanitized by the copy. So this drives the SETTINGS PROPERTY — the surface
    // LensClient and the Configurable read — over the two verdicts that separate the rules.
    //
    // TalyvorSettings is constructed directly rather than through service(): the URL getters touch
    // only its own State, and reaching for the application container would make this a platform test
    // for no gain. The API-key properties are the ones that need PasswordSafe, and nothing here
    // touches them.
    @Test
    fun settingsPropertiesUseTheSharedRule() {
        val cases = loadCases()
        val disagreements = ArrayList<String>()
        for (c in cases) {
            val s = TalyvorSettings()
            s.lensUrl = c.url
            val lensSafe = s.lensUrl != ""
            s.trackUrl = c.url
            val trackSafe = s.trackUrl != ""
            if (lensSafe != c.safe) disagreements.add("lensUrl=${quote(c.url)} -> safe:$lensSafe, table says safe:${c.safe}")
            if (trackSafe != c.safe) disagreements.add("trackUrl=${quote(c.url)} -> safe:$trackSafe, table says safe:${c.safe}")
        }
        assertTrue(
            "the settings properties disagree with the shared table on ${disagreements.size} reads — " +
                "the plugin is not using the rule this file pins:\n  " + disagreements.joinToString("\n  "),
            disagreements.isEmpty()
        )
    }

    private fun quote(s: String) = "\"" + s + "\""
}
