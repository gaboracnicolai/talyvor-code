package codebase

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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

// BuildFromRoot indexes a whole repo: it reuses IndexDirectory's confined walk
// (which already skips node_modules/.git/vendor/dist and lock/minified files),
// reads each embeddable file THROUGH Confine (S11), chunks it, and embeds every
// chunk via the injected Embedder (production: Lens). The returned index is a local
// artifact; the caller persists it under the root. The ONLY content that leaves the
// machine is the chunk text sent to the Embedder — the same Lens trust boundary as
// a chat call.
func BuildFromRoot(ctx context.Context, emb Embedder, root string, maxFiles int) (*SemanticIndex, error) {
	fi, err := IndexDirectory(root, maxFiles)
	if err != nil {
		return nil, err
	}
	var chunks []Chunk
	for _, f := range fi.Files {
		if !embeddableLang(f.Language) {
			continue
		}
		abs, cerr := Confine(root, f.Path) // every read is confined to root
		if cerr != nil {
			continue
		}
		content, rerr := ReadFile(abs, DefaultMaxFileBytes)
		if rerr != nil {
			continue
		}
		chunks = append(chunks, ChunkFile(f.Path, content)...)
	}
	idx, err := BuildIndex(ctx, emb, chunks)
	if err != nil {
		return nil, err
	}
	if abs, aerr := filepath.Abs(root); aerr == nil {
		idx.Root = abs
	}
	return idx, nil
}
