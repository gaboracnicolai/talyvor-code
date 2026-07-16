// In-extension index BUILD — walker + chunker + language detection, the TS mirror of
// the Go internal/codebase walker + ChunkFile. Runs headlessly under `npm test`: a real
// fixture tree in a temp dir, no vscode. Proves the .talyvor self-index bug is NOT
// reintroduced, the skip sets match Go, chunk boundaries match ChunkFile, and every read
// is S11-confined (adversarial `../` refused, symlink-out-of-root not followed).

import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import {
  chunkFile,
  contentHash,
  detectLanguage,
  embeddableLang,
  readConfined,
  walkRepo,
} from "../src/agent/index-build-pure";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

function tmpRoot(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "tlv-build-"));
}

function write(root: string, rel: string, content: string): void {
  const p = path.join(root, rel);
  fs.mkdirSync(path.dirname(p), { recursive: true });
  fs.writeFileSync(p, content);
}

// ─── language detection ────────────────────────────────

function testDetectLanguage(): void {
  assert(detectLanguage("a.go") === "Go", ".go → Go");
  assert(detectLanguage("a.ts") === "TypeScript" && detectLanguage("a.tsx") === "TypeScript", "ts/tsx → TypeScript");
  assert(detectLanguage("a.py") === "Python", ".py → Python");
  assert(detectLanguage("a.md") === "Markdown", ".md → Markdown");
  assert(detectLanguage("a.bin") === "Other" && detectLanguage("Makefile") === "Other", "unknown → Other");
  assert(embeddableLang("Go") && !embeddableLang("Other") && !embeddableLang(""), "embeddableLang: only recognized langs");
}

function testContentHash_MatchesSha256(): void {
  // SHA-256 of "abc" (well-known vector).
  assert(
    contentHash("abc") === "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
    "contentHash is SHA-256 hex (matches Go crypto/sha256)",
  );
}

// ─── chunker (mirror ChunkFile) ────────────────────────

function testChunkFile_WhitespaceYieldsNone(): void {
  assert(chunkFile("x.go", "   \n\n  ").length === 0, "whitespace-only file → no chunks");
}

function testChunkFile_GoDeclarationAware(): void {
  const src = "package p\n\n// Alpha does A.\nfunc Alpha() int { return 1 }\n\n// Bravo does B.\nfunc Bravo() int { return 2 }\n";
  const chunks = chunkFile("p.go", src);
  assert(chunks.length === 3, `Go: header + 2 decl chunks; got ${chunks.length}`);
  assert(chunks[0].start_line === 1 && chunks[0].end_line === 2, "header chunk covers the package/import span (1-2)");
  assert(chunks[1].start_line === 3 && chunks[1].content.includes("Alpha"), "2nd chunk starts at the doc comment (3) and holds Alpha");
  assert(chunks[2].content.includes("Bravo") && chunks[2].content.includes("// Bravo"), "3rd chunk holds Bravo + its doc comment");
  for (const c of chunks) assert(c.language === "Go" && c.file === "p.go", "chunk carries file + language");
}

function testChunkFile_NonGoWindows(): void {
  // 60 lines of markdown → two overlapping 50-line windows (overlap 10): [1,50], [41,60].
  const lines = Array.from({ length: 60 }, (_, i) => `line ${i + 1}`).join("\n") + "\n";
  const chunks = chunkFile("notes.md", lines);
  assert(chunks.length === 2, `60 lines → 2 windows; got ${chunks.length}`);
  assert(chunks[0].start_line === 1 && chunks[0].end_line === 50, "window 1 = [1,50]");
  assert(chunks[1].start_line === 41 && chunks[1].end_line === 60, "window 2 = [41,60] (10-line overlap)");
}

// ─── walker (mirror IndexDirectory skip sets) ──────────

function testWalkRepo_SkipsAndSelectsCorrectly(): void {
  const root = tmpRoot();
  try {
    write(root, "a.go", "package a\nfunc A(){}\n");
    write(root, "b.ts", "export const b = 1;\n");
    write(root, "notes.md", "# hi\n");
    write(root, "sub/c.go", "package c\nfunc C(){}\n");
    write(root, "build/gen.go", "package gen\nfunc G(){}\n"); // `build` is NOT skipped (Go: false)
    write(root, "data.bin", "\x00\x01binary"); // Other → not embeddable
    write(root, "yarn.lock", "lockfile\n"); // .lock suffix
    write(root, "app.min.js", "x\n"); // .min.js suffix
    write(root, "pkg-lock.json", "{}\n"); // -lock.json suffix
    write(root, ".git/config", "[core]\n"); // .git skipDir
    write(root, "node_modules/dep/index.js", "module.exports={}\n"); // node_modules skipDir
    write(root, ".talyvor/codebase-index.json", `{"chunks":[{"file":"SENTINEL"}]}`); // MUST NOT be indexed

    const entries = walkRepo(root, 500);
    const paths = new Set(entries.map((e) => e.path));

    for (const want of ["a.go", "b.ts", "notes.md", path.join("sub", "c.go"), path.join("build", "gen.go")]) {
      assert(paths.has(want), `must index ${want}`);
    }
    for (const banned of [
      "data.bin",
      "yarn.lock",
      "app.min.js",
      "pkg-lock.json",
      path.join(".git", "config"),
      path.join("node_modules", "dep", "index.js"),
      path.join(".talyvor", "codebase-index.json"),
    ]) {
      assert(!paths.has(banned), `must NOT index ${banned}`);
    }
    // The .talyvor self-index bug (already fixed in Go) must not reappear.
    assert(![...entries].some((e) => e.content.includes("SENTINEL")), "the .talyvor cache must never be walked/indexed");
    for (const e of entries) assert(e.hash.length === 64 && e.content.length > 0, "each entry carries content + a SHA-256 hash");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

// ─── S11 confinement (adversarial) ─────────────────────

function testReadConfined_RefusesEscape(): void {
  const root = tmpRoot();
  try {
    let threw = false;
    try {
      readConfined(root, "../../../../etc/passwd");
    } catch (e) {
      threw = e instanceof Error && /outside workspace root/.test(e.message);
    }
    assert(threw, "S11: a confined read must refuse a ../ escape");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
}

function testWalkRepo_DoesNotFollowSymlinkOutOfRoot(): void {
  const root = tmpRoot();
  const outside = tmpRoot();
  try {
    fs.writeFileSync(path.join(outside, "secret.go"), "package s\n// SECRET_MATERIAL\n");
    write(root, "real.go", "package r\nfunc R(){}\n");
    // Adversarial: an in-root symlink pointing at a file OUTSIDE the workspace root.
    try {
      fs.symlinkSync(path.join(outside, "secret.go"), path.join(root, "leak.go"));
    } catch {
      return; // symlink unsupported on this platform — skip silently (not a real test skip of the logic)
    }
    const entries = walkRepo(root, 500);
    assert(entries.some((e) => e.path === "real.go"), "the real in-root file is indexed");
    assert(!entries.some((e) => e.content.includes("SECRET_MATERIAL")), "S11: a symlink out of root must NOT be followed/indexed");
    assert(!entries.some((e) => e.path === "leak.go"), "the out-of-root symlink is not indexed");
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
    fs.rmSync(outside, { recursive: true, force: true });
  }
}

function main(): void {
  testDetectLanguage();
  testContentHash_MatchesSha256();
  testChunkFile_WhitespaceYieldsNone();
  testChunkFile_GoDeclarationAware();
  testChunkFile_NonGoWindows();
  testWalkRepo_SkipsAndSelectsCorrectly();
  testReadConfined_RefusesEscape();
  testWalkRepo_DoesNotFollowSymlinkOutOfRoot();
  // eslint-disable-next-line no-console
  console.log("ok (8 tests)");
}

main();
