// Atomic + versioned Save — the TS mirror of Go Save (temp-then-rename) + the
// IndexVersion stamp the reader gates on. The concurrent torn-read guarantee is proven
// with a worker_threads reader (real OS-thread parallelism — the node equivalent of the
// Go goroutine race): while the main thread rewrites a large index, a reader thread
// hammers the target and must NEVER see a torn/partial file, because rename is atomic.
// Plus deterministic checks: reader-accepts-the-version-stamp, whole-file replace, and
// no temp-file leftover (mutation-verified).

import { isMainThread, parentPort, Worker, workerData } from "worker_threads";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { indexPath, INDEX_VERSION, parseIndex, type SemanticIndex } from "../src/agent/retrieval-pure";
import { saveIndex } from "../src/agent/index-build-pure";

// ── reader worker: loop reading + parsing the target until told to stop ──
if (!isMainThread && workerData && workerData.mode === "reader") {
  const target: string = workerData.target;
  let torn: string | null = null;
  let reads = 0;
  let stop = false;
  parentPort!.on("message", (m) => {
    if (m === "stop") stop = true;
  });
  const loop = (): void => {
    for (let i = 0; i < 200 && !stop; i++) {
      try {
        const raw = fs.readFileSync(target, "utf8");
        parseIndex(raw); // throws on a torn/partial JSON
        reads++;
      } catch (e) {
        torn = e instanceof Error ? e.message : String(e);
        parentPort!.postMessage({ torn, reads });
        return;
      }
    }
    if (stop) parentPort!.postMessage({ torn, reads });
    else setImmediate(loop);
  };
  loop();
} else {
  // ── main test ──
  const assert = (cond: unknown, msg: string): void => {
    if (!cond) throw new Error("ASSERT: " + msg);
  };

  const tmpRoot = (): string => fs.mkdtempSync(path.join(os.tmpdir(), "tlv-save-"));

  const makeIndex = (n: number): SemanticIndex => {
    const idx: SemanticIndex = {
      version: INDEX_VERSION,
      root: "/x",
      embed_model: "text-embedding-3-small",
      file_hashes: {},
      chunks: [],
      vectors: [],
    };
    for (let i = 0; i < n; i++) {
      const name = `pkg/file_${i}.go`;
      idx.chunks.push({ file: name, language: "Go", start_line: 1, end_line: 40, content: "x".repeat(160) });
      idx.file_hashes![name] = "a".repeat(64);
      idx.vectors.push(Array.from({ length: 64 }, (_, j) => i + j));
    }
    return idx;
  };

  const testVersionStampedReaderAccepts = (): void => {
    const root = tmpRoot();
    try {
      const target = indexPath(root);
      saveIndex(makeIndex(3), target);
      const parsed = parseIndex(fs.readFileSync(target, "utf8")); // the reader's own gate
      assert(parsed.version === INDEX_VERSION, "the writer stamps the version the reader accepts");
      assert(parsed.chunks.length === 3, "round-loads the chunks");
    } finally {
      fs.rmSync(root, { recursive: true, force: true });
    }
  };

  const testAtomicWholeReplace = (): void => {
    const root = tmpRoot();
    try {
      const target = indexPath(root);
      saveIndex(makeIndex(2), target);
      saveIndex(makeIndex(5), target); // replace
      const parsed = parseIndex(fs.readFileSync(target, "utf8"));
      assert(parsed.chunks.length === 5, "replace yields exactly the new index, never a mix");
    } finally {
      fs.rmSync(root, { recursive: true, force: true });
    }
  };

  const testNoTempLeftover = (): void => {
    const root = tmpRoot();
    try {
      const target = indexPath(root);
      saveIndex(makeIndex(4), target);
      const siblings = fs.readdirSync(path.dirname(target));
      assert(
        siblings.every((f) => f === "codebase-index.json"),
        `no temp sibling left behind; saw ${JSON.stringify(siblings)}`,
      );
    } finally {
      fs.rmSync(root, { recursive: true, force: true });
    }
  };

  const testConcurrentReaderNeverTorn = async (): Promise<void> => {
    const root = tmpRoot();
    try {
      const target = indexPath(root);
      const big = makeIndex(2000); // large enough that a non-atomic write would be caught mid-flight
      saveIndex(big, target); // seed a valid file
      const worker = new Worker(__filename, { workerData: { mode: "reader", target } });
      const done = new Promise<{ torn: string | null; reads: number }>((resolve) => {
        worker.once("message", resolve);
      });
      for (let i = 0; i < 150; i++) saveIndex(big, target); // rewrite under the reader
      worker.postMessage("stop");
      const res = await done;
      await worker.terminate();
      assert(res.torn === null, `a concurrent reader saw a TORN write — Save is not atomic: ${res.torn}`);
      assert(res.reads > 0, "the reader actually read the file during the rewrites");
    } finally {
      fs.rmSync(root, { recursive: true, force: true });
    }
  };

  const main = async (): Promise<void> => {
    testVersionStampedReaderAccepts();
    testAtomicWholeReplace();
    testNoTempLeftover();
    await testConcurrentReaderNeverTorn();
    // eslint-disable-next-line no-console
    console.log("ok (4 tests)");
  };

  main().catch((err) => {
    // eslint-disable-next-line no-console
    console.error(err);
    process.exit(1);
  });
}
