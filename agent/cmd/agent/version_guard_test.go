package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ⚠ ONLY AN AST CHECK CAN SEE THIS. `go build -ldflags "-X main.version=v1.2.3"` against a CONST
// exits 0, prints no warning, and produces a binary carrying the original literal. There is no
// build failure, no lint finding and no runtime error to catch it — the only symptom is that every
// release reports the same version forever, which nobody notices because it looks like a version.
//
// Measured on this repo before the fix: a binary built with -X main.version=v9.9.9-PROOF reported
// "talyvor-code 0.1.0".
func TestVersionIsAVarSoLdflagsCanStampIt(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}

	var found bool
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range vs.Names {
				if name.Name != "version" {
					continue
				}
				found = true
				if gd.Tok == token.CONST {
					t.Errorf("main.version is declared CONST. The release job stamps it with " +
						"-X main.version=$GITHUB_REF_NAME, and the linker silently ignores -X on a " +
						"constant — the build succeeds and every release reports the literal below " +
						"instead of its tag. It must be `var`.")
				}
			}
		}
	}
	if !found {
		t.Fatal("no `version` declaration found in main.go — the release job's -X flag now targets " +
			"a symbol that does not exist, which the linker also ignores silently")
	}
}

// The release job's -X path must name the symbol that actually exists. A rename on either side is
// silent in exactly the same way.
func TestReleaseJobStampsTheSymbolThatExists(t *testing.T) {
	wf, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "ci.yaml"))
	if err != nil {
		t.Skipf("workflow not readable from here: %v", err)
	}
	if !strings.Contains(string(wf), "-X main.version=") {
		t.Error("the release job no longer stamps main.version — released binaries would carry the " +
			"hardcoded literal with nothing indicating it")
	}
}
