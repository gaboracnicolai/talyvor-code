// The sentence a user is shown by Tools → Talyvor → Test Lens Connection.
//
// ⚠ IT SAID "✅ Connected to Lens v<version>" FOR A WRONG, REVOKED OR EXPIRED KEY, because the only
// probe behind it was GET /healthz, which Lens serves unauthenticated — and its single failure
// sentence sent the user to "the URL and your network", the two things that had just worked.

package com.talyvor.code

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ConnReportPureTest {

    // K1 — a rejected key is a key problem, not a connection.
    @Test
    fun `a rejected key is reported as an error that names the key`() {
        val r = ConnReportPure.connectionReport(true, "9.9.9", CredentialVerdict.REJECTED, 401)
        assertTrue("reported as success: ${r.message}", r.isError)
        assertTrue("never names the key: ${r.message}", r.message.contains("key", ignoreCase = true))
        assertTrue("does not carry the status: ${r.message}", r.message.contains("401"))
    }

    // K2 — and it must not send the user to the URL/network, which demonstrably just worked.
    @Test
    fun `a rejected key does not blame the network`() {
        val r = ConnReportPure.connectionReport(true, "9.9.9", CredentialVerdict.REJECTED, 403)
        assertFalse("still blames the network: ${r.message}", r.message.contains("network", ignoreCase = true))
    }

    // K3 — POSITIVE CONTROL, THE OTHER DIRECTION: a verified key still succeeds, keeps the version
    // and says the key was checked. Without this an "always error" report would satisfy K1 and K2.
    @Test
    fun `a verified key succeeds, keeps the version and says the key was verified`() {
        val r = ConnReportPure.connectionReport(true, "1.2.3", CredentialVerdict.OK, 200)
        assertFalse(r.message, r.isError)
        assertTrue("lost the version: ${r.message}", r.message.contains("1.2.3"))
        assertTrue("never states the key was verified: ${r.message}", r.message.contains("key", ignoreCase = true))
    }

    // K4 — FAIL-OPEN: an unknown verdict reports EXACTLY what shipped before this probe existed.
    // This is what stops the fix reddening an install running an older Lens.
    @Test
    fun `an unknown verdict keeps the pre-existing message verbatim`() {
        val r = ConnReportPure.connectionReport(true, "1.2.3", CredentialVerdict.UNKNOWN, 404)
        assertFalse(r.message, r.isError)
        assertEquals("✅ Connected to Lens v1.2.3", r.message)
    }

    // K5 — unreachable is unchanged, and is the one case where the URL/network advice is right.
    @Test
    fun `unreachable still points at the URL and the network`() {
        val r = ConnReportPure.connectionReport(false, "unknown", CredentialVerdict.UNKNOWN, 0)
        assertTrue(r.isError)
        assertTrue(r.message.contains("network", ignoreCase = true))
    }
}
