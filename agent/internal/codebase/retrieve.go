package codebase

import (
	"context"
	"fmt"
	"strings"
)

// Retriever is the consumer-facing RELEVANCE API — retrieve the chunks most
// relevant to a query. It REPLACES the path-substring FindRelevantFiles as the
// relevance source: chat and the agent hold a Retriever, not the index+embedder
// directly. A nil Retriever means "no index built" — consumers degrade to their
// prior behavior.
type Retriever interface {
	Retrieve(ctx context.Context, query string, k int) ([]RetrievedChunk, error)
}

// BoundIndex binds a SemanticIndex to an Embedder to satisfy Retriever, so callers
// don't thread the embedder through every call. A nil Index retrieves nothing.
type BoundIndex struct {
	Index *SemanticIndex
	Emb   Embedder
}

func (b BoundIndex) Retrieve(ctx context.Context, query string, k int) ([]RetrievedChunk, error) {
	if b.Index == nil {
		return nil, nil
	}
	return b.Index.Retrieve(ctx, b.Emb, query, k)
}

// RelevantContextSection renders retrieved chunks as a prompt section giving the
// model CROSS-FILE context. It cites each chunk by `file:start-end` and includes its
// content, but SKIPS any chunk from excludeFile (the file being edited/asked about —
// shown in full elsewhere), and shows at most maxChunks. Empty input → "" so callers
// can concatenate unconditionally. Pure.
func RelevantContextSection(chunks []RetrievedChunk, excludeFile string, maxChunks int) string {
	if len(chunks) == 0 {
		return ""
	}
	if maxChunks <= 0 {
		maxChunks = 5
	}
	var b strings.Builder
	shown := 0
	for _, c := range chunks {
		if c.File == excludeFile {
			continue
		}
		if shown >= maxChunks {
			break
		}
		if shown == 0 {
			b.WriteString("\nRelevant code from elsewhere in the repository (context only — do not rewrite these files):\n")
		}
		fmt.Fprintf(&b, "\n// %s:%d-%d\n%s\n", c.File, c.StartLine, c.EndLine, c.Content)
		shown++
	}
	return b.String()
}
