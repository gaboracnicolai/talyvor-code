import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "fs";
import * as path from "path";
import { KNOWN_MODELS } from "./models-pure";
import { LensClient } from "../lens/client";

// THE CATALOGUE ADVERTISES A PROVIDER THE DISPATCH LAYER CANNOT REACH.
//
// KNOWN_MODELS is the list the QuickPick offers and each entry carries a `provider` the user is
// shown. completeStream does not read that field: providerForModel classifies by id prefix into
// exactly two outcomes — gpt-/o1/o3 to the OpenAI route, EVERYTHING ELSE to the Anthropic route — and
// the endpoint AND the request body shape are both chosen from it. So `mistral-large`, listed as
// provider "Mistral", is POSTed to /v1/proxy/anthropic/v1/messages with an Anthropic body.
//
// The full measurement, including what talyvor-lens actually does with it (the provider is pinned by
// the ROUTE, and Lens registers a /v1/proxy/mistral/* nothing here calls), is in the Go half of this
// harness: agent/internal/lens/model_routing_parity_test.go. Both halves read
// testdata/model-routing.json and both MEASURE the dispatch through their own real client rather
// than restating it, so fixing one side alone reds that side.

interface RoutingCase {
  id: string;
  catalogProvider: string;
  routedEndpoint: string;
  routedProvider: string;
  dispatchable: boolean;
  why?: string;
}

/** Walks up for the shared table. ⚠ LOUD, NOT EMPTY — a file that has moved must not leave this
 *  suite verifying nothing. */
function tablePath(): string {
  let dir = __dirname;
  for (let i = 0; i < 10; i++) {
    const candidate = path.join(dir, "testdata", "model-routing.json");
    if (fs.existsSync(candidate)) return candidate;
    const parent = path.dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  throw new Error(
    `testdata/model-routing.json not found above ${__dirname} — this suite would have verified nothing`,
  );
}

// A floor, not a count. The population rule below cannot catch a truncation on its own: a catalogue
// emptied alongside the table satisfies it vacuously.
const ROUTING_FLOOR = 6;

function loadCases(): RoutingCase[] {
  const p = tablePath();
  const parsed = JSON.parse(fs.readFileSync(p, "utf8")) as { cases?: RoutingCase[] };
  const cases = parsed.cases ?? [];
  assert.ok(
    cases.length >= ROUTING_FLOOR,
    `${p} has ${cases.length} cases, expected at least ${ROUTING_FLOOR} — a shrunken table proves less than it claims`,
  );
  return cases;
}

// dispatchPathFor drives the REAL completeStream with a stubbed global fetch and returns the path it
// POSTed to. Measured, not inferred from providerForModel: the endpoint literals live in client.ts,
// so a classifier that stayed right while an endpoint moved would still be a misroute.
async function dispatchPathFor(modelId: string): Promise<string> {
  const realFetch = globalThis.fetch;
  let seen = "";
  try {
    globalThis.fetch = (async (input: unknown) => {
      seen = new URL(String(input)).pathname;
      // Non-SSE JSON so completeStream takes its documented fallback and returns cleanly. The body
      // shape does not matter here; the PATH is the measurement.
      return new Response("{}", {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }) as typeof globalThis.fetch;

    const client = new LensClient("https://lens.example.com", "tlv_test_key");
    await client.completeStream(
      [{ role: "user", content: "hi" }],
      modelId,
      "parity",
      "ws",
      "issue",
      { onChunk: () => {}, onDone: () => {}, onError: () => {} },
    );
  } finally {
    globalThis.fetch = realFetch;
  }
  assert.notEqual(seen, "", `model ${modelId}: no request was issued — the dispatch was not measured`);
  return seen;
}

test("every offered model is dispatchable to its own provider", () => {
  for (const c of loadCases()) {
    if (!c.dispatchable) continue;
    assert.equal(
      c.catalogProvider.toLowerCase(),
      c.routedProvider,
      `${c.id}: catalogue says provider "${c.catalogProvider}" but the client routes it to "${c.routedProvider}" — mark dispatchable=false and say why, or fix the routing`,
    );
  }
});

test("the routed endpoint is what the client actually POSTs", async () => {
  for (const c of loadCases()) {
    const got = await dispatchPathFor(c.id);
    assert.equal(got, c.routedEndpoint, `${c.id}: completeStream POSTed to "${got}", table says "${c.routedEndpoint}"`);
  }
});

test("the table population equals the shipped catalogue", () => {
  const inTable = new Map(loadCases().map((c) => [c.id, c]));
  const inCatalogue = new Map(KNOWN_MODELS.map((m) => [m.id, m]));
  for (const [id, m] of inCatalogue) {
    const c = inTable.get(id);
    assert.ok(
      c,
      `${id} is offered by KNOWN_MODELS and has no case — widen testdata/model-routing.json AND the Go port, or drop the model`,
    );
    assert.equal(c.catalogProvider, m.provider, `${id}: catalogue declares provider "${m.provider}", table says "${c.catalogProvider}"`);
  }
  for (const id of inTable.keys()) {
    assert.ok(
      inCatalogue.has(id),
      `${id} has a case and is not in KNOWN_MODELS — drop the case, or restore the model`,
    );
  }
});

test("undispatchable cases carry their reason", () => {
  for (const c of loadCases()) {
    if (c.dispatchable) {
      assert.equal(
        (c.why ?? "").trim(),
        "",
        `${c.id}: dispatchable=true but carries a "why" — the field records why a model CANNOT reach its provider`,
      );
      continue;
    }
    assert.notEqual(
      (c.why ?? "").trim(),
      "",
      `${c.id}: dispatchable=false with no "why" — a pinned defect must say what it is`,
    );
  }
});
