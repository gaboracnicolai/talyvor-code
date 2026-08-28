package docs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// THE CENSUS: EVERY CALL THIS CLIENT MAKES, PINNED BY METHOD *AND* PATH.
//
// ⚠ MEASURED, NOT ASSUMED. A method-mutation census over the agent (change one call's HTTP method,
// run `go test ./...`) found that BOTH of this package's GETs — Search and GetPage — could be
// changed to POST with the whole suite still green. Only AskDocs's POST was checked, and only
// because a test happened to assert a decoded response. The fakes in this repository do not look
// at the method at all.
//
// ⚠ THE EXPECTED VALUES COME FROM talyvor-docs' MEASURED ROUTE SET (main 97ba255/3b4aa4e, 100
// routes under /v1, enumerated by its own AST resolver):
//
//	GET  /v1/workspaces/{wsID}/search
//	POST /v1/workspaces/{wsID}/ai/ask
//	GET  /v1/spaces/{spaceID}/pages/{pageID}
//
// All three matched at 8eee6f0, so this closes a coverage gap rather than fixing a break. CI here
// checks out only talyvor-code, so this pins what the CLIENT sends; it cannot notice the server
// moving.

type recorded struct{ method, path, query string }

// strictFake answers only the exact (method, path) pairs it is given and 404s everything else —
// deliberately LESS forgiving than a real server.
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
	cases := []struct {
		name   string
		allow  map[string]string
		call   func(c *Client) error
		expect recorded
	}{
		{
			name:  "Search is a GET and carries q and limit",
			allow: map[string]string{"GET /v1/workspaces/ws-1/search": `{"results":[]}`},
			call: func(c *Client) error {
				_, err := c.Search(context.Background(), "ws-1", "deploy runbook", 3)
				return err
			},
			expect: recorded{"GET", "/v1/workspaces/ws-1/search", "limit=3&q=deploy+runbook"},
		},
		{
			name:  "AskDocs is a POST",
			allow: map[string]string{"POST /v1/workspaces/ws-1/ai/ask": `{"answer":"a"}`},
			call: func(c *Client) error {
				_, err := c.AskDocs(context.Background(), "ws-1", "how do I deploy?")
				return err
			},
			expect: recorded{"POST", "/v1/workspaces/ws-1/ai/ask", ""},
		},
		{
			name:  "GetPage is a GET",
			allow: map[string]string{"GET /v1/spaces/sp-1/pages/pg-1": `{"id":"pg-1"}`},
			call: func(c *Client) error {
				_, err := c.GetPage(context.Background(), "sp-1", "pg-1")
				return err
			},
			expect: recorded{"GET", "/v1/spaces/sp-1/pages/pg-1", ""},
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
			if len(rec) != 1 {
				t.Fatalf("requests = %+v, want exactly 1", rec)
			}
			if rec[0] != tc.expect {
				t.Errorf("request = %+v, want %+v", rec[0], tc.expect)
			}
		})
	}
}
