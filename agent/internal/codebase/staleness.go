package codebase

import (
	"fmt"
	"sort"
)

// StalenessReport says whether a persisted index is out of date vs the working tree
// and which files diverged. Cheap: it walks + hashes (no embedding, no Lens).
type StalenessReport struct {
	Stale   bool
	Changed []string // new or modified files (repo-rel paths), sorted
	Deleted []string // files in the index no longer on disk, sorted
}

// Summary renders a one-line human note.
func (r StalenessReport) Summary() string {
	if !r.Stale {
		return "codebase index is fresh"
	}
	return fmt.Sprintf("codebase index is STALE (%d changed, %d deleted) — run `talyvor-code index`", len(r.Changed), len(r.Deleted))
}

// Staleness compares the working tree's per-file content hashes to the index's
// stored hashes. A nil index is a clean not-stale no-op (nothing to compare). An
// index with no stored hashes (older schema) reports every file changed, i.e. stale
// — which correctly prompts a re-index.
func Staleness(root string, idx *SemanticIndex, maxFiles int) (StalenessReport, error) {
	if idx == nil {
		return StalenessReport{}, nil
	}
	entries, err := walkRepoFiles(root, maxFiles)
	if err != nil {
		return StalenessReport{}, err
	}
	current := make(map[string]struct{}, len(entries))
	var changed []string
	for _, e := range entries {
		current[e.path] = struct{}{}
		if idx.FileHashes[e.path] != e.hash {
			changed = append(changed, e.path)
		}
	}
	var deleted []string
	for path := range idx.FileHashes {
		if _, ok := current[path]; !ok {
			deleted = append(deleted, path)
		}
	}
	sort.Strings(changed)
	sort.Strings(deleted)
	return StalenessReport{Stale: len(changed)+len(deleted) > 0, Changed: changed, Deleted: deleted}, nil
}
