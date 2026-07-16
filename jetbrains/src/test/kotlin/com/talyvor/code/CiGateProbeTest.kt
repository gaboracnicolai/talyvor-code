// TEMPORARY — CI-gate TEETH probe (JetBrains Run 6, Phase 0). Deliberately fails to
// prove the now-honest jetbrains gate FAILS LOUDLY and blocks the PR. Reverted in the
// next commit; NOT product code.
package com.talyvor.code

import org.junit.Assert.assertEquals
import org.junit.Test

class CiGateProbeTest {
    @Test
    fun deliberateFailureMustNowBlockThePR() {
        assertEquals("teeth probe — this failure must turn the gate RED", 1, 2)
    }
}
