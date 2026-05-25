package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// withRoot creates a temp root with the supplied marker files
// and returns its path. Lets each test pick its own build system
// without fighting the host repo's go.mod.
func withRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, body := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return dir
}

// ─── DetectBuildCommand ─────────────────────────────

func TestDetectBuildCommand_FindsGoMod(t *testing.T) {
	root := withRoot(t, map[string]string{"go.mod": "module example.com/foo\n"})
	cmd, lang, err := DetectBuildCommand(root)
	if err != nil {
		t.Fatalf("DetectBuildCommand: %v", err)
	}
	if lang != LangGo {
		t.Errorf("lang = %s, want %s", lang, LangGo)
	}
	if !strings.Contains(cmd, "go build") {
		t.Errorf("cmd = %q, want to contain `go build`", cmd)
	}
}

func TestDetectBuildCommand_FindsPackageJSONWithTestScript(t *testing.T) {
	root := withRoot(t, map[string]string{
		"package.json": `{"scripts":{"test":"jest"}}`,
	})
	cmd, lang, err := DetectBuildCommand(root)
	if err != nil {
		t.Fatalf("DetectBuildCommand: %v", err)
	}
	if lang != LangTypeScript && lang != LangJavaScript {
		t.Errorf("lang = %s, want TS/JS", lang)
	}
	if !strings.Contains(cmd, "npm test") {
		t.Errorf("cmd = %q, want npm test", cmd)
	}
}

func TestDetectBuildCommand_PackageJSONWithoutTestScriptFalls(t *testing.T) {
	// A bare package.json without a "test" script shouldn't claim
	// npm test will work; we'd rather refuse than burn cycles on
	// an exit-status-1 npm scream.
	root := withRoot(t, map[string]string{
		"package.json": `{"name":"x"}`,
	})
	_, _, err := DetectBuildCommand(root)
	if err == nil {
		t.Fatal("expected error when no test script")
	}
}

func TestDetectBuildCommand_FindsCargoToml(t *testing.T) {
	root := withRoot(t, map[string]string{"Cargo.toml": "[package]\nname = \"x\"\n"})
	cmd, lang, err := DetectBuildCommand(root)
	if err != nil {
		t.Fatalf("DetectBuildCommand: %v", err)
	}
	if lang != LangRust {
		t.Errorf("lang = %s", lang)
	}
	if !strings.Contains(cmd, "cargo") {
		t.Errorf("cmd = %q", cmd)
	}
}

func TestDetectBuildCommand_FindsPythonProject(t *testing.T) {
	root := withRoot(t, map[string]string{"requirements.txt": ""})
	cmd, lang, err := DetectBuildCommand(root)
	if err != nil {
		t.Fatalf("DetectBuildCommand: %v", err)
	}
	if lang != LangPython {
		t.Errorf("lang = %s", lang)
	}
	if !strings.Contains(cmd, "pytest") {
		t.Errorf("cmd = %q", cmd)
	}
}

func TestDetectBuildCommand_FindsGemfile(t *testing.T) {
	root := withRoot(t, map[string]string{"Gemfile": "source 'https://rubygems.org'\n"})
	cmd, lang, err := DetectBuildCommand(root)
	if err != nil {
		t.Fatalf("DetectBuildCommand: %v", err)
	}
	if lang != LangRuby {
		t.Errorf("lang = %s", lang)
	}
	if !strings.Contains(cmd, "rspec") {
		t.Errorf("cmd = %q", cmd)
	}
}

func TestDetectBuildCommand_ErrorsWhenNoMarkers(t *testing.T) {
	root := withRoot(t, map[string]string{"README.md": "# nothing here"})
	_, _, err := DetectBuildCommand(root)
	if err == nil {
		t.Fatal("expected error for marker-less directory")
	}
	if !strings.Contains(err.Error(), "cannot detect") {
		t.Errorf("error should explain the failure: %v", err)
	}
}

// ─── Run ────────────────────────────────────────────

func TestRun_CapturesStdoutAndExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only command")
	}
	res, err := Run(context.Background(), `printf hello`, t.TempDir(), 5*time.Second)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit = %d", res.ExitCode)
	}
	if res.Stdout != "hello" {
		t.Errorf("stdout = %q", res.Stdout)
	}
	if res.Command != `printf hello` {
		t.Errorf("Command echoes the input: %q", res.Command)
	}
	if res.Duration <= 0 {
		t.Errorf("Duration should be positive: %v", res.Duration)
	}
}

func TestRun_NonZeroExitDoesNotError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only command")
	}
	res, err := Run(context.Background(), "false", t.TempDir(), 5*time.Second)
	if err != nil {
		t.Fatalf("Run should not error on non-zero exit: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatal("expected non-zero exit")
	}
	if IsSuccess(res) {
		t.Fatal("IsSuccess should be false for non-zero exit")
	}
}

func TestRun_TimeoutKillsProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only command")
	}
	res, err := Run(context.Background(), "sleep 10", t.TempDir(), 2*time.Second)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Sleep is killed before the 5s elapses; expect non-zero exit.
	if res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when timed out, got 0")
	}
	if res.Duration >= 10*time.Second {
		t.Fatalf("timeout did not kick in: duration = %v", res.Duration)
	}
}

func TestIsSuccess(t *testing.T) {
	if !IsSuccess(&ExecutionResult{ExitCode: 0}) {
		t.Fatal("exit 0 should be success")
	}
	if IsSuccess(&ExecutionResult{ExitCode: 1}) {
		t.Fatal("exit 1 should not be success")
	}
	if IsSuccess(nil) {
		t.Fatal("nil should not be success")
	}
}
