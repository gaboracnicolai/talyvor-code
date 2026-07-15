package codebase

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
)

// Embedder turns texts into vectors. The production implementation routes through
// the existing Lens client (internal/lens) — the SAME trust boundary as chat; no
// local model, no external service. Tests inject a deterministic stand-in. This is
// the ONLY seam through which chunk content leaves the machine.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// embedBatchSize caps how many chunk texts go to the Embedder per call so a large
// repo doesn't build one giant request.
const embedBatchSize = 64

// RetrievedChunk is a chunk plus its cosine similarity to the query.
type RetrievedChunk struct {
	Chunk
	Score float64 `json:"score"`
}

// SemanticIndex holds the embedded chunk corpus for cosine retrieval. It is a plain
// LOCAL artifact (persisted under the repo root, confined) — vectors + chunk
// metadata, nothing else.
type SemanticIndex struct {
	Root       string      `json:"root,omitempty"`
	EmbedModel string      `json:"embed_model,omitempty"`
	Chunks     []Chunk     `json:"chunks"`
	Vectors    [][]float32 `json:"vectors"`
}

// BuildIndex embeds every chunk's CONTENT (content only — not its path — so
// retrieval ranks by code content, not filename) and returns the index. Batches the
// Embedder calls. Pure given the Embedder.
func BuildIndex(ctx context.Context, emb Embedder, chunks []Chunk) (*SemanticIndex, error) {
	idx := &SemanticIndex{Chunks: chunks, Vectors: make([][]float32, 0, len(chunks))}
	for start := 0; start < len(chunks); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(chunks) {
			end = len(chunks)
		}
		texts := make([]string, 0, end-start)
		for _, c := range chunks[start:end] {
			texts = append(texts, c.Content)
		}
		vecs, err := emb.Embed(ctx, texts)
		if err != nil {
			return nil, fmt.Errorf("codebase: embed chunks: %w", err)
		}
		if len(vecs) != len(texts) {
			return nil, fmt.Errorf("codebase: embedder returned %d vectors for %d texts", len(vecs), len(texts))
		}
		idx.Vectors = append(idx.Vectors, vecs...)
	}
	return idx, nil
}

// Retrieve embeds the query and returns the top-k chunks by cosine similarity,
// sorted by descending score (deterministic file/line tiebreak). This is the
// codebase RELEVANCE source that REPLACES the path-substring FindRelevantFiles: it
// ranks by embedded content, so a chunk whose CONTENT matches the query wins even
// when its PATH shares no term. nil/empty index → nil (caller degrades gracefully).
func (idx *SemanticIndex) Retrieve(ctx context.Context, emb Embedder, query string, k int) ([]RetrievedChunk, error) {
	if idx == nil || len(idx.Chunks) == 0 {
		return nil, nil
	}
	if k <= 0 {
		k = 5
	}
	qv, err := emb.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("codebase: embed query: %w", err)
	}
	if len(qv) != 1 {
		return nil, fmt.Errorf("codebase: embedder returned %d vectors for 1 query", len(qv))
	}
	q := qv[0]
	scored := make([]RetrievedChunk, 0, len(idx.Chunks))
	for i, c := range idx.Chunks {
		var v []float32
		if i < len(idx.Vectors) {
			v = idx.Vectors[i]
		}
		scored = append(scored, RetrievedChunk{Chunk: c, Score: cosine(q, v)})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		if scored[i].File != scored[j].File {
			return scored[i].File < scored[j].File
		}
		return scored[i].StartLine < scored[j].StartLine
	})
	if len(scored) > k {
		scored = scored[:k]
	}
	return scored, nil
}

// Save writes the index as JSON, creating the parent dir. The caller is responsible
// for passing a CONFINED path under the repo root.
func (idx *SemanticIndex) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("codebase: index dir: %w", err)
	}
	data, err := json.Marshal(idx)
	if err != nil {
		return fmt.Errorf("codebase: marshal index: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("codebase: write index: %w", err)
	}
	return nil
}

// LoadIndex reads a persisted index. Returns (nil, nil) when the file is absent so
// callers can degrade to no-retrieval without special-casing.
func LoadIndex(path string) (*SemanticIndex, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("codebase: read index: %w", err)
	}
	var idx SemanticIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("codebase: parse index: %w", err)
	}
	return &idx, nil
}

// cosine is the similarity kernel. Safe on empty / mismatched-length / zero vectors
// (returns 0).
func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
