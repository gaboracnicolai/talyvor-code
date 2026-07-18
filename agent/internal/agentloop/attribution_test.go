package agentloop

import (
	"context"
	"testing"
)

// TestLoop_EditAttribution_LastWriterPerFile — the survival gate, level 1: the loop
// records, per file, the output_id of the LAST edit_file generation that wrote it.
// edit_file writes a file's COMPLETE content, so an earlier generation whose write is
// fully overwritten by a later one MUST NOT survive — over-attribution corrupts the
// moat. Proves the mandated case: generation A (oid-A) superseded by generation B
// (oid-B) on foo.go → foo.go attributes to B only; A does not appear.
func TestLoop_EditAttribution_LastWriterPerFile(t *testing.T) {
	root := t.TempDir()
	model := &oidModel{
		replies: []string{
			`{"tool":"edit_file","args":{"path":"foo.go","content":"package foo\n// A\n"}}`,
			`{"tool":"edit_file","args":{"path":"bar.go","content":"package bar\n"}}`,
			`{"tool":"edit_file","args":{"path":"foo.go","content":"package foo\n// B supersedes A\n"}}`,
			`{"tool":"done","args":{"summary":"ok"}}`,
		},
		oids: []string{"oid-A", "oid-C-bar", "oid-B", "oid-done"},
	}
	res, err := New(model, DefaultTools(root, nil), Config{MaxSteps: 10}).Run(context.Background(), "t")
	if err != nil {
		t.Fatal(err)
	}
	if res.EditAttribution["foo.go"] != "oid-B" {
		t.Errorf("foo.go attributed to %q, want oid-B (last writer; A superseded)", res.EditAttribution["foo.go"])
	}
	if res.EditAttribution["bar.go"] != "oid-C-bar" {
		t.Errorf("bar.go attributed to %q, want oid-C-bar", res.EditAttribution["bar.go"])
	}
	for f, id := range res.EditAttribution {
		if id == "oid-A" {
			t.Errorf("superseded oid-A must not survive; found on %s", f)
		}
	}
}

// TestLoop_EditAttribution_SkipsUnknownAndFailedEdits — an edit with no output_id
// (gateway returned none) or a failed edit_file contributes nothing to record.
func TestLoop_EditAttribution_SkipsUnknownAndFailedEdits(t *testing.T) {
	root := t.TempDir()
	model := &oidModel{
		replies: []string{
			`{"tool":"edit_file","args":{"path":"ok.go","content":"package ok\n"}}`,
			`{"tool":"edit_file","args":{"path":"../escape.go","content":"x"}}`, // S11 refusal → tool error
			`{"tool":"done","args":{"summary":"ok"}}`,
		},
		oids: []string{"oid-ok", "oid-escape", "oid-done"},
	}
	res, err := New(model, DefaultTools(root, nil), Config{MaxSteps: 10}).Run(context.Background(), "t")
	if err != nil {
		t.Fatal(err)
	}
	if res.EditAttribution["ok.go"] != "oid-ok" {
		t.Errorf("ok.go attributed to %q, want oid-ok", res.EditAttribution["ok.go"])
	}
	if _, present := res.EditAttribution["../escape.go"]; present {
		t.Error("a refused (failed) edit_file must not be recorded")
	}
}
