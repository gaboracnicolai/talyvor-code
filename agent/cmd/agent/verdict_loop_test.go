package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// verdictPost is one recorded mechanical-verdict report-back.
type verdictPost struct {
	outputID string
	body     map[string]any
}

// fakeLensWithVerdicts serves queued completions (each stamped with a deterministic X-Talyvor-Output-Id) and
// records POSTs to /v1/output-verdicts/{id}/mechanical. verdictStatus is the status it returns for those
// POSTs (use 500 to simulate a reporting outage).
func fakeLensWithVerdicts(t *testing.T, replies []string, verdictStatus int) (*httptest.Server, *[]verdictPost, *string) {
	t.Helper()
	var mu sync.Mutex
	idx := 0
	lastOid := ""
	var posts []verdictPost
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(r.URL.Path, "/output-verdicts/") && strings.HasSuffix(r.URL.Path, "/mechanical") {
			seg := strings.Split(r.URL.Path, "/")
			oid := ""
			for i, s := range seg {
				if s == "output-verdicts" && i+1 < len(seg) {
					oid = seg[i+1]
				}
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			posts = append(posts, verdictPost{outputID: oid, body: body})
			w.WriteHeader(verdictStatus)
			return
		}
		if r.URL.Path == "/healthz" {
			w.WriteHeader(200)
			return
		}
		if idx >= len(replies) {
			t.Errorf("fake lens: unexpected extra completion (idx=%d)", idx)
			w.WriteHeader(500)
			return
		}
		body := replies[idx]
		lastOid = fmt.Sprintf("oid-%d", idx)
		idx++
		w.Header().Set("X-Talyvor-Output-Id", lastOid) // the K4 bound identity, per completion
		w.Header().Set("Content-Type", "application/json")
		enc, _ := json.Marshal(body)
		fmt.Fprintf(w, `{"content":[{"type":"text","text":%s}]}`, enc)
	}))
	return srv, &posts, &lastOid
}

// a fail-then-pass build script + a fix, shared by the loop tests.
func healScenario(t *testing.T) (dir, script string, replies []string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "foo.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script = filepath.Join(dir, "fake-build.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nif [ -f \"$0.ran\" ]; then echo ok; exit 0; fi\ntouch \"$0.ran\"\necho 'compile error' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	replies = []string{
		`{"plan":["modify foo"],"files":[{"path":"foo.txt","operation":"modify","description":"x"}]}`,
		"HELLO\n",
		`[{"file":"foo.txt","content":"FIXED\n"}]`,
	}
	return dir, script, replies
}

// WITH reporting ON: the repair→build cycle posts exactly one mechanical verdict for the repair's output_id,
// carrying the build's pass verdict + exit code.
func TestHealLoop_ReportsVerdict_WhenEnabled(t *testing.T) {
	dir, script, replies := healScenario(t)
	srv, posts, lastOid := fakeLensWithVerdicts(t, replies, 200)
	defer srv.Close()
	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	t.Setenv("TALYVOR_REPORT_VERDICTS", "true")
	chdirT(t, dir)

	var stdout, stderr bytes.Buffer
	if err := run([]string{"run", "--yes", "--heal", "--heal-cmd", script, "uppercase foo"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(*posts) != 1 {
		t.Fatalf("want exactly 1 verdict report (the repair→build pair), got %d: %+v", len(*posts), *posts)
	}
	p := (*posts)[0]
	if p.outputID != *lastOid {
		t.Errorf("verdict must key on the REPAIR's output_id (%s), got %s", *lastOid, p.outputID)
	}
	if p.body["verdict"] != "compiled" {
		t.Errorf("the repair fixed the build → verdict=compiled; got %v", p.body["verdict"])
	}
	if ec, _ := p.body["exit_code"].(float64); ec != 0 {
		t.Errorf("passing build → exit_code 0; got %v", p.body["exit_code"])
	}
}

// DEFAULT (reporting OFF): the loop behaves exactly as before — recovers, writes the fix, and posts NOTHING.
func TestHealLoop_NoVerdict_WhenDisabled(t *testing.T) {
	dir, script, replies := healScenario(t)
	srv, posts, _ := fakeLensWithVerdicts(t, replies, 200)
	defer srv.Close()
	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	// TALYVOR_REPORT_VERDICTS unset → default false.
	chdirT(t, dir)

	var stdout, stderr bytes.Buffer
	if err := run([]string{"run", "--yes", "--heal", "--heal-cmd", script, "uppercase foo"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "Fixed on attempt 1") {
		t.Errorf("loop must still recover with reporting off; out=%q", stdout.String())
	}
	if len(*posts) != 0 {
		t.Errorf("reporting off must post NOTHING; got %d: %+v", len(*posts), *posts)
	}
}

// A reporting failure (verdict endpoint 500) NEVER breaks the user's build — the loop still completes.
func TestHealLoop_ReportingFailureNeverBreaksBuild(t *testing.T) {
	dir, script, replies := healScenario(t)
	srv, posts, _ := fakeLensWithVerdicts(t, replies, http.StatusInternalServerError) // reporting outage
	defer srv.Close()
	t.Setenv("TALYVOR_LENS_URL", srv.URL)
	t.Setenv("TALYVOR_LENS_API_KEY", "tlv_k")
	t.Setenv("TALYVOR_WORKSPACE_ID", "ws-1")
	t.Setenv("TALYVOR_REPORT_VERDICTS", "true")
	chdirT(t, dir)

	var stdout, stderr bytes.Buffer
	if err := run([]string{"run", "--yes", "--heal", "--heal-cmd", script, "uppercase foo"}, &stdout, &stderr); err != nil {
		t.Fatalf("a reporting outage must NOT fail the run: %v", err)
	}
	if !strings.Contains(stdout.String(), "Fixed on attempt 1") {
		t.Errorf("build must still complete despite the reporting outage; out=%q", stdout.String())
	}
	if len(*posts) != 1 {
		t.Errorf("the agent still ATTEMPTED the report (got 500); posts=%d", len(*posts))
	}
	body, _ := os.ReadFile(filepath.Join(dir, "foo.txt"))
	if string(body) != "FIXED\n" {
		t.Errorf("the fix must still be written; got %q", string(body))
	}
}

func TestMechanicalVerdict_Mapping(t *testing.T) {
	cases := []struct {
		cmd     string
		success bool
		want    string
	}{
		{"go build ./...", true, "compiled"},
		{"go build ./...", false, "compile_failed"},
		{"go test ./...", true, "tests_passed"},
		{"go test ./...", false, "tests_failed"},
		{"npm run build", false, "compile_failed"},
	}
	for _, c := range cases {
		if got := mechanicalVerdict(c.cmd, c.success); got != c.want {
			t.Errorf("mechanicalVerdict(%q,%v)=%q want %q", c.cmd, c.success, got, c.want)
		}
	}
}

var _ = io.Discard
