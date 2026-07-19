package agentloop

import (
	"context"
	"testing"

	"github.com/talyvor/code/internal/lens"
)

// TestLoop_OutputCanonicalSHA_RecordedPerGeneration — the H5 artifact-commit rule needs, per output_id,
// the sha256 of that generation's CANONICAL reply text (the value Lens captured as output_content_sha256).
// The loop records it at the only moment the reply text and its output_id are paired (transcript trimming
// makes post-hoc pairing impossible). For iterative generations the canonical text is the whole tool-call
// reply — which is exactly why the commit rule (disk == canonical) naturally declines to commit them; the
// map still must be SOUND so the rule can decide.
func TestLoop_OutputCanonicalSHA_RecordedPerGeneration(t *testing.T) {
	root := t.TempDir()
	replyA := `{"tool":"edit_file","args":{"path":"foo.go","content":"package foo\n"}}`
	replyDone := `{"tool":"done","args":{"summary":"ok"}}`
	model := &oidModel{
		replies: []string{replyA, replyDone},
		oids:    []string{"oid-A", "oid-done"},
	}
	res, err := New(model, DefaultTools(root, nil), Config{MaxSteps: 10}).Run(context.Background(), "t")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := res.OutputCanonicalSHA["oid-A"], lens.CanonicalContentSHA256(replyA); got != want {
		t.Errorf("oid-A canonical sha = %q, want sha of its canonical reply %q", got, want)
	}
	if got, want := res.OutputCanonicalSHA["oid-done"], lens.CanonicalContentSHA256(replyDone); got != want {
		t.Errorf("oid-done canonical sha = %q, want %q", got, want)
	}
}
