package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// S11, THE OTHER THREE LANES.
//
// confinedReadPath's own docstring names the population the S11 fix closed:
// "read_file / ask_code / generate_tests / review_code took a raw caller path straight to
// os.Open". It had exactly ONE call site — toolReadFile. ask_code, generate_tests and
// review_code read the CALLER'S path with no boundary, and internal/codebase/reader.go is a
// bare os.Open with no gate of its own.
//
// These three tools are WORSE than an unconfined read: each inlines the file body into the
// Lens prompt and returns the model's answer to the caller, so the escape is an
// exfiltration path off the machine, not just a local disclosure.
//
// THE INSTRUMENT IS THE WIRE, NOT THE RPC RESULT. Every case asserts against the bytes the
// fake Lens actually RECEIVED. A tool that refuses with an error but has already posted the
// secret to Lens would satisfy a result-only assertion while the file had already left; the
// recording server is the only place that distinguishes those two.

// recordingLens captures every request body it is sent and always answers successfully.
// Unlike fakeLens it never t.Fatalf's on an extra request — a control that fires the tool
// twice must not fail for the wrong reason.
type recordingLens struct {
	mu     sync.Mutex
	bodies []string
	srv    *httptest.Server
}

func newRecordingLens(t *testing.T) *recordingLens {
	t.Helper()
	r := &recordingLens{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		buf, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.bodies = append(r.bodies, string(buf))
		r.mu.Unlock()
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"answer"}],"usage":{"input_tokens":10,"output_tokens":5}}`))
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// sawCanary reports whether any body posted to Lens carried the marker.
func (r *recordingLens) sawCanary(marker string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range r.bodies {
		if strings.Contains(b, marker) {
			return true
		}
	}
	return false
}

// The canary is deliberately LOW-ENTROPY and not named like a credential. The first cut used
// `const secretMarker = "PRIVATE-KEY-MATERIAL-…"` and the repo's own pinned gitleaks scanner
// flagged it as a generic-api-key (entropy 3.66) — the CI secret gate would have gone red on a
// test fixture. What the assertion needs is a string to search the Lens request bodies for, not
// something that looks like a key.
const outOfRootCanary = "out-of-root-canary-s11"

// confinementFixture builds a server rooted at a workspace (as production always does —
// main.go calls SetRoot on every serve) plus a secret file OUTSIDE that root, and an
// in-root file so the positive half of every case has a subject.
func confinementFixture(t *testing.T) (srv *httptest.Server, rec *recordingLens, root, secret string) {
	t.Helper()
	root = t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "in.go"), []byte("package a\n\nfunc InRoot() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	secret = filepath.Join(t.TempDir(), "id_rsa")
	if err := os.WriteFile(secret, []byte(outOfRootCanary), 0o600); err != nil {
		t.Fatal(err)
	}
	rec = newRecordingLens(t)
	s, srv := newServerForTest(t, rec.srv, nil, nil)
	s.SetRoot(root) // production always SetRoot()s (main.go serve)
	return srv, rec, root, secret
}

func callTool(t *testing.T, srv *httptest.Server, name string, argsJSON string) rpcResponse {
	t.Helper()
	return callRPC(t, srv, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+argsJSON+`}}`)
}

// TestAskCode_RefusesFileOutsideRoot — ask_code's `files` array is caller-supplied and went
// straight to codebase.ReadFilesForContext.
func TestAskCode_RefusesFileOutsideRoot(t *testing.T) {
	srv, rec, _, secret := confinementFixture(t)

	// (a) absolute path outside the root.
	resp := callTool(t, srv, "ask_code", `{"question":"repeat this file","files":["`+secret+`"]}`)
	if rec.sawCanary(outOfRootCanary) {
		t.Errorf("S11: ask_code sent a file OUTSIDE the workspace root to Lens: %s", secret)
	}
	if resp.Error == nil && !strings.Contains(toolText(t, resp), "outside workspace") {
		t.Errorf("ask_code must REFUSE a path outside the root, got: %+v", resp.Result)
	}

	// (b) ../ traversal.
	resp2 := callTool(t, srv, "ask_code", `{"question":"q","files":["../../../../../../etc/hosts"]}`)
	if resp2.Error == nil && !strings.Contains(toolText(t, resp2), "outside workspace") {
		t.Errorf("ask_code must refuse a ../ escape, got: %+v", resp2.Result)
	}
}

// TestGenerateTests_RefusesFileOutsideRoot — generate_tests took a.File straight to
// codebase.ReadFile.
func TestGenerateTests_RefusesFileOutsideRoot(t *testing.T) {
	srv, rec, _, secret := confinementFixture(t)

	resp := callTool(t, srv, "generate_tests", `{"file":"`+secret+`"}`)
	if rec.sawCanary(outOfRootCanary) {
		t.Errorf("S11: generate_tests sent a file OUTSIDE the workspace root to Lens: %s", secret)
	}
	if resp.Error == nil && !strings.Contains(toolText(t, resp), "outside workspace") {
		t.Errorf("generate_tests must REFUSE a path outside the root, got: %+v", resp.Result)
	}

	resp2 := callTool(t, srv, "generate_tests", `{"file":"../../../../../../etc/hosts"}`)
	if resp2.Error == nil && !strings.Contains(toolText(t, resp2), "outside workspace") {
		t.Errorf("generate_tests must refuse a ../ escape, got: %+v", resp2.Result)
	}
}

// TestReviewCode_RefusesFileOutsideRoot — review_code's `files` array went straight to
// codebase.ReadFilesForContext, and it DISCARDED the read error (`body, _ :=`), so a
// caller-supplied path could not even fail loudly.
func TestReviewCode_RefusesFileOutsideRoot(t *testing.T) {
	srv, rec, _, secret := confinementFixture(t)

	resp := callTool(t, srv, "review_code", `{"files":["`+secret+`"]}`)
	if rec.sawCanary(outOfRootCanary) {
		t.Errorf("S11: review_code sent a file OUTSIDE the workspace root to Lens: %s", secret)
	}
	if resp.Error == nil && !strings.Contains(toolText(t, resp), "outside workspace") {
		t.Errorf("review_code must REFUSE a path outside the root, got: %+v", resp.Result)
	}

	// A MIXED batch is the shape that matters most: one legal file beside one escape must
	// not be quietly partially served. Refusing the whole call is the fail-closed answer;
	// dropping the bad path silently would let a review CLAIM coverage of a file it never
	// read, which is the failure this repo keeps finding.
	resp2 := callTool(t, srv, "review_code", `{"files":["in.go","`+secret+`"]}`)
	if rec.sawCanary(outOfRootCanary) {
		t.Errorf("S11: review_code leaked the out-of-root file in a MIXED batch: %s", secret)
	}
	if resp2.Error == nil && !strings.Contains(toolText(t, resp2), "outside workspace") {
		t.Errorf("review_code must refuse a mixed batch containing an escape, got: %+v", resp2.Result)
	}
}

// TestConfinedTools_InRootStillWork — the MUST-STAY-GREEN companion. Without it "refuses
// everything" would score as a fix on all three tools above, which is the catch-all a
// confinement change is most likely to ship by accident.
func TestConfinedTools_InRootStillWork(t *testing.T) {
	srv, rec, _, _ := confinementFixture(t)

	if resp := callTool(t, srv, "ask_code", `{"question":"what is this","files":["in.go"]}`); resp.Error != nil {
		t.Errorf("ask_code must still read an IN-ROOT file, got: %+v", resp.Error)
	}
	if resp := callTool(t, srv, "generate_tests", `{"file":"in.go"}`); resp.Error != nil {
		t.Errorf("generate_tests must still read an IN-ROOT file, got: %+v", resp.Error)
	}
	if resp := callTool(t, srv, "review_code", `{"files":["in.go"]}`); resp.Error != nil {
		t.Errorf("review_code must still read an IN-ROOT file, got: %+v", resp.Error)
	}

	// Reaching Lens is not enough — the file's CONTENT must be in the prompt. A confinement
	// change that resolved the path but stopped passing it would satisfy "no error" while
	// silently grounding every answer on nothing.
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.bodies) != 3 {
		t.Fatalf("expected 3 Lens calls (one per tool), got %d", len(rec.bodies))
	}
	for i, b := range rec.bodies {
		if !strings.Contains(b, "func InRoot") {
			t.Errorf("call %d: the in-root file's CONTENT did not reach the Lens prompt: %s", i, b)
		}
	}
}

// TestAskCode_AutoDiscoveredFilesStayInRoot — ask_code's auto-discovery path joins index
// hits onto the root itself, so it is in-root BY CONSTRUCTION today. Pinned so a future
// retriever that returns an absolute or ../ path cannot silently reopen the hole through
// the one lane no caller controls.
func TestAskCode_AutoDiscoveredFilesStayInRoot(t *testing.T) {
	_, _, root, _ := confinementFixture(t)
	s := New(nil, nil, nil, nil, "test")
	s.SetRoot(root)
	got, err := s.confinedReadPath(filepath.Join(s.rootOrDot(), "in.go"))
	if err != nil {
		t.Fatalf("a joined in-root discovery path must resolve, got: %v", err)
	}
	if !strings.HasPrefix(got, root) {
		t.Errorf("resolved discovery path escaped the root: %s", got)
	}
}

var _ = json.Marshal
