// THE LINCHPIN — writer↔reader agreement. Build an index with THIS TS writer
// (index-build-pure.ts), then load it with the EXISTING, UNMODIFIED reader
// (retrieval-pure.ts, #22) and retrieve. If the on-disk format drifted by even one
// field name or the version stamp, the reader's parseIndex would reject it or retrieval
// would rank wrong. Runs headlessly under `npm test`. (Mutation-verified: bumping the
// writer's version stamp makes the reader reject the file — the round-trip goes RED.)

import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { buildFromRoot, saveIndex } from "../src/agent/index-build-pure";
import { indexPath, loadIndex, loadRetriever, INDEX_VERSION, type Embedder } from "../src/agent/retrieval-pure";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

function tmpRoot(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "tlv-rt-"));
}
function write(root: string, rel: string, content: string): void {
  const p = path.join(root, rel);
  fs.mkdirSync(path.dirname(p), { recursive: true });
  fs.writeFileSync(p, content);
}

// keywordEmbedder is a deterministic content-correlated embedder: it maps auth/verify →
// one axis and format → another, so cosine ranking is meaningful without a network. The
// SAME embedder embeds chunk content (build) and the query (retrieve).
function keywordEmbedder(): Embedder {
  const vec = (t: string): number[] => {
    const low = t.toLowerCase();
    return [low.includes("auth") || low.includes("verify") ? 1 : 0, low.includes("format") ? 1 : 0, 0.1];
  };
  return { embed: async (texts: string[]) => texts.map(vec) };
}

// The end-to-end round-trip: writer builds + saves; the reader loads + retrieves.
async function testRoundTrip_WriterToReaderRetrieval(): Promise<void> {
  const root = tmpRoot();
  try {
    write(root, "auth.go", "package auth\n\n// Verify checks the auth token.\nfunc Verify() bool { return true }\n");
    write(root, "format.go", "package fmtx\n\n// Format renders text.\nfunc Format(s string) string { return s }\n");

    // BUILD with the TS writer, SAVE to the canonical path.
    const emb = keywordEmbedder();
    const idx = await buildFromRoot(emb, root, 100);
    saveIndex(idx, indexPath(root));

    // LOAD with the EXISTING reader — proves the version stamp is accepted (no drift).
    const reloaded = loadIndex(root);
    assert(reloaded !== null, "the reader loads the writer's index (version accepted)");
    assert(reloaded!.version === INDEX_VERSION, "version round-trips");
    assert(reloaded!.chunks.length === idx.chunks.length, "chunk count round-trips");

    // RETRIEVE via the reader's Retriever — real cosine ranking, no format drift.
    const ret = await loadRetriever(root, emb);
    assert(ret !== null, "loadRetriever binds the writer's index");
    const hits = await ret!.retrieve("how is the auth token verified", 5);
    assert(hits.length > 0, "retrieval returns hits");
    assert(hits[0].file === "auth.go", `the auth chunk ranks first; got ${hits[0].file}`);
    assert(hits[0].score > hits[hits.length - 1].score, "scores are real cosines, ranked descending");
    assert(hits[0].startLine >= 1 && hits[0].endLine >= hits[0].startLine, "1-based line span round-trips");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

// Guard the on-disk shape explicitly: the raw JSON must carry the snake_case field
// names + version the reader parses (writer imports the reader's types, but assert it).
async function testRoundTrip_OnDiskFieldNames(): Promise<void> {
  const root = tmpRoot();
  try {
    write(root, "a.go", "package a\n\nfunc A() int { return 1 }\n");
    const idx = await buildFromRoot(keywordEmbedder(), root, 100);
    saveIndex(idx, indexPath(root));
    const raw = fs.readFileSync(indexPath(root), "utf8");
    const obj = JSON.parse(raw);
    assert(obj.version === INDEX_VERSION, "on-disk `version` present");
    assert(Array.isArray(obj.chunks) && Array.isArray(obj.vectors), "on-disk chunks + vectors arrays");
    assert("start_line" in obj.chunks[0] && "end_line" in obj.chunks[0] && "file" in obj.chunks[0], "snake_case chunk fields");
    assert(typeof obj.file_hashes === "object" && obj.embed_model === "text-embedding-3-small", "file_hashes + embed_model stamped");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

// Incremental round-trip: build, edit one file, re-index with prev, save, reload — the
// reader still retrieves cleanly (incremental output is reader-valid too).
async function testRoundTrip_IncrementalStaysReaderValid(): Promise<void> {
  const root = tmpRoot();
  try {
    write(root, "auth.go", "package auth\n\nfunc Verify() bool { return true }\n");
    write(root, "format.go", "package fmtx\n\nfunc Format() {}\n");
    const emb = keywordEmbedder();
    const full = await buildFromRoot(emb, root, 100);
    saveIndex(full, indexPath(root));

    write(root, "auth.go", "package auth\n\nfunc Verify() bool { return false }\n"); // edit
    const prev = loadIndex(root); // reader loads prior
    const { buildIncremental } = await import("../src/agent/index-build-pure");
    const inc = await buildIncremental(emb, root, 100, prev);
    saveIndex(inc, indexPath(root));

    const ret = await loadRetriever(root, emb);
    const hits = await ret!.retrieve("auth verify", 5);
    assert(hits[0].file === "auth.go", "after an incremental rebuild + save, the reader still ranks auth first");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

async function main(): Promise<void> {
  await testRoundTrip_WriterToReaderRetrieval();
  await testRoundTrip_OnDiskFieldNames();
  await testRoundTrip_IncrementalStaysReaderValid();
  // eslint-disable-next-line no-console
  console.log("ok (3 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
