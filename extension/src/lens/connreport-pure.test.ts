import { test } from "node:test";
import assert from "node:assert/strict";
import { connectionReport } from "./connreport-pure";

// ⚠ THE DEFECT: "Talyvor: Test Lens Connection" probes GET /healthz, which Lens serves
// UNAUTHENTICATED (talyvor-lens internal/dashboard/handler.go says so in as many words). The API
// key is never put on the wire, so a wrong, revoked or expired key produced
// "✅ Connected to Lens v<version>" — and the ONLY failure sentence the command can emit sends the
// user to "the URL and your network", which are the two things that are fine.
//
// The same false ✓ ships in all three clients: this command, the JetBrains TestConnectionAction,
// and `talyvor-code check`.

// T1 — THE RED. A credential Lens rejects must not read as a working connection.
test("a rejected key is reported as a key problem, not a connection", () => {
  const r = connectionReport({ available: true, version: "9.9.9", verdict: "rejected", status: 401 });
  assert.equal(r.kind, "error", `reported ${r.kind}: ${r.message}`);
  assert.match(r.message, /key/i, `the message never names the key: ${r.message}`);
});

// T2 — and it must NOT send the user to the URL/network, which are demonstrably working: the
// reachability probe just succeeded against that very URL.
test("a rejected key does not blame the URL or the network", () => {
  const r = connectionReport({ available: true, version: "9.9.9", verdict: "rejected", status: 401 });
  assert.doesNotMatch(r.message, /network/i, `still blames the network: ${r.message}`);
});

// T3 — POSITIVE CONTROL, THE OTHER DIRECTION. A verified key still succeeds, keeps the version,
// and says the key was actually checked. Without this, "always error" would pass T1 and T2.
test("a verified key succeeds and says the key was verified", () => {
  const r = connectionReport({ available: true, version: "1.2.3", verdict: "ok", status: 200 });
  assert.equal(r.kind, "info", r.message);
  assert.match(r.message, /1\.2\.3/, `lost the version: ${r.message}`);
  assert.match(r.message, /key/i, `never states the key was verified: ${r.message}`);
});

// T4 — FAIL-OPEN. An older Lens without /v1/auth/me (404), a server-error reply, or a probe that
// never got a reply leaves the report EXACTLY as it was before this existed: success, and no claim
// either way about the key. This is what stops the fix reddening working installs.
test("an unknown verdict keeps today's message verbatim and claims nothing", () => {
  for (const status of [0, 404, 500, 502]) {
    const r = connectionReport({ available: true, version: "1.2.3", verdict: "unknown", status });
    assert.equal(r.kind, "info", `status ${status}: ${r.message}`);
    assert.equal(r.message, "✅ Connected to Lens v1.2.3", `status ${status}: ${r.message}`);
  }
});

// T5 — unreachable is unchanged, and is the one case where the URL/network advice is right.
test("unreachable still points at the URL and the network", () => {
  const r = connectionReport({ available: false, version: "unknown", verdict: "unknown", status: 0 });
  assert.equal(r.kind, "error");
  assert.match(r.message, /network/i);
});
