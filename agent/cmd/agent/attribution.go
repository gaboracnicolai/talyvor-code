package main

import "sort"

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
