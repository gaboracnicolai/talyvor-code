package lens

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/talyvor/code/internal/issueref"
)

// ⚠ RED-FIRST ON THE HEADER THAT LEAVES THE PROCESS, not on the extraction function.
//
// issueref has its own unit tests, and they cannot catch the failure that matters: a correct
// extractor whose value never reaches the wire, or — far worse — a caller that forwards the BRANCH
// NAME instead of the identifier. These assert the bytes an HTTP server actually received.

// captureHeaders runs one Complete against a test server and returns what it saw.
func captureHeaders(t *testing.T, issueID string) http.Header {
	t.Helper()
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer srv.Close()

	c, err := New(srv.URL, "test-key")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	msgs := []Message{{Role: "user", Content: "hello"}}
	if _, err := c.Complete(context.Background(), msgs, "claude-haiku-4-5", "test", "ws-1", issueID); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	return got
}

// ⚠ THE PRIVACY PIN. A branch name carries customer names, incidents and codenames. This exact
// branch must put "ENG-42" on the wire and nothing else — no substring of "acme-corp-breach" may
// appear in ANY header.
func TestIssueHeader_TransmitsOnlyTheIdentifierNeverTheBranch(t *testing.T) {
	const branch = "fix/acme-corp-breach-ENG-42"

	id := issueref.FromBranch(branch)
	if id != "ENG-42" {
		t.Fatalf("FromBranch(%q) = %q, want ENG-42", branch, id)
	}

	got := captureHeaders(t, id)
	if h := got.Get("X-Talyvor-Issue"); h != "ENG-42" {
		t.Errorf("X-Talyvor-Issue = %q, want exactly ENG-42", h)
	}
	// ⚠ SWEEP EVERY HEADER, not just the issue one — the leak this guards against is a caller that
	// puts the branch somewhere else entirely.
	for name, vals := range got {
		for _, v := range vals {
			for _, secret := range []string{"acme", "corp", "breach", branch} {
				if strings.Contains(strings.ToLower(v), secret) {
					t.Errorf("header %s = %q leaked %q from the branch name", name, v, secret)
				}
			}
		}
	}
}

// A lowercase branch must attribute to the same issue — otherwise eng-42 silently attributes to
// nothing while looking like it worked.
func TestIssueHeader_LowercaseBranchNormalisesOnTheWire(t *testing.T) {
	id := issueref.FromBranch("eng-42/fix")
	got := captureHeaders(t, id)
	if h := got.Get("X-Talyvor-Issue"); h != "ENG-42" {
		t.Errorf("X-Talyvor-Issue = %q, want ENG-42 (case-normalised)", h)
	}
}

// ⚠ NO MATCH MEANS NO ATTRIBUTION, ON THE WIRE. An empty header is what Track records as
// unattributed, which is correct; a guessed identifier would be a wrong bill.
func TestIssueHeader_UnmatchedBranchSendsNothing(t *testing.T) {
	for _, branch := range []string{"main", "master", "HEAD", "spike/try-something", ""} {
		id := issueref.FromBranch(branch)
		if id != "" {
			t.Fatalf("FromBranch(%q) = %q, want empty — this would attribute work to a guess", branch, id)
		}
		got := captureHeaders(t, id)
		if h := got.Get("X-Talyvor-Issue"); h != "" {
			t.Errorf("branch %q put %q on the wire; it must send nothing", branch, h)
		}
	}
}

// ⚠ EXPLICIT BEATS DETECTED, and the wire is where that has to be true.
func TestIssueHeader_ExplicitWinsOverTheBranch(t *testing.T) {
	id, source := issueref.Resolve("ENG-9", func() (string, error) { return "feature/ENG-42-add-login", nil })
	if id != "ENG-9" || source != "explicit" {
		t.Fatalf("Resolve = (%q,%q), want (ENG-9, explicit)", id, source)
	}
	if h := captureHeaders(t, id).Get("X-Talyvor-Issue"); h != "ENG-9" {
		t.Errorf("X-Talyvor-Issue = %q, want ENG-9 — an inference overrode a statement", h)
	}
}

// The detected identifier reaches the wire when nothing was stated.
func TestIssueHeader_DetectedIdentifierReachesTheWire(t *testing.T) {
	id, source := issueref.Resolve("", func() (string, error) { return "feature/ENG-42-add-login", nil })
	if id != "ENG-42" || source != "branch" {
		t.Fatalf("Resolve = (%q,%q), want (ENG-42, branch)", id, source)
	}
	if h := captureHeaders(t, id).Get("X-Talyvor-Issue"); h != "ENG-42" {
		t.Errorf("X-Talyvor-Issue = %q, want ENG-42", h)
	}
}
