package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/talyvor/code/internal/config"
	"github.com/talyvor/code/internal/issueref"
)

// B27.17: a branch's <key>-<n> attributes only when the key is one of the workspace's Track teams
// or a configured key — asked of a Track that answers GET /v1/workspaces/{ws}/teams.
func TestIssueKeySource_AsksTrackForItsTeamsAndAddsConfiguredKeys(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"t1","name":"Engineering","identifier":"ENG"}]`))
	}))
	defer srv.Close()

	keys := issueKeySource(config.Config{TrackURL: srv.URL, TrackAPIKey: "k", WorkspaceID: "ws-1", IssueKeys: " ops , "})
	branch := func(name string) func() (string, error) { return func() (string, error) { return name, nil } }

	for _, c := range []struct{ branch, want string }{
		{"eng-42-x", "ENG-42"},    // a Track team
		{"ops-7-hotfix", "OPS-7"}, // a configured key
		{"b18-52-x", ""},          // neither: no issue, so no header
	} {
		if id, _ := issueref.Resolve("", branch(c.branch), keys); id != c.want {
			t.Errorf("Resolve(%q) = %q, want %q", c.branch, id, c.want)
		}
	}
	if path != "/v1/workspaces/ws-1/teams" {
		t.Errorf("asked Track %q, want /v1/workspaces/ws-1/teams", path)
	}
}
