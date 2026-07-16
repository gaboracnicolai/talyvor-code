// Refresh orchestration — the vscode-free core of the "build index" command: load the
// prior index (version-mismatch → loud full rebuild), build incrementally, save
// atomically, report the delta. Headless-tested; the vscode command is a thin wrapper.

import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { indexDelta, refreshIndex } from "../src/agent/index-build-pure";
import { indexPath, loadIndex, INDEX_VERSION, type Embedder, type SemanticIndex } from "../src/agent/retrieval-pure";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}
function tmpRoot(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "tlv-refresh-"));
}
function writeF(root: string, rel: string, content: string): void {
  const p = path.join(root, rel);
  fs.mkdirSync(path.dirname(p), { recursive: true });
  fs.writeFileSync(p, content);
}
const constEmbedder = (): Embedder => ({ embed: async (texts: string[]) => texts.map(() => [1, 0, 0, 0]) });

function testIndexDelta(): void {
  const idx = { file_hashes: { "a.go": "h1", "b.go": "h2" } } as unknown as SemanticIndex;
  assert(indexDelta(null, idx).changed === 2 && indexDelta(null, idx).reused === 0, "no prior → all changed");
  const prev = { file_hashes: { "a.go": "h1" } } as unknown as SemanticIndex;
  const d = indexDelta(prev, idx);
  assert(d.reused === 1 && d.changed === 1, "one hash matches → 1 reused, 1 changed");
}

// First build → full; unchanged re-refresh → all reused; edit one → 1 changed. And the
// output round-trips through the reader on disk each time.
async function testRefresh_FullThenIncremental(): Promise<void> {
  const root = tmpRoot();
  try {
    writeF(root, "a.go", "package p\n\nfunc A() int { return 1 }\n");
    writeF(root, "b.go", "package p\n\nfunc B() int { return 2 }\n");
    const emb = constEmbedder();

    const first = await refreshIndex(emb, root, 100);
    assert(first.fullRebuild && first.reused === 0 && first.changed === 2, `first build is full; got ${JSON.stringify(first)}`);
    assert(fs.existsSync(indexPath(root)), "the index file is written to disk");
    assert(loadIndex(root)!.version === INDEX_VERSION, "the reader loads the refreshed index");

    const second = await refreshIndex(emb, root, 100);
    assert(!second.fullRebuild && second.reused === 2 && second.changed === 0, `unchanged re-refresh reuses all; got ${JSON.stringify(second)}`);

    writeF(root, "b.go", "package p\n\nfunc B() int { return 999 }\n");
    const third = await refreshIndex(emb, root, 100);
    assert(third.reused === 1 && third.changed === 1, `editing one file → 1 reused, 1 changed; got ${JSON.stringify(third)}`);
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

// A corrupt / version-mismatched prior index must trigger a LOUD full rebuild, never a
// silent mis-parse (the reader would throw; refreshIndex catches → prev=null).
async function testRefresh_VersionMismatchForcesFullRebuild(): Promise<void> {
  const root = tmpRoot();
  try {
    writeF(root, "a.go", "package p\n\nfunc A() int { return 1 }\n");
    const p = indexPath(root);
    fs.mkdirSync(path.dirname(p), { recursive: true });
    fs.writeFileSync(p, JSON.stringify({ version: 999, chunks: [], vectors: [] }));
    const res = await refreshIndex(constEmbedder(), root, 100);
    assert(res.fullRebuild, "a version-mismatched prior forces a full rebuild");
    assert(loadIndex(root)!.version === INDEX_VERSION, "and the rewritten index is reader-valid again");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

async function main(): Promise<void> {
  testIndexDelta();
  await testRefresh_FullThenIncremental();
  await testRefresh_VersionMismatchForcesFullRebuild();
  // eslint-disable-next-line no-console
  console.log("ok (3 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});
