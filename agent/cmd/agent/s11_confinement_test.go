package main

import (
	"os"
	"path/filepath"
	"testing"
)

// S11: agent file-writes were confined only by isAbs() — an absolute LLM-planned path is written
// verbatim, and a "../" relative path escapes filepath.Join(root, ...). Under `run --yes` this is an
// unattended arbitrary-file write (RCE-adjacent). RED (today): both escapes succeed. GREEN: writeChange
// refuses any target outside the workspace root, INDEPENDENT of the approval prompt.
func TestS11_WriteChange_RefusesEscape(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir() // a sibling dir NOT under root

	// (a) absolute path outside the workspace root
	abs := filepath.Join(outsideDir, "pwned_abs.txt")
	_ = writeChange(root, &FileChange{Path: abs, Operation: "create", NewContent: "PWNED"})
	if _, err := os.Stat(abs); err == nil {
		t.Errorf("S11: writeChange wrote an ABSOLUTE path OUTSIDE the workspace root: %s", abs)
	}

	// (b) relative "../" escape
	_ = writeChange(root, &FileChange{Path: "../pwned_rel.txt", Operation: "create", NewContent: "PWNED"})
	escaped := filepath.Join(filepath.Dir(root), "pwned_rel.txt")
	if _, err := os.Stat(escaped); err == nil {
		t.Errorf("S11: writeChange escaped via ../ to %s", escaped)
	}

	// (c) delete outside root must also be refused
	victim := filepath.Join(outsideDir, "victim.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = writeChange(root, &FileChange{Path: victim, Operation: "delete"})
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("S11: writeChange DELETED a file outside the workspace root: %s", victim)
	}

	// POSITIVE: an in-root write still works.
	if err := writeChange(root, &FileChange{Path: "sub/ok.txt", Operation: "create", NewContent: "ok"}); err != nil {
		t.Errorf("in-root write should succeed, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "sub", "ok.txt")); err != nil {
		t.Errorf("in-root file missing after write: %v", err)
	}
}
