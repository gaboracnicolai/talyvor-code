// Retrieval-grounding tests — the extension's TS mirror of the Go semantic index
// (#21): load the SAME on-disk .talyvor/codebase-index.json, embed the query, rank by
// true cosine, honest-absent. Runs headlessly under `npm test` with a FAKE embedder
// (no network) + a temp index file, plus a fetch-stub check that LensClient.embed hits
// the right endpoint. Proves the retriever is the "honest MCP relevance" twin: real
// cosine scores, version-gated load, no fabricated ranking.

import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import {
  cosine,
  indexPath,
  loadRetriever,
  parseIndex,
  retrieve,
  Retriever,
  INDEX_VERSION,
  type Embedder,
  type SemanticIndex,
} from "../src/agent/retrieval-pure";
import { LensClient } from "../src/lens/client";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

// fakeEmbedder returns a fixed query vector, so ranking is deterministic without Lens.
function fakeEmbedder(vec: number[]): Embedder {
  return { embed: async (texts: string[]) => texts.map(() => vec) };
}

function seededIndex(): SemanticIndex {
  return {
    version: INDEX_VERSION,
    embed_model: "text-embedding-3-small",
    chunks: [
      { file: "auth.go", language: "Go", start_line: 1, end_line: 5, content: "func Verify()" },
      { file: "format.go", language: "Go", start_line: 1, end_line: 5, content: "func Format()" },
    ],
    vectors: [
      [1, 0],
      [0, 1],
    ],
  };
}

// ─── cosine ────────────────────────────────────────────

function testCosine_KnownValues(): void {
  assert(Math.abs(cosine([1, 0], [1, 0]) - 1) < 1e-9, "identical unit vectors → 1");
  assert(Math.abs(cosine([1, 0], [0, 1]) - 0) < 1e-9, "orthogonal → 0");
  assert(Math.abs(cosine([0.8, 0.6], [1, 0]) - 0.8) < 1e-9, "cosine([.8,.6],[1,0]) = 0.8");
}

function testCosine_SafeOnDegenerate(): void {
  assert(cosine([], [1]) === 0, "empty → 0");
  assert(cosine([1, 2], [1]) === 0, "mismatched length → 0");
  assert(cosine([0, 0], [0, 0]) === 0, "zero vectors → 0 (no NaN)");
}

// ─── parseIndex (version gate) ─────────────────────────

function testParseIndex_ValidLoads(): void {
  const idx = parseIndex(JSON.stringify(seededIndex()));
  assert(idx.chunks.length === 2 && idx.vectors.length === 2, "valid v1 index parses");
}

function testParseIndex_RejectsFutureVersion(): void {
  let threw = false;
  try {
    parseIndex(JSON.stringify({ ...seededIndex(), version: INDEX_VERSION + 1 }));
  } catch (e) {
    threw = e instanceof Error && /version/.test(e.message);
  }
  assert(threw, "a future index version must fail loud, naming the version");
}

function testParseIndex_RejectsLegacyUnversioned(): void {
  let threw = false;
  try {
    parseIndex(`{"chunks":[{"file":"a.go"}],"vectors":[[1]]}`); // no version → 0
  } catch {
    threw = true;
  }
  assert(threw, "a legacy unversioned index (version 0) must fail loud");
}

// ─── retrieve (real cosine, honest ranking) ────────────

async function testRetrieve_RanksByRealCosine(): Promise<void> {
  const idx = seededIndex();
  const hits = await retrieve(idx, fakeEmbedder([0.8, 0.6]), "how does verification work", 5);
  assert(hits.length === 2, "both chunks returned");
  assert(hits[0].file === "auth.go", "top hit is the semantically-closest chunk");
  assert(Math.abs(hits[0].score - 0.8) < 1e-6, `top score must be REAL cosine 0.8; got ${hits[0].score}`);
  assert(Math.abs(hits[1].score - 0.6) < 1e-6, `second score must be 0.6; got ${hits[1].score}`);
  // The old fabricated linear decay would have been 1.0 / 0.9 — prove we're not that.
  assert(hits[0].score !== 1.0 && hits[1].score !== 0.9, "must not be a fabricated linear rank");
}

async function testRetrieve_EmptyIndex(): Promise<void> {
  const empty: SemanticIndex = { version: INDEX_VERSION, chunks: [], vectors: [] };
  const hits = await retrieve(empty, fakeEmbedder([1, 0]), "q", 5);
  assert(hits.length === 0, "an empty index yields no hits");
}

async function testRetrieve_TopKLimit(): Promise<void> {
  const idx = seededIndex();
  const hits = await retrieve(idx, fakeEmbedder([0.8, 0.6]), "q", 1);
  assert(hits.length === 1 && hits[0].file === "auth.go", "top-k limit honored, best first");
}

// ─── loadRetriever (disk, honest-absent) ───────────────

async function testLoadRetriever_LoadsAndRetrieves(): Promise<void> {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "tlv-idx-"));
  try {
    const p = indexPath(root);
    fs.mkdirSync(path.dirname(p), { recursive: true });
    fs.writeFileSync(p, JSON.stringify(seededIndex()));
    const ret = await loadRetriever(root, fakeEmbedder([0.8, 0.6]));
    assert(ret instanceof Retriever, "a present index yields a Retriever");
    const hits = await ret!.retrieve("q", 5);
    assert(hits[0].file === "auth.go", "loaded retriever ranks by real cosine");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

async function testLoadRetriever_AbsentIsNull(): Promise<void> {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "tlv-idx-"));
  try {
    const ret = await loadRetriever(root, fakeEmbedder([1, 0]));
    assert(ret === null, "no index on disk → null (honest-absent, caller degrades)");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

async function testLoadRetriever_DiskVersionMismatchThrows(): Promise<void> {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "tlv-idx-"));
  try {
    const p = indexPath(root);
    fs.mkdirSync(path.dirname(p), { recursive: true });
    fs.writeFileSync(p, JSON.stringify({ ...seededIndex(), version: 999 }));
    let threw = false;
    try {
      await loadRetriever(root, fakeEmbedder([1, 0]));
    } catch (e) {
      threw = e instanceof Error && /version/.test(e.message);
    }
    assert(threw, "a version-mismatched on-disk index must fail loud, not mis-load");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

// ─── LensClient.embed (transport) ──────────────────────

async function testLensEmbed_HitsEmbeddingsEndpoint(): Promise<void> {
  let seenUrl = "";
  let seenBody: unknown;
  let seenFeature = "";
  const stub: typeof fetch = async (input, init) => {
    seenUrl = String(input);
    seenFeature = String((init?.headers as Record<string, string>)["X-Talyvor-Feature"] ?? "");
    seenBody = JSON.parse(String(init?.body));
    // Return vectors out of order to prove the client re-orders by `index`.
    return new Response(
      JSON.stringify({ data: [{ index: 1, embedding: [9, 9] }, { index: 0, embedding: [1, 2] }] }),
      { status: 200 },
    );
  };
  const original = globalThis.fetch;
  globalThis.fetch = stub;
  try {
    const c = new LensClient("http://lens:8080/", "k");
    const vecs = await c.embed(["a", "b"], "text-embedding-3-small", "embed", "ws-1", "ENG-1");
    assert(seenUrl.includes("/v1/proxy/openai/v1/embeddings"), "embed hits the OpenAI embeddings proxy: " + seenUrl);
    assert(seenFeature === "code-embed", "feature tagged code-embed for cost attribution");
    const body = seenBody as { model: string; input: string[] };
    assert(body.model === "text-embedding-3-small" && body.input.length === 2, "model + input sent");
    assert(vecs.length === 2 && vecs[0][0] === 1 && vecs[1][0] === 9, "vectors re-ordered by index");
  } finally {
    globalThis.fetch = original;
  }
}

async function main(): Promise<void> {
  testCosine_KnownValues();
  testCosine_SafeOnDegenerate();
  testParseIndex_ValidLoads();
  testParseIndex_RejectsFutureVersion();
  testParseIndex_RejectsLegacyUnversioned();
  await testRetrieve_RanksByRealCosine();
  await testRetrieve_EmptyIndex();
  await testRetrieve_TopKLimit();
  await testLoadRetriever_LoadsAndRetrieves();
  await testLoadRetriever_AbsentIsNull();
  await testLoadRetriever_DiskVersionMismatchThrows();
  await testLensEmbed_HitsEmbeddingsEndpoint();
  // eslint-disable-next-line no-console
  console.log("ok (12 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
