package shell

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/talyvor/code/internal/config"
	"github.com/talyvor/code/internal/lens"
)

// ─── DetectShell ──────────────────────────────────

func TestDetectShell_FallsBackToBash(t *testing.T) {
	t.Setenv("SHELL", "")
	if got := DetectShell(); got != "bash" {
		t.Fatalf("default = %q, want bash", got)
	}
}

func TestDetectShell_ExtractsName(t *testing.T) {
	cases := map[string]string{
		"/bin/zsh":            "zsh",
		"/usr/local/bin/fish": "fish",
		"/bin/bash":           "bash",
		"powershell":          "powershell",
		"":                    "bash",
	}
	for in, want := range cases {
		t.Setenv("SHELL", in)
		if got := DetectShell(); got != want {
			t.Errorf("DetectShell(%q) = %q, want %q", in, got, want)
		}
	}
}

// ─── DetectOS ─────────────────────────────────────

func TestDetectOS_ReturnsKnownName(t *testing.T) {
	got := DetectOS()
	// Match the friendly name we surface to the model.
	switch runtime.GOOS {
	case "darwin":
		if got != "macOS" {
			t.Fatalf("darwin → %q, want macOS", got)
		}
	case "linux":
		if got != "Linux" {
			t.Fatalf("linux → %q, want Linux", got)
		}
	case "windows":
		if got != "Windows" {
			t.Fatalf("windows → %q, want Windows", got)
		}
	default:
		if got == "" {
			t.Fatalf("OS detection returned empty")
		}
	}
}

// ─── BuildShellPrompt ─────────────────────────────

func TestBuildShellPrompt_IncludesShellAndOS(t *testing.T) {
	p := BuildShellPrompt("kill process on port 8080", "zsh", "macOS")
	for _, want := range []string{"zsh", "macOS", "port 8080", "ONLY the command"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q:\n%s", want, p)
		}
	}
}

// ─── IsCommandSafe ────────────────────────────────

func TestIsCommandSafe_BlocksDangerousPatterns(t *testing.T) {
	bad := []string{
		"rm -rf /",
		"rm -rf ~",
		"rm -rf /*",
		"sudo rm -rf /etc",
		"dd if=/dev/zero of=/dev/sda",
		"chmod 777 /etc/passwd",
		"chmod -R 777 /",
		":(){ :|:& };:",
		"mkfs.ext4 /dev/sda1",
	}
	for _, cmd := range bad {
		if IsCommandSafe(cmd) {
			t.Errorf("expected unsafe: %q", cmd)
		}
	}
}

func TestIsCommandSafe_AllowsCommonOperations(t *testing.T) {
	ok := []string{
		"ls -la",
		"find . -name '*.go' -mtime -7",
		"docker ps -a",
		"kubectl get pods",
		"curl -sSL https://example.com",
		"rm /tmp/scratch.txt",
		"git status",
		"grep -rn TODO src/",
	}
	for _, cmd := range ok {
		if !IsCommandSafe(cmd) {
			t.Errorf("expected safe: %q", cmd)
		}
	}
}

// ─── ExecuteCommand ───────────────────────────────

func TestExecuteCommand_CapturesStdoutAndExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip POSIX shell on windows")
	}
	stdout, _, code, err := ExecuteCommand(context.Background(), `printf "hello"`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stdout != "hello" {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestExecuteCommand_CapturesNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip POSIX shell on windows")
	}
	_, _, code, err := ExecuteCommand(context.Background(), "false")
	if err != nil {
		t.Fatalf("execute should not error on non-zero exit, got %v", err)
	}
	if code == 0 {
		t.Fatalf("expected non-zero exit, got %d", code)
	}
}

// ─── SuggestFix ───────────────────────────────────

func TestSuggestFix_CallsLensWithErrorContext(t *testing.T) {
	var gotMsg string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 8192)
		n, _ := r.Body.Read(buf)
		gotMsg = string(buf[:n])
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ls -la"}],"usage":{"input_tokens":40,"output_tokens":4}}`))
	}))
	defer srv.Close()

	lc := lens.New(srv.URL, "tlv_k")
	cfg := &config.Config{WorkspaceID: "ws-1"}
	got, err := SuggestFix(context.Background(), lc, cfg, "ls --bogus", "ls: unknown option --bogus", "bash", "macOS", "")
	if err != nil {
		t.Fatalf("SuggestFix: %v", err)
	}
	if got != "ls -la" {
		t.Fatalf("got = %q, want ls -la", got)
	}
	for _, want := range []string{"ls --bogus", "unknown option", "bash", "macOS"} {
		if !strings.Contains(gotMsg, want) {
			t.Errorf("lens body missing %q:\n%s", want, gotMsg)
		}
	}
}

func TestSuggestFix_StripsFencesAndBackticks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{\"content\":[{\"type\":\"text\",\"text\":\"```bash\\nls -la\\n```\"}],\"usage\":{\"input_tokens\":40,\"output_tokens\":4}}"))
	}))
	defer srv.Close()
	lc := lens.New(srv.URL, "tlv_k")
	cfg := &config.Config{WorkspaceID: "ws-1"}
	got, err := SuggestFix(context.Background(), lc, cfg, "ls --bogus", "err", "bash", "macOS", "")
	if err != nil {
		t.Fatalf("SuggestFix: %v", err)
	}
	if got != "ls -la" {
		t.Fatalf("got = %q, want ls -la", got)
	}
}

// ─── Generate ─────────────────────────────────────

func TestGenerate_CallsLensWithHaiku(t *testing.T) {
	var gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 8192)
		n, _ := r.Body.Read(buf)
		body := string(buf[:n])
		if i := strings.Index(body, `"model":"`); i >= 0 {
			tail := body[i+9:]
			if j := strings.Index(tail, `"`); j >= 0 {
				gotModel = tail[:j]
			}
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"lsof -ti :8080 | xargs kill -9"}],"usage":{"input_tokens":60,"output_tokens":12}}`))
	}))
	defer srv.Close()

	lc := lens.New(srv.URL, "tlv_k")
	cfg := &config.Config{WorkspaceID: "ws-1"}
	cmd, cost, err := Generate(context.Background(), lc, cfg, "kill process on port 8080", "zsh", "macOS", "")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotModel != "claude-haiku-4-5" {
		t.Errorf("expected haiku, got %q", gotModel)
	}
	if !strings.Contains(cmd, "lsof") {
		t.Fatalf("command unexpected: %q", cmd)
	}
	if cost <= 0 {
		t.Errorf("cost should be > 0, got %v", cost)
	}
}

func TestGenerate_StripsFencesAndDescriptions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Models sometimes wrap or prepend "Here is the command:"
		_, _ = w.Write([]byte("{\"content\":[{\"type\":\"text\",\"text\":\"Here is the command:\\n```bash\\ndocker ps -a\\n```\"}],\"usage\":{\"input_tokens\":40,\"output_tokens\":4}}"))
	}))
	defer srv.Close()
	lc := lens.New(srv.URL, "tlv_k")
	cfg := &config.Config{WorkspaceID: "ws-1"}
	cmd, _, err := Generate(context.Background(), lc, cfg, "list containers", "bash", "macOS", "")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if cmd != "docker ps -a" {
		t.Fatalf("expected stripped command, got %q", cmd)
	}
}
