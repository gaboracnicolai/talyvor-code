import { test } from "node:test";
import assert from "node:assert/strict";
import * as http from "http";
import { AddressInfo } from "net";
import { LensClient } from "./client";

// connreport-pure.test.ts pins the SENTENCE. This file pins the PROBE — that the API key is
// actually put on the wire, and which HTTP answers become which verdict. Without it, a
// verifyCredential that never sends the Authorization header would satisfy every wording test.

interface Seen {
  paths: string[];
  auth: (string | undefined)[];
}

async function withStub(
  handler: (path: string, auth: string | undefined) => number,
  body: (client: LensClient, seen: Seen, base: string) => Promise<void>,
): Promise<void> {
  const seen: Seen = { paths: [], auth: [] };
  const server = http.createServer((req, res) => {
    seen.paths.push(req.url ?? "");
    seen.auth.push(req.headers.authorization);
    res.writeHead(handler(req.url ?? "", req.headers.authorization), {
      "Content-Type": "application/json",
    });
    res.end("{}");
  });
  await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
  const port = (server.address() as AddressInfo).port;
  try {
    const base = `http://127.0.0.1:${port}`;
    await body(new LensClient(base, "sk-test"), seen, base);
  } finally {
    await new Promise<void>((r) => server.close(() => r()));
  }
}

// P1 — the key goes on the wire, to the authenticated route. THIS is the whole finding: the
// pre-existing probe hit /healthz, which carries no credential.
test("the credential probe sends the API key to /v1/auth/me", async () => {
  await withStub(
    () => 200,
    async (client, seen) => {
      const r = await client.verifyCredential();
      assert.equal(r.verdict, "ok");
      assert.deepEqual(seen.paths, ["/v1/auth/me"]);
      assert.deepEqual(seen.auth, ["Bearer sk-test"]);
    },
  );
});

// P2 — 401 and 403 are verdicts against the key.
test("401 and 403 are rejections", async () => {
  for (const code of [401, 403]) {
    await withStub(
      () => code,
      async (client) => {
        const r = await client.verifyCredential();
        assert.equal(r.verdict, "rejected", `HTTP ${code} read as ${r.verdict}`);
        assert.equal(r.status, code);
      },
    );
  }
});

// P3 — FAIL-OPEN, the direction that stops this breaking working installs: everything else is
// "unknown", never "rejected".
test("404 and server errors are unknown, never a verdict against the key", async () => {
  for (const code of [404, 500, 502, 400, 429]) {
    await withStub(
      () => code,
      async (client) => {
        const r = await client.verifyCredential();
        assert.equal(r.verdict, "unknown", `HTTP ${code} read as ${r.verdict}`);
      },
    );
  }
});

// P4 — an unreachable server is "unknown" and does not throw. A diagnostic must not die on its
// own optional step.
test("a dead socket is unknown and does not throw", async () => {
  const client = new LensClient("http://127.0.0.1:1", "sk-test");
  const r = await client.verifyCredential();
  assert.equal(r.verdict, "unknown");
  assert.equal(r.status, 0);
});

// P5 — an unconfigured client answers "unknown" without sending anything, so the probe cannot
// leak a request when there is no key to put in it.
//
// ⚠ THE FIRST VERSION OF THIS TEST COULD NOT FAIL, AND ONLY THE MUTATION HARNESS SAID SO. It
// built the unconfigured client against `http://127.0.0.1:1` — a DIFFERENT address from the stub
// — so `seen.paths` was empty whether the guard ran or not, and deleting the guard (mutation N9)
// was caught by nothing. The client must point AT THE STUB for the absence of a request to mean
// anything. Same shape as an empty-population census reporting "clean".
test("an unconfigured client probes nothing", async () => {
  await withStub(
    () => 200,
    async (_client, seen, base) => {
      // The stub the harness just started — same origin as the configured client, no API key.
      const empty = new LensClient(base, "");
      const r = await empty.verifyCredential();
      assert.equal(r.verdict, "unknown");
      assert.deepEqual(seen.paths, [], "a client with no key still sent a request");
    },
  );
});
