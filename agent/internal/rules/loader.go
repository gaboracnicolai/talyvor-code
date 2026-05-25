// Package rules loads a project's `.talyvor-rules` file and
// surfaces section-targeted views for the CLI and extension to
// prepend to LLM prompts. The format is intentionally simple:
// INI-style sections separated by `[name]` headers, body lines
// taken verbatim. We want a team to feel like they're editing a
// README, not a config DSL.
package rules

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RulesFileName is the on-disk file name we search for. Matches
// the convention .editorconfig/.gitignore/.prettierrc use: hidden
// dotfile at the repo root.
const RulesFileName = ".talyvor-rules"

// MaxRulesFileSize is the size cap. Beyond 32KB the rules eat
// enough prompt budget to dilute the user's actual question.
const MaxRulesFileSize = 32 * 1024

// Rules is the parsed file plus the original content + path. The
// path is useful for "rules from .../talyvor-code/.talyvor-rules"
// breadcrumbs in the IDE.
type Rules struct {
	Raw      string
	FilePath string
	Sections RuleSections
}

// RuleSections holds the four built-in sections plus a map of
// language-specific rule blobs. Languages are normalised to
// lower-case so `[GO]` and `[go]` both land in `Languages["go"]`.
type RuleSections struct {
	General   string
	Languages map[string]string
	Agent     string
	Testing   string
	Review    string
}

// Load searches for a `.talyvor-rules` file starting at `root`
// and walking up to the filesystem root. Returns (nil, nil) when
// no file is found — rules are always optional. Returns an error
// only when a file exists but can't be read or exceeds the size
// cap; callers should propagate that to the user so they don't
// silently get unenforced rules.
func Load(root string) (*Rules, error) {
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	dir := abs
	for {
		candidate := filepath.Join(dir, RulesFileName)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			if info.Size() > MaxRulesFileSize {
				return nil, fmt.Errorf("rules: %s exceeds max size of %d bytes (got %d)", candidate, MaxRulesFileSize, info.Size())
			}
			body, err := os.ReadFile(candidate)
			if err != nil {
				return nil, err
			}
			return &Rules{
				Raw:      string(body),
				FilePath: candidate,
				Sections: Parse(string(body)),
			}, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, nil
		}
		dir = parent
	}
}

// Parse splits an INI-style body into sections. Section names
// are normalised to lower-case; section bodies are trimmed of
// leading/trailing whitespace; comment lines starting with `#`
// are dropped. Empty sections are not stored.
func Parse(content string) RuleSections {
	out := RuleSections{Languages: map[string]string{}}
	current := ""
	var buf strings.Builder
	flush := func() {
		body := strings.TrimSpace(buf.String())
		buf.Reset()
		if body == "" || current == "" {
			return
		}
		switch current {
		case "general":
			out.General = body
		case "agent":
			out.Agent = body
		case "testing":
			out.Testing = body
		case "review":
			out.Review = body
		default:
			out.Languages[current] = body
		}
	}
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimRight(raw, "\r")
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "#") {
			continue
		}
		if strings.HasPrefix(trim, "[") && strings.HasSuffix(trim, "]") && len(trim) >= 3 {
			flush()
			current = strings.ToLower(strings.TrimSpace(trim[1 : len(trim)-1]))
			continue
		}
		if current == "" {
			continue
		}
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	flush()
	return out
}

// ForLanguage returns the general + language-specific blob a
// completion/chat call should prepend. Empty for nil rules so
// callers can build their prompts unconditionally.
func ForLanguage(r *Rules, languageID string) string {
	if r == nil {
		return ""
	}
	return combine(r.Sections.General, langSection(r, languageID))
}

// ForAgent returns general + agent rules for the agentic flow.
func ForAgent(r *Rules) string {
	if r == nil {
		return ""
	}
	return combine(r.Sections.General, r.Sections.Agent)
}

// ForReview returns general + review rules for code-review.
func ForReview(r *Rules) string {
	if r == nil {
		return ""
	}
	return combine(r.Sections.General, r.Sections.Review)
}

// ForTesting returns general + testing + language rules for
// test-generation prompts.
func ForTesting(r *Rules, languageID string) string {
	if r == nil {
		return ""
	}
	return combine(r.Sections.General, r.Sections.Testing, langSection(r, languageID))
}

// langSection looks up a normalised language key. Returns empty
// when no language match (or no Languages map at all).
func langSection(r *Rules, languageID string) string {
	if r == nil || r.Sections.Languages == nil {
		return ""
	}
	return r.Sections.Languages[strings.ToLower(strings.TrimSpace(languageID))]
}

// combine joins non-empty section blobs with a blank line.
// Trailing whitespace is normalised so the prompt prefix never
// trails into the user message with extra newlines.
func combine(parts ...string) string {
	var keep []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, "\n\n")
}

// PromptPrefix wraps a combined rules blob in the canonical
// "Project rules" envelope. Injected at the start of LLM system
// prompts because models attend better to the beginning of the
// context window.
func PromptPrefix(combined string) string {
	body := strings.TrimSpace(combined)
	if body == "" {
		return ""
	}
	return "Project rules (follow these exactly):\n---\n" + body + "\n---\n\n"
}

// Compile-time guard for an unused error alias we'd want if we
// add structured validation later.
var _ = errors.New
