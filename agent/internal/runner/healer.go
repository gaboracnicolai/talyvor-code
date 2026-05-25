package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// MaxHealAttempts pins the heal-loop cap at 3. Cursor-style
// healing can spiral into "fix one thing, break another" — the
// cap keeps the cost ceiling predictable.
const MaxHealAttempts = 3

// HealContext is what we feed the model when a build fails. The
// fields are deliberately concise — the model has the full file
// content too, but the surfacing of "what just failed, on which
// files" is the load-bearing context.
type HealContext struct {
	TaskDescription string
	FailedCommand   string
	ErrorOutput     string
	ChangedFiles    []string
	Language        Language
	Attempt         int
}

// FileFix is one corrected file. Content is the FULL new content,
// not a diff — easier for the agent to apply and easier for the
// model to produce.
type FileFix struct {
	File    string `json:"file"`
	Content string `json:"content"`
}

// HealResult bundles a healing attempt's outcome for telemetry.
type HealResult struct {
	FileFixes []FileFix
	Attempt   int
	Success   bool
}

// HealingPrompt builds the user-message body for the repair pass.
// We keep the JSON schema instruction at the bottom because
// models that "read the prompt top-down" still produce JSON
// when the format reminder is the last thing they see.
func HealingPrompt(ctx HealContext) string {
	var b strings.Builder
	b.WriteString("The following code changes caused build/test failures. Diagnose the error and return corrected file content.\n\n")
	fmt.Fprintf(&b, "Task: %s\n", ctx.TaskDescription)
	if ctx.Language != "" {
		fmt.Fprintf(&b, "Language: %s\n", ctx.Language)
	}
	if ctx.Attempt > 0 {
		fmt.Fprintf(&b, "Attempt: %d of %d\n", ctx.Attempt, MaxHealAttempts)
	}
	fmt.Fprintf(&b, "Command run: %s\n", ctx.FailedCommand)
	b.WriteString("\nError output:\n")
	b.WriteString(ctx.ErrorOutput)
	if !strings.HasSuffix(ctx.ErrorOutput, "\n") {
		b.WriteString("\n")
	}
	if len(ctx.ChangedFiles) > 0 {
		b.WriteString("\nFiles changed in this task:\n")
		for _, f := range ctx.ChangedFiles {
			fmt.Fprintf(&b, "  - %s\n", f)
		}
	}
	b.WriteString(`
Return a JSON array of corrected files. Each entry must include the FULL new file content, not a diff:

[
  {"file": "src/auth.ts", "content": "<full corrected file content>"}
]

Return ONLY the JSON array. No prose, no markdown fences, no explanation.`)
	return b.String()
}

// ParseHealResult decodes the healing response into FileFix
// entries. Defensive against the common ways models break the
// "ONLY JSON" rule: markdown fences, leading prose, partial
// objects. Returns a JSON-flavoured error when nothing salvages.
func ParseHealResult(raw string) ([]FileFix, error) {
	s := strings.TrimSpace(raw)
	// Strip a leading ```json fence + closing ``` if present.
	if strings.HasPrefix(s, "```") {
		if i := strings.Index(s, "\n"); i >= 0 {
			s = s[i+1:]
		}
		if j := strings.LastIndex(s, "```"); j >= 0 {
			s = strings.TrimRight(s[:j], "\n")
		}
	}
	// Salvage an inner JSON array when the model wrapped prose
	// around it.
	if !strings.HasPrefix(strings.TrimSpace(s), "[") {
		if a := strings.Index(s, "["); a >= 0 {
			if b := strings.LastIndex(s, "]"); b > a {
				s = s[a : b+1]
			}
		}
	}
	s = strings.TrimSpace(s)

	var raws []FileFix
	if err := json.Unmarshal([]byte(s), &raws); err != nil {
		return nil, fmt.Errorf("runner: invalid JSON healing response: %w", err)
	}
	// Drop incomplete entries — model output is best-effort and
	// we don't want to write empty files.
	out := make([]FileFix, 0, len(raws))
	for _, f := range raws {
		if strings.TrimSpace(f.File) == "" || f.Content == "" {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

// Compile-time guard.
var _ = errors.New
