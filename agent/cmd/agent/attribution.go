package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/talyvor/code/internal/config"
	"github.com/talyvor/code/internal/lens"
)

// attributionReporter is the slice of the Lens client the caller needs. The real
// *lens.Client satisfies it; tests inject a fake. Code holds NO authority — Lens owns
// the ownership gate (append-only, first-wins).
type attributionReporter interface {
	ReportAttribution(ctx context.Context, outputID, targetKind, targetRef string) error
}

// attributePR reports each SURVIVING generation (survivingAttributions: last-writer per
// file ∩ the committed diff) as attributed to targetRef with target_kind "pr". Returns
// the count reported.
//
// Flag-gated: with ReportAttribution OFF it makes ZERO calls (byte-identical). Best-
// effort: any error (a 409 is already success inside the reporter) is logged and skipped
// — attribution NEVER fails the PR. No secrets/content are sent — ids + target_ref only.
func attributePR(ctx context.Context, cfg config.Config, log io.Writer, rep attributionReporter, editAttribution map[string]string, committedFiles []string, targetRef string) int {
	if !cfg.ReportAttribution {
		return 0
	}
	n := 0
	for _, id := range survivingAttributions(editAttribution, committedFiles) {
		err := rep.ReportAttribution(ctx, id, "pr", targetRef)
		switch {
		case err == nil:
			n++ // recorded (or an identical re-post) — silent success
		case errors.Is(err, lens.ErrAttributionConflict):
			// Already attributed to a DIFFERENT ref — a possible mis-attribution. LOG it
			// (non-fatal, success-equivalent) but do NOT count it as this PR's attribution.
			if log != nil {
				fmt.Fprintf(log, "! attribution: %s is already attributed to a different target (possible mis-attribution) — not re-claimed for this PR\n", id)
			}
		default:
			if log != nil {
				fmt.Fprintf(log, "! attribution failed (ignored) for %s: %v\n", id, err)
			}
		}
	}
	return n
}

// singlePassLastWriters builds the single-pass path's per-file last-writer map (path →
// output_id), reusing #26's survival discipline: each APPLIED Phase-2 generation records
// its file, then heal-loop repairs OVERWRITE the files they rewrote (a repair is the
// later, surviving writer). Skipped files are excluded by the caller (they never reach
// `applied`); the committed-diff filter (survivingAttributions) then drops anything that
// did not survive. Unknown (empty) ids are skipped.
func singlePassLastWriters(applied []FileChange, healAttribution map[string]string) map[string]string {
	m := make(map[string]string, len(applied)+len(healAttribution))
	for _, c := range applied {
		if c.Path != "" && c.OutputID != "" {
			m[c.Path] = c.OutputID
		}
	}
	// Heal repairs run AFTER Phase 2, so they win for any file they rewrote (last writer).
	for file, oid := range healAttribution {
		if file != "" && oid != "" {
			m[file] = oid
		}
	}
	return m
}

// survivingAttributions is the survival gate: from the loop's per-file last-writer map
// (agentloop.Result.EditAttribution), keep only the output_ids whose file actually
// survived into the COMMITTED diff (committedFiles, from `git diff base...branch`).
//
// This is the moat-integrity rule "attribute only what survived into the committed
// diff, not what was merely touched during the run":
//   - a generation whose write was fully overwritten by a later one is already dropped
//     upstream (the map holds only the last writer per file);
//   - a file edited then reverted to its base content is absent from committedFiles here
//     and so is dropped;
//   - unknown (empty) output_ids are dropped.
//
// Returns a DISTINCT, sorted list — over-attribution corrupts K4, so soundness beats
// coverage (the same rule the earlier recon STOPped on).
func survivingAttributions(editAttribution map[string]string, committedFiles []string) []string {
	committed := make(map[string]bool, len(committedFiles))
	for _, f := range committedFiles {
		committed[f] = true
	}
	seen := make(map[string]bool)
	out := make([]string, 0, len(editAttribution))
	for file, oid := range editAttribution {
		if oid == "" || !committed[file] || seen[oid] {
			continue
		}
		seen[oid] = true
		out = append(out, oid)
	}
	sort.Strings(out)
	return out
}
