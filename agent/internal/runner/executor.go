// Package runner powers the self-healing loop. Two responsibilities:
//
//   1. Detect a project's build/test command (`go build ./...`,
//      `npm test`, …) from on-disk markers.
//   2. Run that command, capture stdout/stderr/exit code, enforce
//      a timeout, and report a structured result.
//
// The healing logic lives in healer.go; this file is the IO side.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// Language is a typed enum so callers don't have to remember the
// stringly-typed identifiers used elsewhere in the codebase.
type Language string

const (
	LangGo         Language = "go"
	LangTypeScript Language = "typescript"
	LangJavaScript Language = "javascript"
	LangPython     Language = "python"
	LangRust       Language = "rust"
	LangRuby       Language = "ruby"
)

// ExecutionResult is the output of a single command invocation.
// Duration is wall-clock, useful for telemetry / "took 3.2s" UI.
type ExecutionResult struct {
	Command  string
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

// DetectBuildCommand inspects root for the conventional marker
// files and returns the canonical build/test command + the
// language it implies. Returns an error when no marker is found
// — callers should treat that as "skip healing" rather than a
// hard failure.
func DetectBuildCommand(root string) (string, Language, error) {
	root = stringsOrDot(root)
	// Order matters: Go > Rust > Python > Ruby > JS/TS, picking
	// the most-specific marker first. JS/TS comes last because
	// a Go monorepo with a tooling package.json shouldn't shadow
	// the Go build.
	if exists(root, "go.mod") {
		return "go build ./...", LangGo, nil
	}
	if exists(root, "Cargo.toml") {
		return "cargo check", LangRust, nil
	}
	if exists(root, "requirements.txt") || exists(root, "pyproject.toml") {
		return "python -m pytest", LangPython, nil
	}
	if exists(root, "Gemfile") {
		return "bundle exec rspec", LangRuby, nil
	}
	if exists(root, "package.json") {
		// Only claim npm test when the package.json actually has
		// a "test" script — bare manifests are common in monorepo
		// roots and `npm test` would just print "Error: no test
		// specified".
		hasTest, err := hasNPMTestScript(filepath.Join(root, "package.json"))
		if err != nil {
			return "", "", fmt.Errorf("runner: %w", err)
		}
		if !hasTest {
			return "", "", fmt.Errorf("runner: cannot detect build system (package.json has no `test` script)")
		}
		// Prefer TypeScript when a tsconfig.json sits alongside.
		lang := LangJavaScript
		if exists(root, "tsconfig.json") {
			lang = LangTypeScript
		}
		return "npm test", lang, nil
	}
	return "", "", errors.New("runner: cannot detect build system (no go.mod / Cargo.toml / requirements.txt / Gemfile / package.json)")
}

func exists(root, name string) bool {
	_, err := os.Stat(filepath.Join(root, name))
	return err == nil
}

func stringsOrDot(s string) string {
	if s == "" {
		return "."
	}
	return s
}

// hasNPMTestScript decodes package.json's scripts map far enough
// to check whether a "test" entry exists.
func hasNPMTestScript(path string) (bool, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(buf, &pkg); err != nil {
		// Treat unreadable manifest as "no test script" rather
		// than failing the whole heal flow.
		return false, nil
	}
	_, ok := pkg.Scripts["test"]
	return ok, nil
}

// Run executes `command` via the host's POSIX shell (or
// PowerShell on Windows). Stdout + stderr are captured verbatim;
// a non-zero exit is NOT an error — the caller decides what to
// do with the captured output. The supplied timeout kills the
// process if it overstays its welcome.
func Run(ctx context.Context, command, workDir string, timeout time.Duration) (*ExecutionResult, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(rctx, "powershell", "-NoProfile", "-Command", command)
	} else {
		cmd = exec.CommandContext(rctx, "sh", "-c", command)
	}
	if workDir != "" {
		cmd.Dir = workDir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)
	exitCode := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
			err = nil
		} else if rctx.Err() == context.DeadlineExceeded {
			// Context timeout fires before the process produces an
			// ExitError on some platforms — synthesise a non-zero
			// exit so callers can detect the failure.
			exitCode = 124
			err = nil
		} else {
			return &ExecutionResult{
				Command:  command,
				Stdout:   stdout.String(),
				Stderr:   stderr.String(),
				ExitCode: -1,
				Duration: duration,
			}, err
		}
	}
	return &ExecutionResult{
		Command:  command,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
		Duration: duration,
	}, nil
}

// IsSuccess returns true when the process exited cleanly.
// nil-safe so callers can chain Run → IsSuccess without an
// intermediate nil-check.
func IsSuccess(r *ExecutionResult) bool {
	return r != nil && r.ExitCode == 0
}
