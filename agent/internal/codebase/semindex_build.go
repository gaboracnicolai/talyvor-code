package codebase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// contentHash is the per-file fingerprint incremental re-index compares against.
// SHA-256 (stdlib, collision-safe) — DESIGN FORK: a faster non-crypto hash
// (xxhash/fnv) would shave walk time on huge repos but adds a dep and a collision
// risk; SHA-256 is the conservative choice for a correctness-critical skip decision.
func contentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// Confine resolves p against root and REFUSES any path that escapes it (the S11
// discipline: the walker/chunker reads only inside the repo root). Mirrors the
// agent's write-side confine so the index read path holds the same boundary.
func Confine(root, p string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Clean(filepath.Join(rootAbs, p))
	}
	rel, err := filepath.Rel(rootAbs, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("codebase: refusing path outside root %q: %s", rootAbs, p)
	}
	return abs, nil
}

// embeddableLang reports whether a detected language is worth embedding. Unknown /
// binary-ish files ("Other") are skipped — they add noise, not signal.
func embeddableLang(lang string) bool { return lang != "" && lang != "Other" }

// DefaultEmbedModel is the embedding model requested through the Lens OpenAI proxy.
const DefaultEmbedModel = "text-embedding-3-small"

const (
	indexDir  = ".talyvor"
	indexFile = "codebase-index.json"
)

// IndexPath is the LOCAL, confined location of the persisted semantic index under a
// repo root: <root>/.talyvor/codebase-index.json. The index never leaves the repo.
func IndexPath(root string) string {
	return filepath.Join(root, indexDir, indexFile)
}

// fileEntry is one walked, confined, read file plus its content hash.
type fileEntry struct {
	path    string
	content string
	hash    string
}

// walkRepoFiles walks the repo (reusing IndexDirectory's confined walk — which skips
// .git/.talyvor/node_modules/vendor/dist and lock/minified files), reads each
// embeddable file THROUGH Confine (S11), and returns its content + hash. The ONLY
// content that later leaves the machine is the chunk text sent to the Embedder — the
// same Lens trust boundary as a chat call.
func walkRepoFiles(root string, maxFiles int) ([]fileEntry, error) {
	fi, err := IndexDirectory(root, maxFiles)
	if err != nil {
		return nil, err
	}
	out := make([]fileEntry, 0, len(fi.Files))
	for _, f := range fi.Files {
		if !embeddableLang(f.Language) {
			continue
		}
		abs, cerr := Confine(root, f.Path) // every read is confined to root (S11)
		if cerr != nil {
			continue
		}
		content, rerr := ReadFile(abs, DefaultMaxFileBytes)
		if rerr != nil {
			continue
		}
		out = append(out, fileEntry{path: f.Path, content: content, hash: contentHash(content)})
	}
	return out, nil
}

// BuildFromRoot builds a FULL index (embeds every file). Thin wrapper over
// BuildIncremental with no prior index.
func BuildFromRoot(ctx context.Context, emb Embedder, root string, maxFiles int) (*SemanticIndex, error) {
	return BuildIncremental(ctx, emb, root, maxFiles, nil)
}

type chunkVec struct {
	chunk Chunk
	vec   []float32
}

// BuildIncremental re-indexes the repo, REUSING the chunks+vectors of files whose
// content hash matches prev, embedding ONLY new/changed files, and dropping deleted
// files' chunks. A nil prev — or a prev of a different IndexVersion or embed model —
// forces a full rebuild (mixing embed models would corrupt cosine). Per-file hashes
// are recorded on the returned index.
func BuildIncremental(ctx context.Context, emb Embedder, root string, maxFiles int, prev *SemanticIndex) (*SemanticIndex, error) {
	entries, err := walkRepoFiles(root, maxFiles)
	if err != nil {
		return nil, err
	}

	reusable := prev != nil && prev.Version == IndexVersion &&
		(prev.EmbedModel == "" || prev.EmbedModel == DefaultEmbedModel)
	prevByFile := map[string][]chunkVec{}
	if reusable {
		for i, c := range prev.Chunks {
			var v []float32
			if i < len(prev.Vectors) {
				v = prev.Vectors[i]
			}
			prevByFile[c.File] = append(prevByFile[c.File], chunkVec{chunk: c, vec: v})
		}
	}

	idx := &SemanticIndex{Version: IndexVersion, EmbedModel: DefaultEmbedModel, FileHashes: make(map[string]string, len(entries))}
	if abs, aerr := filepath.Abs(root); aerr == nil {
		idx.Root = abs
	}

	var reusedChunks []Chunk
	var reusedVecs [][]float32
	var newChunks []Chunk
	for _, e := range entries {
		idx.FileHashes[e.path] = e.hash
		if reusable && e.hash != "" && prev.FileHashes[e.path] == e.hash {
			for _, cv := range prevByFile[e.path] {
				reusedChunks = append(reusedChunks, cv.chunk)
				reusedVecs = append(reusedVecs, cv.vec)
			}
			continue
		}
		newChunks = append(newChunks, ChunkFile(e.path, e.content)...)
	}

	newVecs, err := embedChunks(ctx, emb, newChunks)
	if err != nil {
		return nil, err
	}
	idx.Chunks = append(reusedChunks, newChunks...)
	idx.Vectors = append(reusedVecs, newVecs...)
	return idx, nil
}
