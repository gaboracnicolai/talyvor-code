package main

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/talyvor/code/internal/config"
)

func execCfg(t *testing.T, issue string) config.Config {
	t.Helper()
	srv := httptest.NewServer(nil)
	t.Cleanup(srv.Close)
	return config.Config{LensURL: srv.URL, LensAPIKey: "lens-key", ActiveIssue: issue}
}

// ⚠ THE CHILD'S FLAGS ARE THE CHILD'S. `claude -p "..."` must reach claude with -p intact; a flag
// parser that swallowed it would silently change what the developer asked to run.
func TestTheChildKeepsItsOwnFlags(t *testing.T) {
	var out, errb bytes.Buffer
	// sh -c 'printf %s "$0 $1"' prints the arguments it actually received.
	err := runExec(&out, &errb, execCfg(t, "ENG-42"),
		[]string{"--quiet", "--", "sh", "-c", `printf '%s|%s' "$1" "$2"`, "x", "-p", "explain internal/proxy"})
	if err != nil {
		t.Fatalf("runExec: %v", err)
	}
	if got := out.String(); got != "-p|explain internal/proxy" {
		t.Errorf("the child received %q, want its own -p flag and argument intact", got)
	}
}

func TestNoCommandIsAUsageErrorNotASilentProxy(t *testing.T) {
	var out, errb bytes.Buffer
	err := runExec(&out, &errb, execCfg(t, "ENG-42"), []string{"--quiet"})
	if err == nil {
		t.Fatal("no command was accepted")
	}
	if !strings.Contains(errb.String(), "exec -- claude") {
		t.Errorf("the usage text does not show how to invoke it:\n%s", errb.String())
	}
}

// ⚠ THE CHILD'S EXIT STATUS SURVIVES ALL THE WAY OUT, as a typed error main can reproduce.
func TestAFailingChildIsReportedAsItsOwnExitCode(t *testing.T) {
	var out, errb bytes.Buffer
	err := runExec(&out, &errb, execCfg(t, ""), []string{"--quiet", "--", "sh", "-c", "exit 7"})
	var code exitCode
	if !errors.As(err, &code) {
		t.Fatalf("error = %v, want an exitCode", err)
	}
	if int(code) != 7 {
		t.Errorf("exit code = %d, want 7", int(code))
	}
}

// The banner is the only chance to tell someone their spend will not be attributed.
func TestTheBannerSaysWhatWasDetectedIncludingNothing(t *testing.T) {
	for _, tc := range []struct {
		issue string
		want  string
	}{
		{"ENG-42", "issue=ENG-42"},
		{"", "issue=(none)"},
	} {
		var out, errb bytes.Buffer
		if err := runExec(&out, &errb, execCfg(t, tc.issue), []string{"--", "sh", "-c", "exit 0"}); err != nil {
			t.Fatalf("runExec: %v", err)
		}
		if !strings.Contains(errb.String(), tc.want) {
			t.Errorf("banner does not contain %q:\n%s", tc.want, errb.String())
		}
		// ⚠ AND IT SAYS THE PROMPTS ARE NOT READ. A developer is being asked to route every prompt
		// they write through a local proxy; that claim belongs on screen, not only in a README.
		if !strings.Contains(errb.String(), "never logs, stores or inspects") {
			t.Errorf("banner does not state the privacy property:\n%s", errb.String())
		}
		// ⚠ AND IT SAYS WHAT HAPPENS TO CONNECTORS, which is the consequence a user cannot see.
		if !strings.Contains(errb.String(), "connectors") {
			t.Errorf("banner does not mention connectors:\n%s", errb.String())
		}
	}
}

// ⚠ THE BANNER MUST NAME EVERY ACCOUNT THAT STOPS BEING BILLED, not just the first one this tool
// learned about.
//
// The Anthropic line has been on screen since the key was first withheld, for a stated reason: a
// developer whose own key is being kept from the child is having the bill moved, and that is worth a
// line rather than a surprise. The sidecar now does exactly the same thing to OPENAI_API_KEY —
// measured, `exec -- aider --model gpt-4o` used to reach api.openai.com on the developer's own key
// and now reaches Lens — so the same sentence is owed for the same reason.
//
// ⚠ IT ASSERTS ON THE ENVIRONMENT IT SETS, both directions. A banner line that appears whatever the
// developer has set would say nothing; one that never appears would be the silence this merge is
// about.
func TestTheBannerNamesEveryAccountThatStopsBeingBilled(t *testing.T) {
	for _, tc := range []struct {
		name     string
		env      map[string]string
		wantSaid []string
		wantNot  []string
	}{
		{
			name:     "an OpenAI key the developer set",
			env:      map[string]string{"OPENAI_API_KEY": "sk-the-developers-own", "ANTHROPIC_API_KEY": ""},
			wantSaid: []string{"OPENAI_API_KEY", "Lens is billed"},
		},
		{
			name:     "an Anthropic key the developer set",
			env:      map[string]string{"ANTHROPIC_API_KEY": "sk-ant-the-developers-own", "OPENAI_API_KEY": ""},
			wantSaid: []string{"ANTHROPIC_API_KEY", "Lens is billed"},
			wantNot:  []string{"OPENAI_API_KEY"},
		},
		{
			name:    "neither",
			env:     map[string]string{"ANTHROPIC_API_KEY": "", "OPENAI_API_KEY": "", "ANTHROPIC_AUTH_TOKEN": ""},
			wantNot: []string{"OPENAI_API_KEY", "is NOT passed"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			var out, errb bytes.Buffer
			if err := runExec(&out, &errb, execCfg(t, "ENG-42"), []string{"--", "sh", "-c", "exit 0"}); err != nil {
				t.Fatalf("runExec: %v", err)
			}
			for _, want := range tc.wantSaid {
				if !strings.Contains(errb.String(), want) {
					t.Errorf("banner does not say %q:\n%s", want, errb.String())
				}
			}
			for _, unwanted := range tc.wantNot {
				if strings.Contains(errb.String(), unwanted) {
					t.Errorf("banner says %q when nothing of the developer's was withheld:\n%s", unwanted, errb.String())
				}
			}
		})
	}
}

// ⚠ THERE IS EXACTLY ONE ISSUE EXTRACTOR, AND IT IS internal/issueref.
//
// A second one would drift from the first, and the failure mode is invisible: two components
// disagreeing about which issue a branch names produces spend attributed to the wrong ticket,
// which looks like data rather than a bug. This asserts the exec path derives nothing itself — it
// consumes the identifier main already resolved.
//
// ⚠ THE PATHS ARE VERIFIED TO EXIST BEFORE SCANNING. A guard whose source tree has moved scans an
// empty set and passes unconditionally, which is exactly how a guard becomes decoration.
func TestOnlyIssurefExtractsAnIssueFromABranch(t *testing.T) {
	root := repoRootFrom(t)
	scan := []string{
		filepath.Join(root, "cmd", "agent", "exec_cmd.go"),
		filepath.Join(root, "internal", "sidecar", "sidecar.go"),
		filepath.Join(root, "internal", "sidecar", "run.go"),
	}
	for _, f := range scan {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("this guard would scan nothing: %s is missing (%v)", f, err)
		}
	}

	fset := token.NewFileSet()
	for _, f := range scan {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		// A branch-name regexp or a call into git for the current branch would both be a second
		// extractor. Neither belongs on this path.
		for _, banned := range []string{"regexp.MustCompile", "GetCurrentBranch", "rev-parse"} {
			if bytes.Contains(src, []byte(banned)) {
				t.Errorf("%s contains %q — issue detection must stay in internal/issueref", filepath.Base(f), banned)
			}
		}
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", f, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			imp, ok := n.(*ast.ImportSpec)
			if ok && strings.Contains(imp.Path.Value, "issueref") && strings.Contains(f, "sidecar") {
				t.Errorf("%s imports issueref: the sidecar must be given an identifier, not resolve one", filepath.Base(f))
			}
			return true
		})
	}
}

// repoRootFrom walks up to the directory holding go.mod, so the guard above anchors on a path that
// exists rather than on a relative guess that quietly resolves to nothing.
func repoRootFrom(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for range 8 {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("cannot locate go.mod above %s", dir)
	return ""
}
