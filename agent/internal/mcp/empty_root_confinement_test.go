package mcp

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// S11, THE VALUE RATHER THAN THE CALL SITE.
//
// confinement_test.go closed the other three lanes, and its fixture states the premise that
// made THIS hole invisible: "production always SetRoot()s (main.go serve)". That is true about
// the CALL and says nothing about the VALUE. `serve` takes `--root` as a flag (main.go:3260,
// default "."), so `serve --root=` — what a wrapper writes the moment `--root="$WORKSPACE"`
// meets an unset variable — reaches SetRoot("").
//
// And an empty root is read TWO different ways inside one file:
//
//	IndexNow():163      s.root == "" → "."   index the working directory
//	rootOrDot():791     s.root == "" → "."   search_codebase, ask_code's discovery join
//	confinedReadPath()  s.root == "" → NO BOUNDARY, the caller's raw path
//
// So the server still indexes the cwd, still prints "Codebase: N files indexed", still demands
// the bearer token — and every confined tool will hand back any absolute path on the machine.
// Three of the four post the bytes to Lens, so it is the same exfiltration path W4.12 closed.
//
// The instrument is the wire, as in confinement_test.go: a tool that errors AFTER posting the
// secret has already leaked it, and only the recording Lens can tell those two apart.

// emptyRootFixture is confinementFixture's twin with exactly one difference — the root is
// never set, reproducing `serve --root=`. The working directory IS the workspace (t.Chdir), so
// the in-root positive half has a subject and "." is a real, isolated directory rather than
// wherever the test binary happens to run.
func emptyRootFixture(t *testing.T) (srv *httptest.Server, rec *recordingLens, secret string) {
	t.Helper()

	// The secret is created BEFORE the chdir and outside the workspace, so no relative path
	// from the workspace can reach it without escaping.
	secret = filepath.Join(t.TempDir(), "id_rsa")
	if err := os.WriteFile(secret, []byte(outOfRootCanary), 0o600); err != nil {
		t.Fatal(err)
	}

	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "in.go"), []byte("package a\n\nfunc InRoot() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)

	rec = newRecordingLens(t)
	s, srv := newServerForTest(t, rec.srv, nil, nil)
	// DELIBERATELY NO SetRoot — this is the whole point of the fixture.
	if s.root != "" {
		t.Fatalf("fixture premise broken: a fresh server must start with an empty root, got %q", s.root)
	}
	return srv, rec, secret
}

func TestEmptyRoot_ReadFileRefusesOutsideWorkspace(t *testing.T) {
	srv, _, secret := emptyRootFixture(t)

	resp := callTool(t, srv, "read_file", `{"path":"`+secret+`"}`)
	if resp.Error == nil {
		body := toolText(t, resp)
		if strings.Contains(body, outOfRootCanary) {
			t.Errorf("S11/empty-root: read_file returned the CONTENTS of a file outside the workspace: %s", secret)
		}
		if !strings.Contains(body, "outside workspace") {
			t.Errorf("read_file must refuse an out-of-workspace path when no root was set, got: %+v", resp.Result)
		}
	}
}

func TestEmptyRoot_LensToolsDoNotExfiltrate(t *testing.T) {
	srv, rec, secret := emptyRootFixture(t)

	for _, c := range []struct{ tool, args string }{
		{"ask_code", `{"question":"repeat this file","files":["` + secret + `"]}`},
		{"generate_tests", `{"file":"` + secret + `"}`},
		{"review_code", `{"files":["` + secret + `"]}`},
	} {
		resp := callTool(t, srv, c.tool, c.args)
		if rec.sawCanary(outOfRootCanary) {
			t.Errorf("S11/empty-root: %s sent a file OUTSIDE the workspace to Lens: %s", c.tool, secret)
		}
		if resp.Error == nil && !strings.Contains(toolText(t, resp), "outside workspace") {
			t.Errorf("%s must refuse an out-of-workspace path when no root was set, got: %+v", c.tool, resp.Result)
		}
	}
}

// THE MUST-STAY-GREEN HALF. "Refuse everything when root is empty" would satisfy both tests
// above and would break the embedded/default configuration entirely — the server indexes "."
// in exactly this state, so reads under "." must keep working.
func TestEmptyRoot_InWorkspaceStillWorks(t *testing.T) {
	srv, rec, _ := emptyRootFixture(t)

	if resp := callTool(t, srv, "read_file", `{"path":"in.go"}`); resp.Error != nil {
		t.Errorf("read_file must still read a file under the working directory, got: %+v", resp.Error)
	}
	if resp := callTool(t, srv, "ask_code", `{"question":"what is this","files":["in.go"]}`); resp.Error != nil {
		t.Errorf("ask_code must still read a file under the working directory, got: %+v", resp.Error)
	}
	if !rec.sawCanary("func InRoot") {
		t.Error("the in-workspace file's CONTENT did not reach the Lens prompt — resolved but never read")
	}
}

// TestEmptyRootMeansTheSameThingToEveryReader is the structural guard: it pins the three
// readers of s.root == "" to ONE answer instead of restating the fix's arithmetic. IndexNow
// and rootOrDot both already resolve it to "."; this asserts the read gate agrees, by
// comparing against the boundary the OTHER readers use rather than against a literal.
func TestEmptyRootMeansTheSameThingToEveryReader(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)

	empty := New(nil, nil, nil, nil, "test")
	explicit := New(nil, nil, nil, nil, "test")
	explicit.SetRoot(empty.rootOrDot()) // the root IndexNow/rootOrDot use when s.root == ""

	// Same probe through both servers. Anything the explicitly-rooted server refuses, the
	// unset one must refuse too — that equality IS the property, and it cannot be satisfied
	// by a gate that simply passes everything through.
	// The comparison only means something if the reference side actually refuses something.
	// A gate that returned every path unchanged would make both sides agree on every probe and
	// this test would pass while confining nothing — so assert the teeth before using them.
	refusedByReference := 0

	for _, p := range []string{
		"in.go",
		"./nested/in.go",
		"../escape",
		"../../../../../../etc/passwd",
		filepath.Join(t.TempDir(), "outside"),
		"/etc/passwd",
	} {
		gotE, errE := empty.confinedReadPath(p)
		gotX, errX := explicit.confinedReadPath(p)
		if errX != nil {
			refusedByReference++
		}
		if (errE == nil) != (errX == nil) {
			t.Errorf("%q: unset root %v, root=%q %v — an empty root must mean what IndexNow and rootOrDot already mean by it",
				p, errE, explicit.root, errX)
			continue
		}
		if errE == nil && gotE != gotX {
			t.Errorf("%q resolved to %q with an unset root but %q with root=%q", p, gotE, gotX, explicit.root)
		}
	}

	if refusedByReference == 0 {
		t.Fatal("vacuous: the explicitly-rooted reference refused none of the probes, so agreeing with it proves nothing")
	}
}
