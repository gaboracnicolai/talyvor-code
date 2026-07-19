package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/talyvor/code/internal/config"
	"github.com/talyvor/code/internal/lens"
)

type fakeArtifactCommitter struct {
	calls []struct {
		OutputID, OutputPath string
		Manifest             []lens.ManifestEntry
	}
	errOn map[string]error
}

func (f *fakeArtifactCommitter) CommitArtifact(_ context.Context, outputID, outputPath string, m []lens.ManifestEntry) (bool, error) {
	f.calls = append(f.calls, struct {
		OutputID, OutputPath string
		Manifest             []lens.ManifestEntry
	}{outputID, outputPath, m})
	if err := f.errOn[outputID]; err != nil {
		return false, err
	}
	return true, nil
}

// gitModule creates a real committed git repo: {go.mod, <files>}, all clean at HEAD.
func gitModule(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
		{"add", "-A"}, {"commit", "-q", "-m", "seed"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	return root
}

const genText = "package main\nfunc main(){}" // a generation's assistant text (no trailing newline)

func onFlag(on bool) config.Config { return config.Config{CommitArtifact: on} }

// THE CORE RULE (a): disk bytes != canonical content of the generation ⇒ NOT committed. A commit whose
// slot can never be reproduced arms the attest gate with an unsatisfiable binding — worse than no commit.
func TestCommitArtifacts_DiskDiffersFromCanonical_NotCommitted(t *testing.T) {
	root := gitModule(t, map[string]string{
		"go.mod":  "module gen\ngo 1.21\n",
		"main.go": "package main\nfunc main(){ println(\"REWRITTEN SINCE\") }\n", // != canonical(genText)
	})
	rep := &fakeArtifactCommitter{}
	var log bytes.Buffer
	n := commitArtifacts(context.Background(), onFlag(true), &log, rep,
		map[string]string{"main.go": "oid-A"},
		map[string]string{"oid-A": lens.CanonicalContentSHA256(genText)},
		[]string{"main.go"}, root)
	if n != 0 || len(rep.calls) != 0 {
		t.Fatalf("a rewritten file must NOT be committed; n=%d calls=%d", n, len(rep.calls))
	}
	if !strings.Contains(log.String(), "main.go") {
		t.Errorf("the skip must be logged with the path; log=%q", log.String())
	}
}

// (b) flag OFF ⇒ ZERO calls, byte-identical behavior.
func TestCommitArtifacts_FlagOff_ZeroCalls(t *testing.T) {
	root := gitModule(t, map[string]string{"go.mod": "module gen\ngo 1.21\n", "main.go": lens.CanonicalContent(genText)})
	rep := &fakeArtifactCommitter{}
	n := commitArtifacts(context.Background(), onFlag(false), nil, rep,
		map[string]string{"main.go": "oid-A"},
		map[string]string{"oid-A": lens.CanonicalContentSHA256(genText)},
		[]string{"main.go"}, root)
	if n != 0 || len(rep.calls) != 0 {
		t.Fatalf("flag OFF must make zero calls; n=%d calls=%d", n, len(rep.calls))
	}
}

// (c) 409 (no content binding) ⇒ logged, non-fatal, other outputs still attempted, count excludes it.
func TestCommitArtifacts_409_LoggedNotFatal(t *testing.T) {
	root := gitModule(t, map[string]string{
		"go.mod": "module gen\ngo 1.21\n",
		"a.go":   lens.CanonicalContent("package main\nvar A = 1"),
		"b.go":   lens.CanonicalContent("package main\nvar B = 2"),
	})
	rep := &fakeArtifactCommitter{errOn: map[string]error{"oid-A": lens.ErrArtifactNoContentBinding}}
	var log bytes.Buffer
	n := commitArtifacts(context.Background(), onFlag(true), &log, rep,
		map[string]string{"a.go": "oid-A", "b.go": "oid-B"},
		map[string]string{
			"oid-A": lens.CanonicalContentSHA256("package main\nvar A = 1"),
			"oid-B": lens.CanonicalContentSHA256("package main\nvar B = 2"),
		},
		[]string{"a.go", "b.go"}, root)
	if len(rep.calls) != 2 {
		t.Fatalf("both outputs must be attempted (non-fatal); got %d", len(rep.calls))
	}
	if !strings.Contains(log.String(), "no content binding") {
		t.Errorf("the 409 must be logged; log=%q", log.String())
	}
	if n != 1 {
		t.Errorf("the 409 output is not committed → n=1 (oid-B only); got %d", n)
	}
}

// (d) no go.mod anywhere up to the repo root ⇒ no-op (never guess a module), logged.
func TestCommitArtifacts_NoGoMod_NoOp(t *testing.T) {
	root := gitModule(t, map[string]string{"app.py": "print('hi')\n"})
	rep := &fakeArtifactCommitter{}
	var log bytes.Buffer
	n := commitArtifacts(context.Background(), onFlag(true), &log, rep,
		map[string]string{"app.py": "oid-A"},
		map[string]string{"oid-A": lens.CanonicalContentSHA256("print('hi')")},
		[]string{"app.py"}, root)
	if n != 0 || len(rep.calls) != 0 {
		t.Fatalf("no go.mod ⇒ no-op; n=%d calls=%d", n, len(rep.calls))
	}
	if !strings.Contains(log.String(), "go.mod") {
		t.Errorf("the no-module skip must be logged; log=%q", log.String())
	}
}

// Happy path: clean module, disk == canonical ⇒ ONE commit whose manifest is exactly the module's
// tracked files (module-relative forward-slash paths) and whose output_path is the slot file.
func TestCommitArtifacts_CleanModule_CommitsFullTrackedManifest(t *testing.T) {
	slot := lens.CanonicalContent(genText)
	root := gitModule(t, map[string]string{
		"go.mod":       "module gen\ngo 1.21\n",
		"main.go":      slot,
		"README.md":    "hi\n",
		"pkg/util.go":  "package pkg\n",
		"ignored.tmp~": "junk\n", // committed too (tracked) — everything tracked is in the manifest
	})
	rep := &fakeArtifactCommitter{}
	var log bytes.Buffer
	n := commitArtifacts(context.Background(), onFlag(true), &log, rep,
		map[string]string{"main.go": "oid-A"},
		map[string]string{"oid-A": lens.CanonicalContentSHA256(genText)},
		[]string{"main.go"}, root)
	if n != 1 || len(rep.calls) != 1 {
		t.Fatalf("want exactly one commit; n=%d calls=%d log=%s", n, len(rep.calls), log.String())
	}
	call := rep.calls[0]
	if call.OutputID != "oid-A" || call.OutputPath != "main.go" {
		t.Errorf("commit = (%s,%s), want (oid-A, main.go)", call.OutputID, call.OutputPath)
	}
	got := map[string]bool{}
	for _, e := range call.Manifest {
		got[e.Path] = true
		if len(e.ContentSHA256) != 64 {
			t.Errorf("entry %s must carry a sha256 hex; got %q", e.Path, e.ContentSHA256)
		}
	}
	for _, want := range []string{"go.mod", "main.go", "README.md", "pkg/util.go", "ignored.tmp~"} {
		if !got[want] {
			t.Errorf("manifest must contain tracked file %s; got %v", want, got)
		}
	}
}

// A DIRTY module (tracked modification or untracked file) is skipped: the tree the agent's build gate
// verified is not the tree git can reproduce — committing it could arm a false compile_failed.
func TestCommitArtifacts_DirtyModule_Skipped(t *testing.T) {
	root := gitModule(t, map[string]string{"go.mod": "module gen\ngo 1.21\n", "main.go": lens.CanonicalContent(genText)})
	if err := os.WriteFile(filepath.Join(root, "untracked.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := &fakeArtifactCommitter{}
	var log bytes.Buffer
	n := commitArtifacts(context.Background(), onFlag(true), &log, rep,
		map[string]string{"main.go": "oid-A"},
		map[string]string{"oid-A": lens.CanonicalContentSHA256(genText)},
		[]string{"main.go"}, root)
	if n != 0 || len(rep.calls) != 0 {
		t.Fatalf("a dirty module must be skipped; n=%d calls=%d", n, len(rep.calls))
	}
	if !strings.Contains(log.String(), "dirty") {
		t.Errorf("the dirty skip must be logged; log=%q", log.String())
	}
}

// External requires without a vendor/ tree: Lens's attest classifier refuses such trees (offline build
// impossible), so a commitment would be pointless — skipped, logged.
func TestCommitArtifacts_ExternalRequiresNoVendor_Skipped(t *testing.T) {
	root := gitModule(t, map[string]string{
		"go.mod":  "module gen\ngo 1.21\n\nrequire example.com/x v1.0.0\n",
		"main.go": lens.CanonicalContent(genText),
	})
	rep := &fakeArtifactCommitter{}
	var log bytes.Buffer
	n := commitArtifacts(context.Background(), onFlag(true), &log, rep,
		map[string]string{"main.go": "oid-A"},
		map[string]string{"oid-A": lens.CanonicalContentSHA256(genText)},
		[]string{"main.go"}, root)
	if n != 0 || len(rep.calls) != 0 {
		t.Fatalf("external requires without vendor/ must be skipped; n=%d calls=%d", n, len(rep.calls))
	}
	if !strings.Contains(log.String(), "vendor") {
		t.Errorf("the vendor skip must be logged; log=%q", log.String())
	}
}
