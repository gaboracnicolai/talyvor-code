package track

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// THE CENSUS: EVERY CALL THIS CLIENT MAKES, PINNED BY METHOD *AND* PATH.
//
// ⚠ WHY, MEASURED RATHER THAN ASSERTED. A path/method mutation census over the whole agent —
// break one endpoint literal, run `go test ./...`, see whether anything reds — returned:
//
//	paths:   2 of 15 sites could be pointed at a nonexistent route with the SUITE STILL GREEN
//	methods: 10 of 14 sites could have their HTTP method changed with the SUITE STILL GREEN
//
// So 12 of 29 facts about this repository's contract with three different servers were unverified.
// Every one of the twelve was CORRECT at 8eee6f0 — checked against talyvor-track's and
// talyvor-docs' measured route sets and a read of talyvor-lens's router — so this closes a
// coverage gap rather than fixing a break. It is worth closing because the fakes in this
// repository accept anything, and this package's own wire_contract_track_test.go opens with
// "W4.20 — THIS PACKAGE'S FAKE TRACK WAS MORE FORGIVING THAN TRACK." W4.30 was caught by the same
// class a second time, by a control: not one fake-backed test could tell a real Track from one
// that answers 200 to every path. Twice is a class.
//
// ⚠ THE EXPECTED VALUES ARE A CROSS-REPO MEASUREMENT AND CI CANNOT RE-VERIFY THEM. They were taken
// from talyvor-track main a41cfd0/2690f41 (136 registered routes, enumerated by its own AST
// resolver). CI here checks out only talyvor-code. What this test guarantees is that the client
// keeps sending what it sent when that comparison was made — it cannot notice the SERVER moving.

type recorded struct{ method, path, query string }

// strictFake answers only the exact (method, path) pairs it is given, and 404s everything else —
// deliberately LESS forgiving than a real server, so a client that drifts is caught here rather
// than in production.
func strictFake(t *testing.T, rec *[]recorded, allow map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*rec = append(*rec, recorded{r.Method, r.URL.Path, r.URL.RawQuery})
		body, ok := allow[r.Method+" "+r.URL.Path]
		if !ok {
			http.Error(w, "no such route", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func TestWireContract_MethodAndPath(t *testing.T) {
	const ws = "ws-1"
	const row = "6f1c9e2a-0000-4000-8000-000000000001"
	issue := `{"id":"` + row + `","identifier":"ENG-42","title":"t"}`

	cases := []struct {
		name   string
		allow  map[string]string
		call   func(c *Client) error
		expect []recorded
	}{
		{
			// GET, and the id slot answers, so exactly one request. Method mutation at
			// client.go:98 was invisible to the whole suite before this.
			name:  "GetIssue by row id",
			allow: map[string]string{"GET /v1/workspaces/ws-1/issues/" + row: issue},
			call: func(c *Client) error {
				_, err := c.GetIssue(context.Background(), ws, row)
				return err
			},
			expect: []recorded{{"GET", "/v1/workspaces/ws-1/issues/" + row, ""}},
		},
		{
			name:  "GetIssue by human key falls through to by-identifier",
			allow: map[string]string{"GET /v1/workspaces/ws-1/issues/by-identifier/ENG-42": issue},
			call: func(c *Client) error {
				_, err := c.GetIssue(context.Background(), ws, "ENG-42")
				return err
			},
			expect: []recorded{
				{"GET", "/v1/workspaces/ws-1/issues/ENG-42", ""},
				{"GET", "/v1/workspaces/ws-1/issues/by-identifier/ENG-42", ""},
			},
		},
		{
			name:  "AddComment",
			allow: map[string]string{"POST /v1/workspaces/ws-1/issues/i-1/comments": `{}`},
			call: func(c *Client) error {
				return c.AddComment(context.Background(), ws, "i-1", "hello")
			},
			expect: []recorded{{"POST", "/v1/workspaces/ws-1/issues/i-1/comments", ""}},
		},
		{
			// ⚠ THE PATH *AND* THE METHOD HERE WERE BOTH UNVERIFIED — this is the one site the
			// path census also flagged. `limit` is not decoration: talyvor-track's List handler
			// reads it (internal/issue/handler.go, q.Get("limit")), so it is part of the contract.
			name:  "ListIssues carries limit as a query parameter",
			allow: map[string]string{"GET /v1/workspaces/ws-1/issues": `[]`},
			call: func(c *Client) error {
				_, err := c.ListIssues(context.Background(), ws, 7)
				return err
			},
			expect: []recorded{{"GET", "/v1/workspaces/ws-1/issues", "limit=7"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rec []recorded
			srv := strictFake(t, &rec, tc.allow)
			defer srv.Close()
			if err := tc.call(mustNew(t, srv.URL, "tlv_k")); err != nil {
				t.Fatalf("call: %v", err)
			}
			if len(rec) != len(tc.expect) {
				t.Fatalf("requests = %+v, want %+v", rec, tc.expect)
			}
			for i := range tc.expect {
				if rec[i] != tc.expect[i] {
					t.Errorf("request %d = %+v, want %+v", i, rec[i], tc.expect[i])
				}
			}
		})
	}
}
