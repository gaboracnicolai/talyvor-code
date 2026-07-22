// Package shell generates shell commands from natural-language
// descriptions. The flow is:
//
//  1. Detect the user's shell + OS for prompt grounding.
//  2. Build a short system prompt that pins the model to a single
//     command, no prose, no fences.
//  3. Route through Lens with Haiku — shell commands are short
//     and latency matters more than nuance.
//  4. Strip the common artefacts (fences, preambles).
//
// Optional helpers cover safety screening + execution + a
// fix-loop suggestion for when a generated command fails.
package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/talyvor/code/internal/config"
	"github.com/talyvor/code/internal/lens"
)

// DefaultModel is the historical Haiku pin. Kept exported so the
// MCP server and other in-process callers have a stable default;
// the CLI passes its resolved model via the function arguments.
const DefaultModel = "claude-haiku-4-5"

// DetectShell extracts the shell binary name from $SHELL. Returns
// "bash" when nothing usable is in the environment so callers
// always get a non-empty default.
func DetectShell() string {
	s := os.Getenv("SHELL")
	if s == "" {
		return "bash"
	}
	base := filepath.Base(s)
	if base == "" || base == "/" || base == "." {
		return "bash"
	}
	return base
}

// DetectOS maps runtime.GOOS to the friendly name we expose to
// the LLM. The model attends better to "macOS"/"Linux" than to
// the Go runtime token.
func DetectOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	case "windows":
		return "Windows"
	case "freebsd":
		return "FreeBSD"
	case "openbsd":
		return "OpenBSD"
	}
	return runtime.GOOS
}

// BuildShellPrompt assembles the system prompt for shell
// generation. The intent is to constrain the model to ONE
// command, no fences, no prose — anything else makes downstream
// stripping flaky.
func BuildShellPrompt(description, shell, osName string) string {
	return fmt.Sprintf(`You are an expert shell command generator.
Generate a single shell command for the given task.

Rules:
- Return ONLY the command, nothing else
- No markdown, no backticks, no explanation
- Use %s syntax
- The command must work on %s
- Prefer safe commands (no -rf without confirmation)
- For complex tasks: pipe commands together
- If the task is impossible in one command, return the most practical approach

Task: %s`, shell, osName, description)
}

// dangerousPatterns are advisory matchers. The user always
// confirms before --run executes anything; this list drives an
// extra "are you sure?" warning, not a hard block.
var dangerousPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\brm\b[^|;\n]*\s-[rR]?[fF]?[rR]?\s+(/|~|/\*|/etc\b|/usr\b|/var\b|/home\b)`),
	regexp.MustCompile(`\brm\b\s+-[a-zA-Z]*[fF][a-zA-Z]*\s+/(\s|$|\*)`),
	regexp.MustCompile(`\bdd\b[^|;\n]*\bof=/dev/`),
	regexp.MustCompile(`\bchmod\b\s+(-[a-zA-Z]+\s+)?777\b\s+(/|~|/etc\b|/usr\b)`),
	regexp.MustCompile(`\bmkfs\.[a-z0-9]+\b\s+/dev/`),
	regexp.MustCompile(`>\s*/dev/sd[a-z]`),
	regexp.MustCompile(`:\(\)\s*{\s*:\|:&\s*}\s*;\s*:`),
	regexp.MustCompile(`\bshutdown\b\s+-[a-zA-Z]*[hH]`),
	regexp.MustCompile(`\bcurl\b[^|;\n]*\|\s*(sudo\s+)?sh\b`),
}

// IsCommandSafe is the advisory safety heuristic. Returns false
// when the command matches one of the dangerous-pattern regexps.
// The CLI shows an extra confirmation for false results; it
// never silently blocks.
func IsCommandSafe(command string) bool {
	for _, re := range dangerousPatterns {
		if re.MatchString(command) {
			return false
		}
	}
	return true
}

// ExecuteCommand runs the supplied command through `sh -c`
// (POSIX) or `powershell -Command` on Windows. Returns captured
// stdout, stderr, and the process exit code. A non-zero exit
// code is NOT an error — the caller decides what to do next.
func ExecuteCommand(ctx context.Context, command string) (string, string, int, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
			err = nil
		}
	}
	return stdout.String(), stderr.String(), exitCode, err
}

// preambles are the common model-generated openers we strip
// before showing the command to the user. The list is short on
// purpose — a too-aggressive sanitiser eats legitimate output.
var preambles = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(here(?:'s| is)?\s+(the\s+)?(command|shell command)\s*:?\s*)`),
	regexp.MustCompile(`(?i)^(the command (you want|is)\s*:?\s*)`),
	regexp.MustCompile(`(?i)^(command\s*:?\s*)`),
}

// stripGenerated removes fences, preambles, and surrounding
// whitespace so the user sees a bare command on stdout. The
// fence/preamble passes loop until stable because models often
// produce "Here is the command:\n```bash\n…\n```" which needs
// preamble removal *first* to expose the inner fence.
func stripGenerated(s string) string {
	out := strings.TrimSpace(s)
	for i := 0; i < 4; i++ {
		next := out
		for _, re := range preambles {
			next = re.ReplaceAllString(next, "")
		}
		next = strings.TrimSpace(next)
		if strings.HasPrefix(next, "```") {
			if nl := strings.Index(next, "\n"); nl >= 0 {
				next = next[nl+1:]
			}
		}
		if strings.HasSuffix(next, "```") {
			next = strings.TrimSuffix(next, "```")
		}
		next = strings.TrimSpace(next)
		if strings.HasPrefix(next, "`") && strings.HasSuffix(next, "`") {
			next = strings.TrimSpace(strings.Trim(next, "`"))
		}
		if next == out {
			break
		}
		out = next
	}
	// Take the first non-empty line — generated commands should
	// be single-line. Multi-line responses are usually prose
	// before the actual command.
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return strings.TrimSpace(out)
}

// Generate asks Lens for a shell command. Returns the cleaned
// command, the estimated USD cost, and any error. Empty model
// falls back to DefaultModel so callers that don't care about
// per-call routing keep the historic behaviour.
func Generate(ctx context.Context, lc *lens.Client, cfg *config.Config, description, shell, osName, model string) (string, float64, error) {
	if strings.TrimSpace(description) == "" {
		return "", 0, errors.New("shell: description is required")
	}
	if lc == nil || !lc.IsConfigured() {
		return "", 0, errors.New("shell: lens not configured")
	}
	if strings.TrimSpace(model) == "" {
		model = DefaultModel
	}
	prompt := BuildShellPrompt(description, shell, osName)
	wsID := ""
	issue := ""
	if cfg != nil {
		wsID = cfg.WorkspaceID
		issue = cfg.ActiveIssue
	}
	usage, err := lc.CompleteWithUsage(ctx,
		[]lens.Message{{Role: "user", Content: prompt}},
		model, "shell", wsID, issue,
	)
	if err != nil {
		return "", 0, err
	}
	return stripGenerated(usage.Text), usage.CostUSD, nil
}

// Explain asks Lens for a short, one-sentence-per-part
// breakdown of the supplied command. Empty model falls back to
// DefaultModel.
func Explain(ctx context.Context, lc *lens.Client, cfg *config.Config, command, shell, osName, model string) (string, float64, error) {
	if lc == nil || !lc.IsConfigured() {
		return "", 0, errors.New("shell: lens not configured")
	}
	if strings.TrimSpace(model) == "" {
		model = DefaultModel
	}
	prompt := fmt.Sprintf(`Explain this %s command briefly — one sentence per part. The user is on %s.

Command: %s`, shell, osName, command)
	wsID, issue := "", ""
	if cfg != nil {
		wsID = cfg.WorkspaceID
		issue = cfg.ActiveIssue
	}
	usage, err := lc.CompleteWithUsage(ctx,
		[]lens.Message{{Role: "user", Content: prompt}},
		model, "shell-explain", wsID, issue,
	)
	if err != nil {
		return "", 0, err
	}
	return strings.TrimSpace(usage.Text), usage.CostUSD, nil
}

// SuggestFix asks Lens to repair a command that just failed,
// given the original command + the error output. Returns ONLY
// the corrected command (stripped of fences/preambles). Empty
// model falls back to DefaultModel.
func SuggestFix(ctx context.Context, lc *lens.Client, cfg *config.Config, originalCommand, errorOutput, shell, osName, model string) (string, error) {
	if lc == nil || !lc.IsConfigured() {
		return "", errors.New("shell: lens not configured")
	}
	if strings.TrimSpace(model) == "" {
		model = DefaultModel
	}
	prompt := fmt.Sprintf(`A shell command failed. Suggest a fix.

Shell: %s
OS: %s
Original command: %s
Error: %s

Return ONLY the corrected command — no prose, no fences.`,
		shell, osName, originalCommand, errorOutput)
	wsID, issue := "", ""
	if cfg != nil {
		wsID = cfg.WorkspaceID
		issue = cfg.ActiveIssue
	}
	usage, err := lc.CompleteWithUsage(ctx,
		[]lens.Message{{Role: "user", Content: prompt}},
		model, "shell-fix", wsID, issue,
	)
	if err != nil {
		return "", err
	}
	return stripGenerated(usage.Text), nil
}
