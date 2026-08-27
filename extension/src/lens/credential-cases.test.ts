import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "fs";
import * as path from "path";
import { verdictForStatus, type CredentialVerdict } from "./connreport-pure";

// ONE RULE, THREE PORTS — asserted from one file, the way safeurl-cases.json already is.
//
// testdata/credential-verdict-cases.json records what a client may conclude about the user's API
// key from the HTTP status of GET /v1/auth/me. The Go port asserts itself against it in
// agent/internal/lens/credential_cases_parity_test.go and the Kotlin port in
// jetbrains/.../CredentialVerdictParityTest.kt. Fixing one port alone reds that port.
//
// It exists because all three ports previously agreed on the WRONG answer: each tested the
// connection with an unauthenticated GET /healthz and reported success, so a wrong, revoked or
// expired key produced a green tick in every client.

interface VerdictCase {
  status: number;
  verdict: CredentialVerdict;
  why: string;
}

// ⚠ LOUD, NOT EMPTY. A moved or truncated manifest must red, not leave this suite verifying
// nothing — the empty-population failure this repository has shipped before.
function loadCases(): VerdictCase[] {
  let dir = __dirname;
  for (;;) {
    const p = path.join(dir, "testdata", "credential-verdict-cases.json");
    if (fs.existsSync(p)) {
      const cases = JSON.parse(fs.readFileSync(p, "utf8")).cases as VerdictCase[];
      assert.ok(
        Array.isArray(cases) && cases.length >= 13,
        `credential-verdict-cases.json holds ${cases?.length} cases; it held 13 when this test ` +
          "was written. A shrinking table is a test asserting less, not a pass.",
      );
      return cases;
    }
    const parent = path.dirname(dir);
    assert.notEqual(parent, dir, "testdata/credential-verdict-cases.json not found walking up");
    dir = parent;
  }
}

test("the TypeScript port matches the shared credential verdict table", () => {
  for (const c of loadCases()) {
    assert.equal(verdictForStatus(c.status), c.verdict, `HTTP ${c.status}: ${c.why}`);
  }
});

// POSITIVE CONTROL ON THE POPULATION: the table must exercise all three verdicts. A table that had
// drifted to a single verdict would pass the loop above while proving nothing.
test("the shared table exercises every verdict", () => {
  const seen = new Set(loadCases().map((c) => c.verdict));
  assert.deepEqual([...seen].sort(), ["ok", "rejected", "unknown"]);
});
