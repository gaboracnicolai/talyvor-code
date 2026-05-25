package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun_VersionPrintsVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"version"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), version) {
		t.Fatalf("expected version in output, got %q", stdout.String())
	}
}

func TestRun_NoArgsPrintsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(nil, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "USAGE") {
		t.Fatalf("expected USAGE help, got %q", stdout.String())
	}
}

func TestRun_UnknownCommandErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"unknownthing"}, &stdout, &stderr); err == nil {
		t.Fatal("expected error for unknown command")
	}
}

func TestRun_AskStubLinesUp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run([]string{"ask", "what is foo?"}, &stdout, &stderr); err != nil {
		t.Fatalf("run ask: %v", err)
	}
	if !strings.Contains(stdout.String(), "Phase 2") {
		t.Fatalf("expected phase 2 stub, got %q", stdout.String())
	}
}
