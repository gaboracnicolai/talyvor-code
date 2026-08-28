package track

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// THE FINDING, MEASURED: THE CLIENT HALF OF A TWO-REPO FIX NEVER SHIPPED, AND THE 404 IS SILENT.
//
// `internal/issueref` recovers a Track issue identifier FROM A GIT BRANCH NAME — "ENG-42" out of
// feature/ENG-42-add-login. That value becomes cfg.ActiveIssue, and four call sites hand it to
// GetIssue, which spends it on Track's `/v1/workspaces/{wsID}/issues/{id}` — the slot that takes
// the row id, a gen_random_uuid()::text. A human key never matches it.
//
// ⚠ TALYVOR-TRACK ALREADY FIXED ITS HALF, AND ITS HANDLER SAYS SO IN AS MANY WORDS
// (internal/issue/handler.go, above r.Get("/by-identifier/{identifier}", …)):
//
//	"Resolve an issue by its human key (ENG-42). The store lookup already existed and is already
//	 workspace-scoped; nothing served it over /v1, so THE CLI AGENT'S TRACK CLIENT PUT AN
//	 IDENTIFIER IN THE {id} SLOT BELOW AND GOT 404 FOR ISSUES THAT EXIST — measured on the wire,
//	 talyvor-code #57 (W3.6/W4.20)."
//
// The route shipped. This client was never pointed at it. `GetByIdentifier` reads
// `WHERE identifier = $1 AND workspace_id = $2`, so the two routes are disjoint: one takes a
// UUID, the other a human key, and the agent only ever asks the first for the second.
//
// ⚠ AND IT FAILS SILENTLY, WHICH IS WHY NOTHING CAUGHT IT. GetIssue maps 404 to (nil, nil) so
// callers can treat the lookup as best-effort. So:
//   - `talyvor-code check` prints "! Issue ENG-42 not found in Track" — for an issue that exists.
//   - main.go:2459 and :2568 discard the error entirely (`if iss, _ := …; iss != nil`), so the
//     agent simply runs with no issue context and says nothing.
//   - internal/mcp/server.go:770 does the same for the MCP tool.
//
// ⚠ WHY THE FIX IS DECISION-FREE, AND WHY THE ORDER IS THE PART THAT MAKES IT SO. `/{id}` is
// tried FIRST, exactly as today, and `/by-identifier/{identifier}` only after a 404. So every
// input that resolves today resolves the same way, through the same request, to the same issue —
// this can only turn 404s into 200s, never change an existing answer. Trying by-identifier first
// would be a policy choice (which route wins when a string could be both); trying it second is a
// strict superset and needs no decision.
//
// Controls: ~/talyvor-queue/w430-byidentifier-controls-x7p2.py — 11/11 armed across BOTH clients,
// every mutation compile-checked first. R1/R7 the fallback removed → red · R2/R8 by-identifier
// asked FIRST → the order assertion reds · R3/R9 the credential dropped on the retry → red ·
// R4 a 500 swallowed as a miss → red · R5 the path misspelled → red · R6 the fake answers 200 to
// ANY path → the request-log assertion reds · R10/R11 unmutated → GREEN.
//
// ⚠ THREE CONTROLS CAME BACK *INVALID* RATHER THAN ARMED ON THE FIRST RUN, AND THAT DISTINCTION IS
// THE POINT: R3's and R4's anchors each matched two or three sites in client.go (AddComment and
// ListIssues set the same header and check the same status), and R6's mutation did not compile. A
// harness that cannot separate "the mutation was malformed" from "the guard did not fire" reports
// a broken control as an armed one.
// ⚠ AND R6 WAS NOT ARMED EVEN ONCE VALID — IT FOUND A REAL GAP. It first targeted a test that
// builds its own 404-everything server, so mutating the shared fake could not reach it. Retargeted,
// it revealed that NONE of the fake-backed tests could tell a real Track from one that answers 200
// to everything: they all assert the ANSWER. That is why the first test now asserts the REQUEST
// LOG — a human key must cost exactly two requests. This repo has been caught by that class before
// (wire_contract_track_test.go: "this package's fake Track was more forgiving than Track").

// probe records the paths a fake Track was asked for, in order.
type probe struct {
	mu    sync.Mutex
	paths []string
	auth  []string
}

func (p *probe) record(r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paths = append(p.paths, r.URL.Path)
	p.auth = append(p.auth, r.Header.Get("Authorization"))
}

func (p *probe) seen() ([]string, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.paths...), append([]string(nil), p.auth...)
}

// realTrack serves the two routes talyvor-track actually serves: `{id}` matches only the row id,
// `by-identifier/{identifier}` only the human key. Everything else is 404, as Track does.
func realTrack(t *testing.T, p *probe, wsID, rowID, identifier string) *httptest.Server {
	t.Helper()
	base := "/v1/workspaces/" + wsID + "/issues"
	issue := map[string]any{"id": rowID, "identifier": identifier, "title": "Add login"}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.record(r)
		switch r.URL.Path {
		case base + "/" + rowID, base + "/by-identifier/" + identifier:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(issue)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

func TestGetIssue_ResolvesABranchDerivedIdentifier(t *testing.T) {
	p := &probe{}
	srv := realTrack(t, p, "ws-1", "6f1c9e2a-0000-4000-8000-000000000001", "ENG-42")
	defer srv.Close()

	got, err := mustNew(t, srv.URL, "tlv_k").GetIssue(context.Background(), "ws-1", "ENG-42")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got == nil {
		paths, _ := p.seen()
		t.Fatalf("GetIssue(\"ENG-42\") returned nil for an issue that EXISTS. Requests made: %v\n"+
			"internal/issueref hands this client a human key off the git branch; Track's {id} slot "+
			"takes a row id. Track ships /issues/by-identifier/{identifier} for exactly this and "+
			"nothing here calls it.", paths)
	}
	if got.Identifier != "ENG-42" {
		t.Errorf("Identifier = %q, want ENG-42", got.Identifier)
	}
	// ⚠ THE REQUEST LOG IS ASSERTED, NOT JUST THE ANSWER, AND CONTROL R6 IS WHY. A fake Track
	// that answered 200 to ANY path would satisfy every assertion above — the issue comes back,
	// the identifier matches — while proving nothing about which route served it. The invariant
	// that actually distinguishes a real Track is that a human key costs EXACTLY TWO requests:
	// the id slot misses, by-identifier answers.
	paths, _ := p.seen()
	want := []string{
		"/v1/workspaces/ws-1/issues/ENG-42",
		"/v1/workspaces/ws-1/issues/by-identifier/ENG-42",
	}
	if len(paths) != len(want) {
		t.Fatalf("requests = %v, want exactly %v — if the id slot ANSWERED for a human key the "+
			"fake is more forgiving than Track, and every test in this file passes for the wrong "+
			"reason.", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("request %d = %q, want %q", i, paths[i], want[i])
		}
	}
}

// ⚠ THE ORDER IS THE LOAD-BEARING PART OF THE FIX BEING DECISION-FREE: a row id must still be
// answered by the FIRST request, so no input that resolves today changes how it resolves.
func TestGetIssue_ARowIDStillResolvesOnTheFirstRequest(t *testing.T) {
	p := &probe{}
	rowID := "6f1c9e2a-0000-4000-8000-000000000001"
	srv := realTrack(t, p, "ws-1", rowID, "ENG-42")
	defer srv.Close()

	got, err := mustNew(t, srv.URL, "tlv_k").GetIssue(context.Background(), "ws-1", rowID)
	if err != nil || got == nil {
		t.Fatalf("GetIssue(rowID) = (%v, %v), want the issue", got, err)
	}
	paths, _ := p.seen()
	if len(paths) != 1 {
		t.Errorf("a row id took %d requests (%v), want exactly 1 — the fallback must not fire when "+
			"the id slot answers, or every existing caller pays a second round trip.", len(paths), paths)
	}
}

// ⚠ THE FALLBACK MUST CARRY THE CREDENTIAL. A second request built without the Authorization
// header 401s, and 401 is >= 400, so GetIssue would return an ERROR where it used to return
// (nil, nil) — turning a silent miss into a loud wrong one.
func TestGetIssue_TheFallbackRequestIsAuthenticated(t *testing.T) {
	p := &probe{}
	srv := realTrack(t, p, "ws-1", "6f1c9e2a-0000-4000-8000-000000000001", "ENG-42")
	defer srv.Close()

	if _, err := mustNew(t, srv.URL, "tlv_k").GetIssue(context.Background(), "ws-1", "ENG-42"); err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	paths, auth := p.seen()
	if len(paths) < 2 {
		t.Fatalf("expected a fallback request, got %v", paths)
	}
	for i, a := range auth {
		if a != "Bearer tlv_k" {
			t.Errorf("request %d (%s) carried Authorization %q, want %q", i, paths[i], a, "Bearer tlv_k")
		}
	}
}

// A genuine miss must stay a genuine miss: both routes 404 => (nil, nil), unchanged.
func TestGetIssue_BothRoutes404IsStillNil(t *testing.T) {
	p := &probe{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.record(r)
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	got, err := mustNew(t, srv.URL, "tlv_k").GetIssue(context.Background(), "ws-1", "NOPE-1")
	if err != nil {
		t.Fatalf("GetIssue: unexpected error %v", err)
	}
	if got != nil {
		t.Fatalf("GetIssue = %+v, want nil", got)
	}
	paths, _ := p.seen()
	if len(paths) != 2 {
		t.Errorf("a genuine miss made %d requests (%v), want 2 — the id slot then by-identifier", len(paths), paths)
	}
}

// A server error must still be an error, on either request — the best-effort contract is about
// 404 only, and widening it would hide a broken Track.
func TestGetIssue_A500IsAnErrorNotAMiss(t *testing.T) {
	for _, tc := range []struct{ name, failOn string }{
		{"on the id request", "id"},
		{"on the fallback request", "by-identifier"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				isFallback := containsSeg(r.URL.Path, "by-identifier")
				if (tc.failOn == "by-identifier") == isFallback {
					http.Error(w, "boom", http.StatusInternalServerError)
					return
				}
				http.Error(w, "not found", http.StatusNotFound)
			}))
			defer srv.Close()
			if _, err := mustNew(t, srv.URL, "tlv_k").GetIssue(context.Background(), "ws-1", "ENG-42"); err == nil {
				t.Error("GetIssue returned nil error for a 500 — a broken Track must not read as a miss")
			}
		})
	}
}

func containsSeg(path, seg string) bool {
	for _, p := range splitPath(path) {
		if p == seg {
			return true
		}
	}
	return false
}

func splitPath(p string) []string {
	var out []string
	cur := ""
	for i := 0; i < len(p); i++ {
		if p[i] == '/' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(p[i])
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
