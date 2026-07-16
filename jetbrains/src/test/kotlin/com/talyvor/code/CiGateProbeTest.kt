// TEMPORARY — CI-gate integrity probe (JetBrains Run 6, Phase 0).
//
// This test DELIBERATELY FAILS. It exists only to prove the JetBrains CI job
// truthfully reports a real test failure. It is added, observed, and reverted within
// the PR — it is NOT product code and does NOT survive into the final diff.
//
//   1. With the OLD ci.yaml (`continue-on-error: true`), this failing test → the
//      jetbrains job still reports GREEN (green-by-absence — the dishonesty).
//   2. With the honest gate (continue-on-error removed), this failing test → the
//      jetbrains job goes RED and blocks the PR (teeth).
//
// Then removed so the PR ends green on the real 49-test suite.
package com.talyvor.code

import org.junit.Assert.assertEquals
import org.junit.Test

class CiGateProbeTest {
    @Test
    fun deliberateFailureProvesTheGateReportsFailures() {
        assertEquals("temporary CI-gate probe — this failure must surface", 1, 2)
    }
}
