// In-extension semantic index BUILDER — the TS mirror of the Go internal/codebase
// writer (walker + chunker + incremental build + atomic save). Lets the extension build
// and refresh its OWN index in-process, with NO shell-out to the Go binary (in-process =
// no PATH trust surface, no second install, no version skew). It writes the EXACT format
// retrieval-pure.ts reads — this module imports that reader's types + constants, so
// writer↔reader agreement is guaranteed by construction (the reader is never modified).
//
// vscode-free + node:fs behind confine-pure (S11), so the whole builder is headless-
// testable. The ONLY thing that leaves the machine is chunk text handed to the Embedder
// (Lens, feature `embed`) — the same trust boundary as chat; the index stays a local file.

import * as fs from "fs";
import * as path from "path";
import { createHash } from "crypto";
import { absolutise } from "./confine-pure";
import {
  DEFAULT_EMBED_MODEL,
  INDEX_VERSION,
  type Chunk,
  type Embedder,
  type SemanticIndex,
} from "./retrieval-pure";

// Mirrors internal/codebase constants exactly.
export const DEFAULT_MAX_FILES = 500;
export const MAX_FILE_BYTES = 100 * 1024;
const CHUNK_WINDOW_LINES = 50;
const CHUNK_OVERLAP_LINES = 10;
const CHUNK_MAX_LINES = 160;
const EMBED_BATCH_SIZE = 64;

// skipDirs — walked-into dirs ignored by basename. NOTE: Go keeps `build` (it maps to
// false), so it is deliberately NOT here.
const SKIP_DIRS = new Set([".git", ".talyvor", "node_modules", "vendor", ".next", "dist", "__pycache__"]);
// skipSuffixes — minified bundles + lockfiles.
const SKIP_SUFFIXES = [".min.js", ".min.css", ".lock", "-lock.json"];

// detectLanguage maps a path's extension to a friendly language name (mirror Go).
export function detectLanguage(p: string): string {
  const ext = path.extname(p).toLowerCase();
  switch (ext) {
    case ".go":
      return "Go";
    case ".ts":
    case ".tsx":
      return "TypeScript";
    case ".js":
    case ".jsx":
      return "JavaScript";
    case ".py":
      return "Python";
    case ".rs":
      return "Rust";
    case ".java":
      return "Java";
    case ".kt":
      return "Kotlin";
    case ".rb":
      return "Ruby";
    case ".swift":
      return "Swift";
    case ".cs":
      return "C#";
    case ".cpp":
    case ".cxx":
    case ".cc":
      return "C++";
    case ".c":
    case ".h":
      return "C";
    case ".php":
      return "PHP";
    case ".md":
    case ".markdown":
      return "Markdown";
    case ".json":
      return "JSON";
    case ".yaml":
    case ".yml":
      return "YAML";
    case ".toml":
      return "TOML";
    case ".html":
    case ".htm":
      return "HTML";
    case ".css":
      return "CSS";
    case ".scss":
    case ".sass":
      return "Sass";
    case ".sh":
    case ".bash":
      return "Shell";
    case ".sql":
      return "SQL";
    default:
      return "Other";
  }
}

// embeddableLang: unknown / binary-ish files ("Other") are skipped (mirror Go).
export function embeddableLang(lang: string): boolean {
  return lang !== "" && lang !== "Other";
}

// contentHash is the per-file fingerprint incremental re-index compares against —
// SHA-256 hex, byte-identical to the Go crypto/sha256 hash.
export function contentHash(content: string): string {
  return createHash("sha256").update(content, "utf8").digest("hex");
}

// ─── chunker (mirror ChunkFile) ────────────────────────

function splitLines(content: string): string[] {
  const lines = content.split("\n");
  if (lines.length > 0 && lines[lines.length - 1] === "") lines.pop();
  return lines;
}

function mkChunk(relPath: string, lang: string, lines: string[], start: number, end: number): Chunk {
  return {
    file: relPath,
    language: lang,
    start_line: start,
    end_line: end,
    content: lines.slice(start - 1, end).join("\n"),
  };
}

// windowRange emits overlapping line windows covering [from,to] (1-based).
function windowRange(relPath: string, lang: string, lines: string[], from: number, to: number): Chunk[] {
  if (from < 1) from = 1;
  if (to > lines.length) to = lines.length;
  if (to < from) return [];
  const out: Chunk[] = [];
  let start = from;
  for (;;) {
    let end = start + CHUNK_WINDOW_LINES - 1;
    if (end > to) end = to;
    out.push(mkChunk(relPath, lang, lines, start, end));
    if (end >= to) break;
    start = end - CHUNK_OVERLAP_LINES + 1;
    if (start < from) start = from;
  }
  return out;
}

// chunkGo splits at top-level (col-0) func/type/const/var boundaries; each absorbs its
// preceding // comment block. Returns [] when no declaration is found.
function chunkGo(relPath: string, lang: string, lines: string[]): Chunk[] {
  const total = lines.length;
  const isDecl = (s: string): boolean =>
    s.startsWith("func ") ||
    s.startsWith("func(") ||
    s.startsWith("type ") ||
    s.startsWith("const ") ||
    s.startsWith("var ");
  const starts: number[] = []; // 1-based, strictly increasing
  for (let i = 0; i < total; i++) {
    if (!isDecl(lines[i])) continue;
    let b = i; // 0-based; extend up over a contiguous // comment block
    while (b - 1 >= 0 && lines[b - 1].startsWith("//")) b--;
    const s = b + 1;
    if (starts.length === 0 || s > starts[starts.length - 1]) starts.push(s);
  }
  if (starts.length === 0) return [];
  const out: Chunk[] = [];
  if (starts[0] > 1) out.push(mkChunk(relPath, lang, lines, 1, starts[0] - 1));
  for (let k = 0; k < starts.length; k++) {
    const s = starts[k];
    const e = k + 1 < starts.length ? starts[k + 1] - 1 : total;
    if (e - s + 1 > CHUNK_MAX_LINES) out.push(...windowRange(relPath, lang, lines, s, e));
    else out.push(mkChunk(relPath, lang, lines, s, e));
  }
  return out;
}

// chunkFile splits one file into retrievable chunks — declaration-aware for Go, else
// overlapping line windows. Whitespace-only input yields no chunks. Pure: no IO.
export function chunkFile(relPath: string, content: string): Chunk[] {
  if (content.trim() === "") return [];
  const lang = detectLanguage(relPath);
  const lines = splitLines(content);
  if (lang === "Go") {
    const cs = chunkGo(relPath, lang, lines);
    if (cs.length > 0) return cs;
  }
  return windowRange(relPath, lang, lines, 1, lines.length);
}

// ─── walker (mirror IndexDirectory, S11-confined) ──────

export interface FileEntry {
  path: string; // repo-relative
  content: string;
  hash: string;
}

// readConfined reads a file THROUGH absolutise (S11 — throws on any path outside root),
// truncating past the per-file cap exactly like the Go ReadFile.
export function readConfined(root: string, relPath: string, maxBytes = MAX_FILE_BYTES): string {
  const abs = absolutise(relPath, root); // throws on escape
  const buf = fs.readFileSync(abs);
  if (buf.length <= maxBytes) return buf.toString("utf8");
  return buf.subarray(0, maxBytes).toString("utf8") + "\n... (truncated)\n";
}

// walkRepo walks the tree from root (recursing only real directories, skipping the Go
// skipDirs; processing only real files, skipping skipSuffixes + non-embeddable langs),
// reads each file S11-confined, and returns its content + SHA-256 hash. Symlinks are
// neither recursed nor read (isDirectory()/isFile() are false for them) — an S11
// hardening over the Go walker (documented in BUILD_STATE), since a symlink can point
// outside the workspace root.
export function walkRepo(root: string, maxFiles = DEFAULT_MAX_FILES): FileEntry[] {
  if (maxFiles <= 0) maxFiles = DEFAULT_MAX_FILES;
  const rootAbs = path.resolve(root);
  const out: FileEntry[] = [];

  const walk = (dir: string): void => {
    if (out.length >= maxFiles) return;
    let entries: fs.Dirent[];
    try {
      entries = fs.readdirSync(dir, { withFileTypes: true });
    } catch {
      return; // permission failure on one dir shouldn't abort the walk
    }
    entries.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
    for (const d of entries) {
      if (out.length >= maxFiles) return;
      if (d.isDirectory()) {
        if (SKIP_DIRS.has(d.name)) continue;
        walk(path.join(dir, d.name));
        continue;
      }
      if (!d.isFile()) continue; // symlinks / sockets / fifos — not followed (S11)
      if (SKIP_SUFFIXES.some((s) => d.name.endsWith(s))) continue;
      const lang = detectLanguage(d.name);
      if (!embeddableLang(lang)) continue;
      const abs = path.join(dir, d.name);
      const rel = path.relative(rootAbs, abs);
      let content: string;
      try {
        content = readConfined(rootAbs, rel);
      } catch {
        continue;
      }
      out.push({ path: rel, content, hash: contentHash(content) });
    }
  };

  walk(rootAbs);
  return out;
}

// ─── incremental build (mirror BuildIncremental) ───────

async function embedChunks(emb: Embedder, chunks: Chunk[]): Promise<number[][]> {
  const vectors: number[][] = [];
  for (let start = 0; start < chunks.length; start += EMBED_BATCH_SIZE) {
    const batch = chunks.slice(start, start + EMBED_BATCH_SIZE);
    const vecs = await emb.embed(batch.map((c) => c.content));
    if (vecs.length !== batch.length) {
      throw new Error(`codebase: embedder returned ${vecs.length} vectors for ${batch.length} texts`);
    }
    vectors.push(...vecs);
  }
  return vectors;
}

interface ChunkVec {
  chunk: Chunk;
  vec: number[];
}

// buildIncremental re-indexes the repo, REUSING the chunks+vectors of files whose
// content hash matches prev, embedding ONLY new/changed files, and dropping deleted
// files' chunks. A null prev — or a prev of a different version / embed model — forces a
// full rebuild (mixing embed models would corrupt cosine). Mirrors Go BuildIncremental.
export async function buildIncremental(
  emb: Embedder,
  root: string,
  maxFiles: number,
  prev: SemanticIndex | null,
): Promise<SemanticIndex> {
  const entries = walkRepo(root, maxFiles);

  const reusable =
    prev !== null &&
    prev.version === INDEX_VERSION &&
    (!prev.embed_model || prev.embed_model === DEFAULT_EMBED_MODEL);
  const prevByFile = new Map<string, ChunkVec[]>();
  if (reusable && prev) {
    prev.chunks.forEach((c, i) => {
      const list = prevByFile.get(c.file) ?? [];
      list.push({ chunk: c, vec: prev.vectors[i] ?? [] });
      prevByFile.set(c.file, list);
    });
  }

  const fileHashes: Record<string, string> = {};
  const reusedChunks: Chunk[] = [];
  const reusedVecs: number[][] = [];
  const newChunks: Chunk[] = [];
  for (const e of entries) {
    fileHashes[e.path] = e.hash;
    if (reusable && prev && e.hash !== "" && prev.file_hashes?.[e.path] === e.hash) {
      for (const cv of prevByFile.get(e.path) ?? []) {
        reusedChunks.push(cv.chunk);
        reusedVecs.push(cv.vec);
      }
      continue;
    }
    newChunks.push(...chunkFile(e.path, e.content));
  }

  const newVecs = await embedChunks(emb, newChunks);
  return {
    version: INDEX_VERSION,
    root: path.resolve(root),
    embed_model: DEFAULT_EMBED_MODEL,
    file_hashes: fileHashes,
    chunks: [...reusedChunks, ...newChunks],
    vectors: [...reusedVecs, ...newVecs],
  };
}

// buildFromRoot builds a FULL index (embeds every file) — thin wrapper over
// buildIncremental with no prior index.
export function buildFromRoot(emb: Embedder, root: string, maxFiles = DEFAULT_MAX_FILES): Promise<SemanticIndex> {
  return buildIncremental(emb, root, maxFiles, null);
}

// ─── atomic + versioned save (mirror Go Save) ──────────

// saveIndex writes the index ATOMICALLY: JSON → temp file in the same dir → rename over
// the target (temp removed on any error). A concurrent reader sees the old or new WHOLE
// file, never a torn one. The version field is stamped by buildIncremental to the SAME
// IndexVersion retrieval-pure's parseIndex accepts.
export function saveIndex(idx: SemanticIndex, targetPath: string): void {
  const dir = path.dirname(targetPath);
  fs.mkdirSync(dir, { recursive: true });
  const data = JSON.stringify(idx);
  const tmp = path.join(dir, `.codebase-index-${process.pid}-${idx.chunks.length}.tmp`);
  try {
    const fd = fs.openSync(tmp, "w");
    try {
      fs.writeFileSync(fd, data);
      fs.fsyncSync(fd);
    } finally {
      fs.closeSync(fd);
    }
    fs.renameSync(tmp, targetPath);
  } catch (err) {
    try {
      fs.rmSync(tmp, { force: true });
    } catch {
      /* best-effort cleanup */
    }
    throw err;
  }
}
