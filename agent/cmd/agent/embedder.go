package main

import (
	"context"
	"fmt"
	"io"

	"github.com/talyvor/code/internal/codebase"
	"github.com/talyvor/code/internal/config"
	"github.com/talyvor/code/internal/lens"
)

// lensEmbedder adapts the Lens client to codebase.Embedder. Every embedding call
// routes through Lens with the SAME issue-attribution + auth headers as a chat call
// — the same trust boundary; no local model, no external service.
type lensEmbedder struct {
	lc          *lens.Client
	model       string
	workspaceID string
	issueID     string
}

func (e lensEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return e.lc.Embed(ctx, texts, e.model, "embed", e.workspaceID, e.issueID)
}

// askContextChunks caps how many retrieved chunks are injected into a chat/ask
// prompt.
const askContextChunks = 6

// retrievedContext retrieves the chunks most relevant to a query and renders them as
// a prompt section (excluding `exclude`, a file already shown in full). Nil retriever
// or any retrieval error → "" so callers concatenate unconditionally and degrade to
// their prior no-retrieval behavior.
func retrievedContext(ctx context.Context, ret codebase.Retriever, query, exclude string) string {
	if ret == nil {
		return ""
	}
	chunks, err := ret.Retrieve(ctx, query, askContextChunks)
	if err != nil {
		return ""
	}
	return codebase.RelevantContextSection(chunks, exclude, askContextChunks)
}

func newLensEmbedder(lc *lens.Client, cfg config.Config) lensEmbedder {
	return lensEmbedder{lc: lc, model: codebase.DefaultEmbedModel, workspaceID: cfg.WorkspaceID, issueID: cfg.ActiveIssue}
}

// loadRetriever loads the persisted semantic index for retrieval-grounded features
// (chat, ask, agent). Absent or unreadable → nil, so callers degrade to their prior
// non-retrieval behavior instead of failing. `note` (may be nil) receives a one-line
// load status; `warn` (may be nil) receives a one-line STALENESS warning when the
// working tree has drifted from the index.
//
// Staleness is warn-only, never auto-refresh: a serving command must not silently
// spend Lens embed calls to re-index. The check is the cheap half of indexing — it
// walks + content-hashes the tree (no embedding). See BUILD_STATE for the warn-vs-
// auto-refresh and content-hash-vs-mtime forks.
func loadRetriever(lc *lens.Client, cfg config.Config, root string, note, warn io.Writer) codebase.Retriever {
	sem, err := codebase.LoadIndex(codebase.IndexPath(root))
	if err != nil || sem == nil {
		if note != nil {
			fmt.Fprintln(note, "  (no semantic index — run `talyvor-code index` for codebase-aware retrieval)")
		}
		return nil
	}
	if note != nil {
		fmt.Fprintf(note, "  semantic index: %d chunks loaded\n", len(sem.Chunks))
	}
	if warn != nil {
		if rep, serr := codebase.Staleness(root, sem, codebase.DefaultMaxFiles); serr == nil && rep.Stale {
			fmt.Fprintf(warn, "  ! %s\n", rep.Summary())
		}
	}
	return codebase.BoundIndex{Index: sem, Emb: newLensEmbedder(lc, cfg)}
}
