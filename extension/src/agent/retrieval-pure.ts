// Retrieval over the production semantic index — the TS mirror of the Go codebase
// package (#21). The extension LOADS the SAME on-disk artifact the CLI's
// `talyvor-code index` writes (<root>/.talyvor/codebase-index.json), embeds the query
// through Lens, and ranks by TRUE cosine — the honest relevance source, not the old
// path-substring guess. Version-gated + honest-absent, matching the Go LoadIndex.
//
// Pure + headless-testable: file IO uses node:fs (no vscode), and the query embedder
// is the `Embedder` seam (a fake in tests, LensClient in production). Only the query
// text leaves the machine (to Lens) — the index stays a local file, never uploaded.

import * as fs from "fs";
import * as path from "path";

// Mirrors internal/codebase constants so the extension reads exactly what the CLI wrote.
export const INDEX_VERSION = 1;
export const DEFAULT_EMBED_MODEL = "text-embedding-3-small";
export const INDEX_DIR = ".talyvor";
export const INDEX_FILE = "codebase-index.json";

// Chunk is one indexed span — snake_case matches the on-disk JSON (Go json tags).
export interface Chunk {
  file: string;
  language: string;
  start_line: number;
  end_line: number;
  content: string;
}

// SemanticIndex is the persisted artifact shape (Go SemanticIndex json tags).
export interface SemanticIndex {
  version: number;
  root?: string;
  embed_model?: string;
  file_hashes?: Record<string, string>;
  chunks: Chunk[];
  vectors: number[][];
}

// RetrievedChunk is a chunk plus its cosine similarity (camelCase — a TS-side result).
export interface RetrievedChunk {
  file: string;
  language: string;
  startLine: number;
  endLine: number;
  content: string;
  score: number;
}

// Embedder is the query-embedding seam (mirrors codebase.Embedder). Fake in tests;
// the production adapter wraps LensClient.embed (see lensEmbedder).
export interface Embedder {
  embed(texts: string[]): Promise<number[][]>;
}

// indexPath is the LOCAL, confined location of the persisted index under a repo root.
export function indexPath(root: string): string {
  return path.join(root, INDEX_DIR, INDEX_FILE);
}

// cosine is the similarity kernel. Safe on empty / mismatched-length / zero vectors
// (returns 0) — mirrors the Go cosine exactly.
export function cosine(a: number[], b: number[]): number {
  if (a.length === 0 || b.length === 0 || a.length !== b.length) return 0;
  let dot = 0;
  let na = 0;
  let nb = 0;
  for (let i = 0; i < a.length; i++) {
    dot += a[i] * b[i];
    na += a[i] * a[i];
    nb += b[i] * b[i];
  }
  if (na === 0 || nb === 0) return 0;
  return dot / (Math.sqrt(na) * Math.sqrt(nb));
}

// parseIndex validates + VERSION-GATES a raw index JSON. A version that does not match
// INDEX_VERSION fails LOUD (an error naming the mismatch) rather than being mis-read —
// mirrors the Go LoadIndex gate (legacy unversioned artifacts parse to version 0 and
// are rejected too).
export function parseIndex(json: string): SemanticIndex {
  let data: unknown;
  try {
    data = JSON.parse(json);
  } catch (err) {
    throw new Error("codebase: parse index: " + (err instanceof Error ? err.message : String(err)));
  }
  if (!data || typeof data !== "object") {
    throw new Error("codebase: parse index: not an object");
  }
  const idx = data as SemanticIndex;
  const version = typeof idx.version === "number" ? idx.version : 0;
  if (version !== INDEX_VERSION) {
    throw new Error(
      `codebase: index version ${version} != supported ${INDEX_VERSION} — run \`talyvor-code index\` to rebuild`,
    );
  }
  return { ...idx, chunks: idx.chunks ?? [], vectors: idx.vectors ?? [] };
}

// retrieve embeds the query and returns the top-k chunks by cosine similarity, sorted
// descending with a deterministic file/line tiebreak (mirrors Go Retrieve). An empty
// index yields no hits.
export async function retrieve(
  idx: SemanticIndex,
  emb: Embedder,
  query: string,
  k: number,
): Promise<RetrievedChunk[]> {
  if (!idx || idx.chunks.length === 0) return [];
  if (k <= 0) k = 5;
  const qv = await emb.embed([query]);
  if (qv.length !== 1) {
    throw new Error(`codebase: embedder returned ${qv.length} vectors for 1 query`);
  }
  const q = qv[0];
  const scored: RetrievedChunk[] = idx.chunks.map((c, i) => ({
    file: c.file,
    language: c.language,
    startLine: c.start_line,
    endLine: c.end_line,
    content: c.content,
    score: cosine(q, idx.vectors[i] ?? []),
  }));
  scored.sort((x, y) => {
    if (x.score !== y.score) return y.score - x.score;
    if (x.file !== y.file) return x.file < y.file ? -1 : 1;
    return x.startLine - y.startLine;
  });
  return scored.slice(0, k);
}

// loadIndex reads a persisted index from disk. Returns null when the file is absent so
// callers degrade to no-retrieval without special-casing; a version mismatch throws
// (fail loud) via parseIndex.
export function loadIndex(root: string): SemanticIndex | null {
  const p = indexPath(root);
  let raw: string;
  try {
    raw = fs.readFileSync(p, "utf8");
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === "ENOENT") return null;
    throw err;
  }
  return parseIndex(raw);
}

// Retriever binds a loaded index to an Embedder — the extension twin of Go BoundIndex.
export class Retriever {
  constructor(
    private readonly idx: SemanticIndex,
    private readonly emb: Embedder,
  ) {}
  retrieve(query: string, k: number): Promise<RetrievedChunk[]> {
    return retrieve(this.idx, this.emb, query, k);
  }
  chunkCount(): number {
    return this.idx.chunks.length;
  }
}

// loadRetriever loads the persisted index for `root` and binds it to `emb`. Returns
// null when no index is present (honest-absent — the caller surfaces "run
// `talyvor-code index`"); a version-mismatched index throws.
export async function loadRetriever(root: string, emb: Embedder): Promise<Retriever | null> {
  const idx = loadIndex(root);
  if (idx === null) return null;
  return new Retriever(idx, emb);
}

// LensEmbedCapable is the structural slice of LensClient the embedder needs, so this
// pure module doesn't import the client concretely.
export interface LensEmbedCapable {
  embed(texts: string[], model: string, feature: string, workspaceId: string, issueId: string): Promise<number[][]>;
}

// lensEmbedder adapts a Lens client to the Embedder seam — only the query text is sent
// to Lens (feature "embed" → tagged code-embed for cost attribution), the same trust
// boundary as a chat call.
export function lensEmbedder(lens: LensEmbedCapable, workspaceId: string, issueId: string): Embedder {
  return {
    embed: (texts: string[]) => lens.embed(texts, DEFAULT_EMBED_MODEL, "embed", workspaceId, issueId),
  };
}
