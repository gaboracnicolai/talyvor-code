// ConnReportPure — the decision behind Tools → Talyvor → Test Lens Connection, kept free of the
// IntelliJ Platform so a plain JUnit test can reach it.
//
// ⚠ WHY IT IS SEPARATE, WHICH IS ALSO WHY IT WAS WRONG: the decision lived inline in
// TestConnectionAction, which extends AnAction and therefore cannot be loaded outside the IDE. So
// the sentence a user is shown when they ask whether their setup works had no test at all — the
// same reason SafeUrlPure was extracted out of TalyvorSettings.

package com.talyvor.code

/** What the authenticated probe could establish. UNKNOWN is a real answer, not a soft REJECTED. */
enum class CredentialVerdict { OK, REJECTED, UNKNOWN }

/** kind is INFO or ERROR; message is shown verbatim in the dialog. */
data class ConnectionReport(val isError: Boolean, val message: String)

object ConnReportPure {

    /**
     * verdictFor maps an HTTP status from GET /v1/auth/me onto a verdict.
     *
     * FAIL-OPEN BY CONSTRUCTION: only 401/403 accuses the key. The table this is asserted against
     * lives at testdata/credential-verdict-cases.json and the Go and TypeScript ports assert
     * themselves against the same file, so fixing one port alone reds that port.
     */
    fun verdictFor(status: Int): CredentialVerdict = when {
        status == 401 || status == 403 -> CredentialVerdict.REJECTED
        status in 200..299 -> CredentialVerdict.OK
        else -> CredentialVerdict.UNKNOWN
    }

    /**
     * connectionReport turns the two probes into the one sentence the user reads.
     *
     * `available` comes from GET /healthz, WHICH LENS SERVES UNAUTHENTICATED — so on its own it is
     * a statement about the URL and the network and must not be reported as one about the key.
     */
    fun connectionReport(available: Boolean, version: String, verdict: CredentialVerdict, status: Int): ConnectionReport {
        if (!available) {
            return ConnectionReport(true, "❌ Cannot connect to Lens — check the URL and your network.")
        }
        return when (verdict) {
            CredentialVerdict.REJECTED -> ConnectionReport(
                true,
                "❌ Lens is reachable, but it rejected your API key (HTTP $status). " +
                    "Check the Lens API key in Settings — the URL is fine.",
            )
            CredentialVerdict.OK -> ConnectionReport(false, "✅ Connected to Lens v$version — API key verified")
            // The probe could not answer (an older Lens with no /v1/auth/me, a server-error reply, a
            // proxy that ate it). Report EXACTLY what was reported before this probe existed: saying
            // the key is bad would red a working install, and saying it is verified would be the same
            // false tick one level down.
            CredentialVerdict.UNKNOWN -> ConnectionReport(false, "✅ Connected to Lens v$version")
        }
    }
}
