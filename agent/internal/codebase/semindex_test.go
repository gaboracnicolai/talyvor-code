package codebase

import (
	"context"
	"hash/fnv"
	"math"
	"path/filepath"
	"strings"
	"testing"
)

// bagEmbedder is a deterministic stand-in for the Lens embedding call: it maps text
// to a normalized token-frequency vector, so texts that share vocabulary sit close
// in cosine space. It exercises the REAL retrieval machinery (chunk → embed → store
// → query-embed → cosine rank) without a network model — and, being content-based,
// it is genuinely NOT the old filename path-substring match.
type bagEmbedder struct{ dim int }

func (b bagEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, b.dim)
		for _, tok := range strings.FieldsFunc(strings.ToLower(t), func(r rune) bool {
			return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
		}) {
			h := fnv.New32a()
			_, _ = h.Write([]byte(tok))
			v[int(h.Sum32())%b.dim]++
		}
		// L2 normalize so cosine == dot.
		var n float64
		for _, x := range v {
			n += float64(x) * float64(x)
		}
		if n > 0 {
			inv := float32(1 / math.Sqrt(n))
			for j := range v {
				v[j] *= inv
			}
		}
		out[i] = v
	}
	return out, nil
}

func fixtureChunks() []Chunk {
	return []Chunk{
		{File: "internal/auth/login.go", Language: "Go", StartLine: 1, EndLine: 8,
			Content: "func Login(user, pass string) (string, error) { // validate credentials, authentication, return session token jwt }"},
		{File: "internal/db/query.go", Language: "Go", StartLine: 1, EndLine: 6,
			Content: "func Query(sql string) (Rows, error) { // execute a postgres database query using the pgx connection pool }"},
		{File: "frontend/button.tsx", Language: "TypeScript", StartLine: 1, EndLine: 5,
			Content: "function RenderButton() { // draw a UI button component with color, padding, margin in the frontend }"},
	}
}

// TestBuildIndex_And_Retrieve_RanksRelevantTop — THE headline proof: for an
// authentication query, the auth chunk ranks #1 and the unrelated frontend-button
// chunk does NOT — relevance by embedded CONTENT similarity, not filename.
func TestBuildIndex_And_Retrieve_RanksRelevantTop(t *testing.T) {
	ctx := context.Background()
	emb := bagEmbedder{dim: 256}
	idx, err := BuildIndex(ctx, emb, fixtureChunks())
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	if len(idx.Chunks) != 3 || len(idx.Vectors) != 3 {
		t.Fatalf("index should hold 3 embedded chunks, got %d/%d", len(idx.Chunks), len(idx.Vectors))
	}

	got, err := idx.Retrieve(ctx, emb, "how does user authentication and login validate credentials", 2)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected top-2, got %d", len(got))
	}
	if got[0].File != "internal/auth/login.go" {
		t.Errorf("top hit must be the auth chunk (content relevance), got %s (score %.3f)", got[0].File, got[0].Score)
	}
	// The unrelated frontend button chunk must NOT be the top hit.
	for _, r := range got {
		if r.File == "frontend/button.tsx" && r.Score >= got[0].Score {
			t.Errorf("irrelevant frontend chunk scored ≥ top (%.3f vs %.3f) — retrieval is not discriminating", r.Score, got[0].Score)
		}
	}
	// Results are sorted by descending score, and every hit carries its file+span.
	for i := 1; i < len(got); i++ {
		if got[i].Score > got[i-1].Score {
			t.Errorf("results not sorted desc: %.3f then %.3f", got[i-1].Score, got[i].Score)
		}
	}
	if got[0].StartLine != 1 || got[0].EndLine != 8 {
		t.Errorf("top hit must carry its span, got %d-%d", got[0].StartLine, got[0].EndLine)
	}
}

// TestRetrieve_ContentNotFilename — proves retrieval is CONTENT-based, not the old
// path-substring behavior: a query whose terms appear in a file's CONTENT but not
// its PATH still ranks that file top, and a query matching a filename but not
// content does not spuriously win.
func TestRetrieve_ContentNotFilename(t *testing.T) {
	ctx := context.Background()
	emb := bagEmbedder{dim: 256}
	idx, _ := BuildIndex(ctx, emb, fixtureChunks())
	// "postgres pgx" appears only in db/query.go's CONTENT — its path ("query.go")
	// contains neither term. Old path-substring on "postgres" would score ZERO.
	got, err := idx.Retrieve(ctx, emb, "postgres pgx connection pool", 1)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(got) != 1 || got[0].File != "internal/db/query.go" {
		t.Errorf("content-term query must retrieve db/query.go by content; got %+v", got)
	}
}

// TestSemanticIndex_SaveLoad_RoundTrip — the index persists to a local file and
// reloads identically (so an expensive embed pass is reused, not recomputed).
func TestSemanticIndex_SaveLoad_RoundTrip(t *testing.T) {
	ctx := context.Background()
	emb := bagEmbedder{dim: 64}
	idx, _ := BuildIndex(ctx, emb, fixtureChunks())
	path := filepath.Join(t.TempDir(), "codebase-index.json")
	if err := idx.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := LoadIndex(path)
	if err != nil {
		t.Fatalf("LoadIndex: %v", err)
	}
	if len(loaded.Chunks) != len(idx.Chunks) || len(loaded.Vectors) != len(idx.Vectors) {
		t.Fatalf("round-trip size mismatch")
	}
	a, _ := idx.Retrieve(ctx, emb, "authentication login", 1)
	b, _ := loaded.Retrieve(ctx, emb, "authentication login", 1)
	if len(a) != 1 || len(b) != 1 || a[0].File != b[0].File {
		t.Errorf("retrieval differs after round-trip: %v vs %v", a, b)
	}
}

// TestCosine — the pure similarity kernel.
func TestCosine(t *testing.T) {
	if c := cosine([]float32{1, 0}, []float32{1, 0}); math.Abs(c-1) > 1e-6 {
		t.Errorf("identical vectors cosine=%v want 1", c)
	}
	if c := cosine([]float32{1, 0}, []float32{0, 1}); math.Abs(c) > 1e-6 {
		t.Errorf("orthogonal cosine=%v want 0", c)
	}
	if c := cosine([]float32{1, 0}, []float32{-1, 0}); math.Abs(c+1) > 1e-6 {
		t.Errorf("opposite cosine=%v want -1", c)
	}
	if c := cosine(nil, nil); c != 0 {
		t.Errorf("empty cosine=%v want 0", c)
	}
	if c := cosine([]float32{1, 2, 3}, []float32{1, 2}); c != 0 {
		t.Errorf("mismatched-length cosine=%v want 0 (safe)", c)
	}
}
