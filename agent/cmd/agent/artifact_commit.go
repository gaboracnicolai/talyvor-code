package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/talyvor/code/internal/config"
	"github.com/talyvor/code/internal/lens"
)

// artifact_commit.go — the H5 artifact-commit caller. After a PR opens, each SURVIVING generation
// (last-writer ∩ committed diff — the same survival set as attribution) whose on-disk file STILL
// byte-equals its canonical content is committed to Lens as a buildable-artifact manifest
// (POST /v1/outputs/{id}/artifact). Lens folds the CAPTURED output_content_sha256 into the slot, so a
// commitment is satisfiable only if disk == canonical — hence THE CORE RULE below. Client-side, NO
// authority: Lens owns ownership + append-once. Flag-gated (CommitArtifact, default-off), best-effort:
// nothing here can ever fail the PR.
//
// THE MODULE SET (the manifest): all git-TRACKED files under the slot file's nearest-go.mod module root,
// hashed from disk, with the module REQUIRED CLEAN (`git status --porcelain` empty — tracked
// modifications AND untracked files both disqualify). Rationale, each part forced:
//   - whole module: any subset risks a FALSE compile_failed at attest (missing import/embed) — the one
//     unforgivable outcome; the whole tree is exactly what the agent's own build gate verified compiles.
//   - tracked-only + clean: the attest-tree supplier must reproduce the EXACT file set later; only a
//     git-reachable tree is reproducible (`git archive` at the PR commit). A dirty or untracked file
//     means the verified tree ≠ the reproducible tree → skip. (Residual, documented: a .gitignored
//     load-bearing file is invisible to the clean check — pathological for Go repos, accepted.)
//   - caps mirrored from Lens attest (attest.go: 64MB tree, 10000 entries): an over-cap module could
//     never be extracted at attest time — skip, loudly (no silent cap).
//   - external requires without vendor/ (lens class.go): the offline sandbox can never build it —
//     the commitment would be inert; skip, loudly.

// artifactCommitter is the slice of the Lens client this caller needs; tests inject a fake.
type artifactCommitter interface {
	CommitArtifact(ctx context.Context, outputID, outputPath string, contextManifest []lens.ManifestEntry) (bool, error)
}

// Caps mirrored from Lens's attest extraction (talyvor-lens internal/attest/attest.go @ 5b0b3d1:
// maxTree 64<<20, safeExtractTar entry cap 10000).
const (
	artifactMaxTreeBytes = 64 << 20
	artifactMaxFiles     = 10000
)

// commitArtifacts applies the core rule per surviving output and returns how many artifacts were
// newly committed. Every skip is logged; no path through here returns an error — the PR already
// succeeded and must stay succeeded.
func commitArtifacts(ctx context.Context, cfg config.Config, log io.Writer, client artifactCommitter,
	lastWriters map[string]string, canonicalSHA map[string]string, committedFiles []string, root string) int {
	if !cfg.CommitArtifact {
		return 0
	}
	committed := make(map[string]bool, len(committedFiles))
	for _, f := range committedFiles {
		committed[f] = true
	}
	paths := make([]string, 0, len(lastWriters))
	for p := range lastWriters {
		paths = append(paths, p)
	}
	sort.Strings(paths) // deterministic order; append-once makes repeats harmless anyway

	logf := func(format string, args ...any) {
		if log != nil {
			fmt.Fprintf(log, format, args...)
		}
	}

	modules := map[string]*moduleInfo{}
	seenOutput := map[string]bool{}
	warnedNoModule := false
	n := 0

	for _, path := range paths {
		oid := lastWriters[path]
		if oid == "" || !committed[path] || seenOutput[oid] {
			continue
		}
		canon, ok := canonicalSHA[oid]
		if !ok || canon == "" {
			logf("! artifact: %s skipped — no canonical content for its last writer (not a whole-file generation)\n", path)
			continue
		}
		// THE CORE RULE: re-read the file FROM DISK and hash it — never trust in-memory content. The
		// heal loop, a formatter, or the user may have rewritten it since the generation; a commit whose
		// slot can never be reproduced arms the attest gate with an unsatisfiable binding.
		diskBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			logf("! artifact: %s skipped — cannot read from disk: %v\n", path, err)
			continue
		}
		if sum := sha256.Sum256(diskBytes); hex.EncodeToString(sum[:]) != canon {
			logf("! artifact: %s skipped — disk bytes no longer equal the generation's canonical content (rewritten since)\n", path)
			continue
		}

		modRoot, found := findModuleRoot(root, path)
		if !found {
			if !warnedNoModule {
				logf("! artifact: no go.mod found — not a Go module, artifact commits skipped\n")
				warnedNoModule = true
			}
			continue
		}
		mod, cached := modules[modRoot]
		if !cached {
			mod = buildModuleManifest(root, modRoot)
			modules[modRoot] = mod
			if mod.skip != "" {
				logf("! artifact: module %s skipped — %s\n", mod.relRoot, mod.skip)
			}
		}
		if mod.skip != "" {
			continue
		}
		slot, rerr := filepath.Rel(modRoot, filepath.Join(root, filepath.FromSlash(path)))
		if rerr != nil {
			logf("! artifact: %s skipped — cannot resolve module-relative path: %v\n", path, rerr)
			continue
		}
		seenOutput[oid] = true
		ok2, cerr := client.CommitArtifact(ctx, oid, filepath.ToSlash(slot), mod.manifest)
		switch {
		case errors.Is(cerr, lens.ErrArtifactNoContentBinding):
			// Lens captured no content hash for this output (pre-binding, stream, extraction failure) —
			// permanently uncommittable. Logged, never fatal.
			logf("! artifact: %s has no content binding on Lens — commitment impossible for %s (skipped)\n", oid, path)
		case cerr != nil:
			logf("! artifact: commit failed (ignored) for %s: %v\n", oid, cerr)
		case ok2:
			n++
		default:
			// Append-once repeat (already committed) — silent success.
		}
	}
	return n
}

// findModuleRoot walks up from the file's directory to the repo root looking for go.mod. Never guesses:
// no go.mod ⇒ not a Go module ⇒ (\"\", false).
func findModuleRoot(root, repoRelPath string) (string, bool) {
	dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(repoRelPath)))
	rootAbs, _ := filepath.Abs(root)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, true
		}
		abs, _ := filepath.Abs(dir)
		if abs == rootAbs {
			return "", false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// moduleInfo is one module's manifest build result. skip non-empty ⇒ the whole module is uncommittable.
type moduleInfo struct {
	relRoot  string // module root relative to repo root ("." for the root module)
	manifest []lens.ManifestEntry
	skip     string
}

// buildModuleManifest enumerates the module's git-TRACKED files, requires the module clean, applies the
// attest caps and the offline-vendor rule, and hashes every file from disk (== HEAD given cleanliness).
func buildModuleManifest(root, modRoot string) *moduleInfo {
	m := &moduleInfo{}
	rel, err := filepath.Rel(root, modRoot)
	if err != nil {
		m.skip = "cannot resolve module root: " + err.Error()
		return m
	}
	m.relRoot = filepath.ToSlash(rel)
	pathspec := m.relRoot
	if pathspec == "" {
		pathspec = "."
	}

	// CLEAN: tracked modifications and untracked files both disqualify — the verified tree must equal
	// the git-reproducible tree, and an untracked .go file could be load-bearing for the build.
	status, err := gitOutput(root, "status", "--porcelain", "--", pathspec)
	if err != nil {
		m.skip = "git status failed: " + err.Error()
		return m
	}
	if strings.TrimSpace(status) != "" {
		m.skip = "module is dirty (uncommitted or untracked files) — the verified tree is not git-reproducible"
		return m
	}

	files, err := gitOutput(root, "ls-files", "-z", "--", pathspec)
	if err != nil {
		m.skip = "git ls-files failed: " + err.Error()
		return m
	}
	var total int64
	count := 0
	for _, f := range strings.Split(files, "\x00") {
		if f == "" {
			continue
		}
		count++
		if count > artifactMaxFiles {
			m.skip = fmt.Sprintf("module has more than %d tracked files — over the attest extraction cap", artifactMaxFiles)
			return m
		}
		abs := filepath.Join(root, filepath.FromSlash(f))
		b, rerr := os.ReadFile(abs)
		if rerr != nil {
			m.skip = "cannot read tracked file " + f + ": " + rerr.Error()
			return m
		}
		total += int64(len(b))
		if total > artifactMaxTreeBytes {
			m.skip = "module exceeds the attest 64MB tree cap"
			return m
		}
		modRel, rerr := filepath.Rel(modRoot, abs)
		if rerr != nil {
			m.skip = "cannot resolve module-relative path for " + f + ": " + rerr.Error()
			return m
		}
		sum := sha256.Sum256(b)
		m.manifest = append(m.manifest, lens.ManifestEntry{Path: filepath.ToSlash(modRel), ContentSHA256: hex.EncodeToString(sum[:])})
	}

	// OFFLINE-BUILDABLE (mirrors lens class.go): external requires without a vendor/ tree can never be
	// built by the network-less attest sandbox — the commitment would be inert.
	gomod, rerr := os.ReadFile(filepath.Join(modRoot, "go.mod"))
	if rerr != nil {
		m.skip = "cannot read go.mod: " + rerr.Error()
		return m
	}
	if hasExternalRequire(string(gomod)) {
		if _, verr := os.Stat(filepath.Join(modRoot, "vendor", "modules.txt")); verr != nil {
			m.skip = "module has external dependencies but no vendor/ tree — the offline attest sandbox could never build it"
			return m
		}
	}
	return m
}

// hasExternalRequire mirrors lens class.go's conservative go.mod scan: any require whose module path
// contains a dot is external (the stdlib is never in require). Single-line and block forms.
func hasExternalRequire(gomod string) bool {
	inBlock := false
	for _, raw := range strings.Split(gomod, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "require ("):
			inBlock = true
		case inBlock && line == ")":
			inBlock = false
		case inBlock && line != "" && !strings.HasPrefix(line, "//"):
			if strings.Contains(strings.Fields(line)[0], ".") {
				return true
			}
		case strings.HasPrefix(line, "require "):
			rest := strings.TrimSpace(strings.TrimPrefix(line, "require"))
			if fields := strings.Fields(rest); len(fields) >= 1 && strings.Contains(fields[0], ".") {
				return true
			}
		}
	}
	return false
}

// sha256Hex is hex(sha256(b)).
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// gitOutput runs git -C root with args and returns stdout.
func gitOutput(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
