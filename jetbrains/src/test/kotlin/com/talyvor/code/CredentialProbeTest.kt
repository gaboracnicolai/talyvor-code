// ConnReportPureTest pins the SENTENCE and CredentialVerdictParityTest pins the STATUS MAPPING.
// This file pins the PROBE: that the API key is actually put on the wire, at the authenticated
// route. Without it, a verifyCredential that never sent an Authorization header would satisfy
// every other assertion in this plugin.

package com.talyvor.code

import com.sun.net.httpserver.HttpServer
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.net.InetSocketAddress

class CredentialProbeTest {

    private class Seen {
        val paths = mutableListOf<String>()
        val auth = mutableListOf<String?>()
    }

    private fun withStub(status: Int, body: (LensClient, Seen, String) -> Unit) {
        val seen = Seen()
        val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
        server.createContext("/") { ex ->
            seen.paths.add(ex.requestURI.path)
            seen.auth.add(ex.requestHeaders.getFirst("Authorization"))
            val payload = "{}".toByteArray()
            // 204 must not carry a body; the JDK server throws if one is written.
            if (status == 204) {
                ex.sendResponseHeaders(status, -1)
            } else {
                ex.sendResponseHeaders(status, payload.size.toLong())
                ex.responseBody.use { it.write(payload) }
            }
        }
        server.start()
        try {
            val base = "http://127.0.0.1:${server.address.port}"
            body(LensClient(base, "sk-test"), seen, base)
        } finally {
            server.stop(0)
        }
    }

    // J1 — the key goes on the wire, to the authenticated route. THIS is the finding: the
    // pre-existing probe hit /healthz, which carries no credential.
    @Test
    fun `the credential probe sends the API key to the authenticated route`() {
        withStub(200) { client, seen, _ ->
            val (verdict, status) = client.verifyCredential()
            assertEquals(CredentialVerdict.OK, verdict)
            assertEquals(200, status)
            assertEquals(listOf("/v1/auth/me"), seen.paths)
            assertEquals(listOf("Bearer sk-test"), seen.auth)
        }
    }

    // J2 — 401 and 403 are verdicts against the key.
    @Test
    fun `401 and 403 are rejections`() {
        for (code in listOf(401, 403)) {
            withStub(code) { client, _, _ ->
                val (verdict, status) = client.verifyCredential()
                assertEquals("HTTP $code", CredentialVerdict.REJECTED, verdict)
                assertEquals(code, status)
            }
        }
    }

    // J3 — FAIL-OPEN over the wire, not just in the mapping function.
    @Test
    fun `404 and server errors come back unknown`() {
        for (code in listOf(404, 500, 502, 400, 429)) {
            withStub(code) { client, _, _ ->
                assertEquals("HTTP $code", CredentialVerdict.UNKNOWN, client.verifyCredential().first)
            }
        }
    }

    // J4 — a dead socket is UNKNOWN and does not throw. A diagnostic must not die on its own
    // optional step.
    @Test
    fun `a dead socket is unknown and does not throw`() {
        val (verdict, status) = LensClient("http://127.0.0.1:1", "sk-test").verifyCredential()
        assertEquals(CredentialVerdict.UNKNOWN, verdict)
        assertEquals(0, status)
    }

    // J5 — an unconfigured client sends NOTHING. ⚠ It is pointed at the LIVE STUB, not at a dead
    // port: a version of this test that used an unreachable address would report "no request" whether
    // the guard ran or not, which is a test that cannot fail. (The TypeScript port shipped exactly
    // that mistake and its mutation harness is what caught it.)
    @Test
    fun `an unconfigured client probes nothing`() {
        withStub(200) { _, seen, base ->
            val empty = LensClient(base, "")
            assertEquals(CredentialVerdict.UNKNOWN, empty.verifyCredential().first)
            assertTrue("a client with no key still sent ${seen.paths}", seen.paths.isEmpty())
        }
    }
}
