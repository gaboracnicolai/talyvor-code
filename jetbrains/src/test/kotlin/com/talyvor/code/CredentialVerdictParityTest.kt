// ONE RULE, THREE PORTS — asserted from one file, the way safeurl-cases.json already is.
//
// testdata/credential-verdict-cases.json records what a client may conclude about the user's API
// key from the HTTP status of GET /v1/auth/me. The Go port asserts itself against it in
// agent/internal/lens/credential_cases_parity_test.go and the TypeScript port in
// extension/src/lens/credential-cases.test.ts. Fixing one port alone reds that port; editing the
// shared file alone reds all three.

package com.talyvor.code

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import java.io.File

class CredentialVerdictParityTest {

    private data class VerdictCase(val status: Int, val verdict: String, val why: String)

    // ⚠ FAILS LOUDLY WHEN THE FILE IS MISSING OR SHORT. A moved or truncated manifest would
    // otherwise leave this test asserting nothing — the empty-population failure this repository
    // has shipped before. The floor is checked against the count in the file at the time it was
    // written, so deleting rows is a red rather than a quieter green.
    private fun loadCases(): List<VerdictCase> {
        var dir: File? = File(System.getProperty("user.dir"))
        while (dir != null) {
            val f = File(dir, "testdata/credential-verdict-cases.json")
            if (f.isFile) {
                val arr = JSONObject(f.readText()).getJSONArray("cases")
                val out = (0 until arr.length()).map {
                    val o = arr.getJSONObject(it)
                    VerdictCase(o.getInt("status"), o.getString("verdict"), o.getString("why"))
                }
                assertTrue(
                    "credential-verdict-cases.json holds ${out.size} cases; it held 13 when this " +
                        "test was written. A shrinking table is a test asserting less, not a pass.",
                    out.size >= 13,
                )
                return out
            }
            dir = dir.parentFile
        }
        fail("testdata/credential-verdict-cases.json not found walking up from ${System.getProperty("user.dir")}")
        return emptyList()
    }

    @Test
    fun `the Kotlin port matches the shared credential verdict table`() {
        val cases = loadCases()
        val expected = mapOf(
            "ok" to CredentialVerdict.OK,
            "rejected" to CredentialVerdict.REJECTED,
            "unknown" to CredentialVerdict.UNKNOWN,
        )
        for (c in cases) {
            val want = expected[c.verdict] ?: fail("unknown verdict ${c.verdict} in the shared table")
            assertEquals(
                "HTTP ${c.status}: ${c.why}",
                want,
                ConnReportPure.verdictFor(c.status),
            )
        }
    }

    // POSITIVE CONTROL ON THE POPULATION: the table must contain all three verdicts. A table that
    // had drifted to a single verdict would pass the loop above while proving nothing.
    @Test
    fun `the shared table exercises every verdict`() {
        val seen = loadCases().map { it.verdict }.toSet()
        assertEquals(setOf("ok", "rejected", "unknown"), seen)
    }
}
