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
	var full bool
	fs.IntVar(&maxFiles, "max-files", codebase.DefaultMaxFiles, "Max files to index")
	fs.BoolVar(&full, "full", false, "Force a full re-embed (ignore the existing index; default is INCREMENTAL — only changed/new files re-embed)")
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
	path := codebase.IndexPath(root)

	// Load the existing index as the incremental base (unless --full). A version/parse
	// error → fall back to a full rebuild loudly, never a silent mis-parse.
	var prev *codebase.SemanticIndex
	if !full {
		if p, lerr := codebase.LoadIndex(path); lerr != nil {
			fmt.Fprintf(stderr, "! existing index unusable (%v) — rebuilding from scratch\n", lerr)
		} else {
			prev = p
		}
	}
	if prev == nil {
		fmt.Fprintln(stdout, "▸ Building semantic codebase index (full — embeddings via Lens)…")
	} else {
		fmt.Fprintln(stdout, "▸ Re-indexing incrementally (embedding only changed/new files via Lens)…")
	}

	idx, err := codebase.BuildIncremental(context.Background(), emb, root, maxFiles, prev)
	if err != nil {
		return fmt.Errorf("index: %w", err)
	}
	if err := idx.Save(path); err != nil {
		return fmt.Errorf("index: save: %w", err)
	}
	reused, changed := indexDelta(prev, idx)
	rel, relErr := filepath.Rel(root, path)
	if relErr != nil {
		rel = path
	}
	fmt.Fprintf(stdout, "✓ Indexed %d chunks across %d files — %d re-embedded, %d reused → %s\n",
		len(idx.Chunks), countFiles(idx.Chunks), changed, reused, rel)
	fmt.Fprintf(stderr, "(embed-model=%s issue=%s)\n", codebase.DefaultEmbedModel, nonEmpty(cfg.ActiveIssue, "(none)"))
	return nil
}

// indexDelta reports how many files were reused (hash unchanged vs prev) vs
// re-embedded (new/changed) in the freshly built index.
func indexDelta(prev, idx *codebase.SemanticIndex) (reused, changed int) {
	for path, h := range idx.FileHashes {
		if prev != nil && prev.FileHashes[path] == h && h != "" {
			reused++
		} else {
			changed++
		}
	}
	return reused, changed
}

// countFiles counts the distinct files behind a chunk slice (for the index summary).
func countFiles(chunks []codebase.Chunk) int {
	seen := map[string]bool{}
	for _, c := range chunks {
		seen[c.File] = true
	}
	return len(seen)
}
