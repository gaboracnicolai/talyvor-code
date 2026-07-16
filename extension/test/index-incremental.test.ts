// Incremental build — the TS mirror of Go BuildIncremental's property set, proven with
// a COUNTING fake embedder (records every text embedded) so a test can show exactly
// which files were re-embedded. Runs headlessly under `npm test` on a real temp tree.
// (This test was mutation-verified: forcing a full rebuild — reusable=false — turns the
// reuse/drop/add assertions RED, confirming they actually catch the incremental behavior.)

import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { buildFromRoot, buildIncremental } from "../src/agent/index-build-pure";
import { INDEX_VERSION, type Embedder, type SemanticIndex } from "../src/agent/retrieval-pure";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

function tmpRoot(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "tlv-inc-"));
}
function writeF(root: string, rel: string, content: string): void {
  const p = path.join(root, rel);
  fs.mkdirSync(path.dirname(p), { recursive: true });
  fs.writeFileSync(p, content);
}
function hasFileChunk(idx: SemanticIndex, file: string): boolean {
  return idx.chunks.some((c) => c.file === file);
}

// countingEmbedder records every text it embeds so a test can prove exactly which
// chunks were (re-)embedded on an incremental pass.
class CountingEmbedder implements Embedder {
  embedded: string[] = [];
  async embed(texts: string[]): Promise<number[][]> {
    this.embedded.push(...texts);
    return texts.map(() => [1, 0, 0, 0]);
  }
  reset(): void {
    this.embedded = [];
  }
  embeddedAny(sub: string): boolean {
    return this.embedded.some((t) => t.includes(sub));
  }
}

async function testBuildFromRoot_SetsVersionAndHashes(): Promise<void> {
  const root = tmpRoot();
  try {
    writeF(root, "a.go", "package p\n\nfunc A() int { return 1 }\n");
    const idx = await buildFromRoot(new CountingEmbedder(), root, 100);
    assert(idx.version === INDEX_VERSION, `index version = ${idx.version}, want ${INDEX_VERSION}`);
    assert(!!idx.file_hashes && idx.file_hashes["a.go"].length === 64, "a per-file SHA-256 hash is recorded");
    assert(idx.embed_model === "text-embedding-3-small", "embed_model stamped");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

// The core proof: after changing ONE file, a re-index embeds ONLY that file's chunks.
async function testBuildIncremental_ReEmbedsOnlyChangedFile(): Promise<void> {
  const root = tmpRoot();
  try {
    writeF(root, "a.go", "package p\n\nfunc Alpha() int { return 1 }\n");
    writeF(root, "b.go", "package p\n\nfunc Bravo() int { return 2 }\n");
    writeF(root, "c.go", "package p\n\nfunc Charlie() int { return 3 }\n");
    const emb = new CountingEmbedder();
    const full = await buildFromRoot(emb, root, 100);
    const total = emb.embedded.length;
    assert(total > 0, "full build embedded something");

    writeF(root, "b.go", "package p\n\nfunc Bravo() int { return 999 }\n"); // change ONLY b.go
    emb.reset();
    const inc = await buildIncremental(emb, root, 100, full);

    assert(!emb.embeddedAny("Alpha") && !emb.embeddedAny("Charlie"), "MUST NOT re-embed unchanged a.go / c.go");
    assert(emb.embeddedAny("999"), "MUST embed the changed b.go");
    assert(emb.embedded.length < total, `incremental embedded ${emb.embedded.length}; must be < full ${total}`);
    for (const f of ["a.go", "b.go", "c.go"]) assert(hasFileChunk(inc, f), `all files remain: ${f}`);
    assert(inc.file_hashes!["b.go"] !== full.file_hashes!["b.go"], "b.go hash changed");
    assert(inc.file_hashes!["a.go"] === full.file_hashes!["a.go"], "a.go hash unchanged");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

async function testBuildIncremental_DropsDeletedFile(): Promise<void> {
  const root = tmpRoot();
  try {
    writeF(root, "a.go", "package p\n\nfunc Alpha() int { return 1 }\n");
    writeF(root, "b.go", "package p\n\nfunc Bravo() int { return 2 }\n");
    const emb = new CountingEmbedder();
    const full = await buildFromRoot(emb, root, 100);
    fs.rmSync(path.join(root, "b.go"));
    emb.reset();
    const inc = await buildIncremental(emb, root, 100, full);

    assert(!hasFileChunk(inc, "b.go"), "a deleted file's chunks leave the index");
    assert(!(inc.file_hashes && "b.go" in inc.file_hashes), "a deleted file's hash is removed");
    assert(hasFileChunk(inc, "a.go"), "the surviving file remains");
    assert(emb.embedded.length === 0, `a pure deletion re-embeds nothing; embedded ${emb.embedded.length}`);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

async function testBuildIncremental_AddsNewFile(): Promise<void> {
  const root = tmpRoot();
  try {
    writeF(root, "a.go", "package p\n\nfunc Alpha() int { return 1 }\n");
    const emb = new CountingEmbedder();
    const full = await buildFromRoot(emb, root, 100);
    writeF(root, "new.go", "package p\n\nfunc NewlyAdded() int { return 7 }\n");
    emb.reset();
    const inc = await buildIncremental(emb, root, 100, full);

    assert(emb.embeddedAny("NewlyAdded"), "a new file is embedded");
    assert(!emb.embeddedAny("Alpha"), "the unchanged file is NOT re-embedded");
    assert(hasFileChunk(inc, "new.go"), "the new file is in the index");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

async function main(): Promise<void> {
  await testBuildFromRoot_SetsVersionAndHashes();
  await testBuildIncremental_ReEmbedsOnlyChangedFile();
  await testBuildIncremental_DropsDeletedFile();
  await testBuildIncremental_AddsNewFile();
  // eslint-disable-next-line no-console
  console.log("ok (4 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
