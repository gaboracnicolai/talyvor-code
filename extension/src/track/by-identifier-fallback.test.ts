import { test } from "node:test";
import assert from "node:assert/strict";
import { TrackClient } from "./client";

// THE FINDING, MEASURED: THIS CLIENT SPENDS A HUMAN ISSUE KEY ON TRACK'S UUID SLOT, AND THE MISS
// IS DISGUISED AS A HIT.
//
// client.ts's own first line says the client exists to "look up an issue by its human identifier
// (ENG-42)". It then builds `/v1/workspaces/{ws}/issues/{identifier}` — Track's `/{id}` route,
// which reads `WHERE id = $1` against a gen_random_uuid()::text primary key. A human key never
// matches. issue-context.ts:54 makes the intent explicit: it VALIDATES the input with
// isValidIssueIdentifier and warns when it does not look like ENG-42, then sends it anyway.
//
// ⚠ AND THE FAILURE IS NOT MERELY SILENT — IT IS DRESSED AS SUCCESS. getIssue returns null for
// any non-ok response, and setActiveIssue then fabricates a SYNTHETIC issue
// (`{id:"", identifier:id, title:id, status:""}`, issue-context.ts:64) so "the attribution header
// still rides". The user sees their own key echoed back as the issue title, with no status, no
// description, no cost, and no error anywhere.
//
// ⚠ TALYVOR-TRACK ALREADY SERVES THE ROUTE THAT WORKS, AND ITS HANDLER NAMES THIS REPO AS THE
// REASON: `GET /v1/workspaces/{wsID}/issues/by-identifier/{identifier}`, reading
// `WHERE identifier = $1 AND workspace_id = $2` — "the CLI agent's Track client put an identifier
// in the {id} slot below and got 404 for issues that exist — measured on the wire, talyvor-code
// #57". The server half shipped; neither client half did.
//
// ⚠ THE ORDER IS WHAT KEEPS THE FIX DECISION-FREE. `/{id}` is asked FIRST, exactly as before, and
// by-identifier only after a 404 — so every input that resolved before resolves identically, via
// the same request. This can only turn 404s into 200s.

type Call = { path: string; auth: string | undefined };

// fakeTrack answers the way talyvor-track actually does: `{id}` matches only the row id,
// `by-identifier/{identifier}` only the human key, everything else 404.
function fakeTrack(rowId: string, identifier: string, calls: Call[]) {
  const body = JSON.stringify({
    id: rowId,
    identifier,
    title: "Add login",
    status: "in_progress",
    ai_cost_usd: 1.5,
  });
  return async (input: unknown, init?: { headers?: Record<string, string> }) => {
    const url = new URL(String(input));
    calls.push({ path: url.pathname, auth: init?.headers?.["Authorization"] });
    const ok =
      url.pathname === `/v1/workspaces/ws-1/issues/${rowId}` ||
      url.pathname === `/v1/workspaces/ws-1/issues/by-identifier/${identifier}`;
    return ok
      ? new Response(body, { status: 200, headers: { "Content-Type": "application/json" } })
      : new Response("not found", { status: 404 });
  };
}

async function withFetch<T>(impl: unknown, fn: () => Promise<T>): Promise<T> {
  const original = globalThis.fetch;
  (globalThis as { fetch: unknown }).fetch = impl;
  try {
    return await fn();
  } finally {
    (globalThis as { fetch: unknown }).fetch = original;
  }
}

const ROW_ID = "6f1c9e2a-0000-4000-8000-000000000001";

test("getIssue resolves a human identifier the way the IDE actually supplies it", async () => {
  const calls: Call[] = [];
  const issue = await withFetch(fakeTrack(ROW_ID, "ENG-42", calls), () =>
    new TrackClient("http://track.test", "tlv_k").getIssue("ws-1", "ENG-42"),
  );
  assert.ok(
    issue,
    `getIssue("ENG-42") returned null for an issue that EXISTS. Requests: ${JSON.stringify(
      calls.map((c) => c.path),
    )}\nissue-context.ts validates this input as a human identifier and Track serves ` +
      `/issues/by-identifier/{identifier} for exactly it.`,
  );
  assert.equal(issue?.identifier, "ENG-42");
  assert.equal(issue?.title, "Add login");
});

test("a row id still resolves on the FIRST request — the fix changes no existing answer", async () => {
  const calls: Call[] = [];
  const issue = await withFetch(fakeTrack(ROW_ID, "ENG-42", calls), () =>
    new TrackClient("http://track.test", "tlv_k").getIssue("ws-1", ROW_ID),
  );
  assert.ok(issue, "a row id must still resolve");
  assert.equal(
    calls.length,
    1,
    `a row id took ${calls.length} requests (${JSON.stringify(
      calls.map((c) => c.path),
    )}), want 1 — the fallback must not fire when the id slot answers.`,
  );
});

test("the fallback request carries the credential", async () => {
  const calls: Call[] = [];
  await withFetch(fakeTrack(ROW_ID, "ENG-42", calls), () =>
    new TrackClient("http://track.test", "tlv_k").getIssue("ws-1", "ENG-42"),
  );
  assert.ok(calls.length >= 2, `expected a fallback request, got ${JSON.stringify(calls)}`);
  for (const c of calls) {
    assert.equal(
      c.auth,
      "Bearer tlv_k",
      `${c.path} was sent without the Authorization header — an unauthenticated retry 401s and the ` +
        `miss stays a miss for the wrong reason.`,
    );
  }
});

test("a genuine miss is still null, after asking both routes", async () => {
  const calls: Call[] = [];
  const issue = await withFetch(fakeTrack(ROW_ID, "ENG-42", calls), () =>
    new TrackClient("http://track.test", "tlv_k").getIssue("ws-1", "NOPE-1"),
  );
  assert.equal(issue, null);
  assert.equal(
    calls.length,
    2,
    `a genuine miss made ${calls.length} requests (${JSON.stringify(
      calls.map((c) => c.path),
    )}), want 2 — the id slot then by-identifier.`,
  );
});
