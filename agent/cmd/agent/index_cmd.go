package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/talyvor/code/internal/codebase"
	"github.com/talyvor/code/internal/config"
	"github.com/talyvor/code/internal/lens"
)

// runIndex builds the SEMANTIC codebase index: it walks the repo (confined to the
// root — S11), chunks every embeddable file, embeds each chunk THROUGH LENS (the
// same trust boundary as chat — no local model, no external service), and persists
// the vectors + chunk metadata to a LOCAL file under <root>/.talyvor/. Chat, ask,
// and the agent then read it for retrieval-grounded relevance. This is the
// deliberate, embed-once step; the serving commands only load + query it.
func runIndex(stdout, stderr io.Writer, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var maxFiles int
	fs.IntVar(&maxFiles, "max-files", codebase.DefaultMaxFiles, "Max files to index")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	lc := lens.New(cfg.LensURL, cfg.LensAPIKey)
	emb := newLensEmbedder(lc, cfg)

	fmt.Fprintln(stdout, "▸ Building semantic codebase index (embeddings via Lens)…")
	idx, err := codebase.BuildFromRoot(context.Background(), emb, root, maxFiles)
	if err != nil {
		return fmt.Errorf("index: %w", err)
	}
	path := codebase.IndexPath(root)
	if err := idx.Save(path); err != nil {
		return fmt.Errorf("index: save: %w", err)
	}
	rel, relErr := filepath.Rel(root, path)
	if relErr != nil {
		rel = path
	}
	fmt.Fprintf(stdout, "✓ Indexed %d chunks across %d files → %s\n", len(idx.Chunks), countFiles(idx.Chunks), rel)
	fmt.Fprintf(stderr, "(embed-model=%s issue=%s)\n", codebase.DefaultEmbedModel, nonEmpty(cfg.ActiveIssue, "(none)"))
	return nil
}

// countFiles counts the distinct files behind a chunk slice (for the index summary).
func countFiles(chunks []codebase.Chunk) int {
	seen := map[string]bool{}
	for _, c := range chunks {
		seen[c.File] = true
	}
	return len(seen)
}
