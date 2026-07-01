// Package model is the single source of truth for which AI
// models Talyvor Code supports and which one to pick for each
// command. Lives in its own package so both the CLI and any
// future surface (extension via the MCP server, scripts in CI)
// agree on the catalogue without duplicating literals.
package model

import (
	"fmt"
	"strings"
)

// ModelProfile is the metadata we surface in `talyvor-code
// models` and the IDE QuickPick. The fields read like marketing
// copy on purpose — the QuickPick is the first thing a power
// user sees when wiring the right LLM to the right task.
type ModelProfile struct {
	ID          string
	DisplayName string
	Provider    string
	SpeedTier   string   // fast / balanced / powerful
	CostTier    string   // cheap / medium / expensive
	BestFor     []string // completions / chat / agent / tests / …
}

// KnownModels is the catalogue. New entries land here only —
// don't sprinkle literals through the codebase.
var KnownModels = []ModelProfile{
	{
		ID:          "claude-haiku-4-6",
		DisplayName: "Claude Haiku",
		Provider:    "Anthropic",
		SpeedTier:   "fast",
		CostTier:    "cheap",
		BestFor:     []string{"completions", "shell", "commit", "ask"},
	},
	{
		ID:          "claude-sonnet-4-6",
		DisplayName: "Claude Sonnet",
		Provider:    "Anthropic",
		SpeedTier:   "balanced",
		CostTier:    "medium",
		BestFor:     []string{"chat", "tests", "agent", "review"},
	},
	{
		ID:          "claude-opus-4-6",
		DisplayName: "Claude Opus",
		Provider:    "Anthropic",
		SpeedTier:   "powerful",
		CostTier:    "expensive",
		BestFor:     []string{"complex-agent", "architecture"},
	},
	{
		ID:          "gpt-4o",
		DisplayName: "GPT-4o",
		Provider:    "OpenAI",
		SpeedTier:   "balanced",
		CostTier:    "medium",
		BestFor:     []string{"chat", "tests"},
	},
	{
		ID:          "gpt-4o-mini",
		DisplayName: "GPT-4o Mini",
		Provider:    "OpenAI",
		SpeedTier:   "fast",
		CostTier:    "cheap",
		BestFor:     []string{"completions", "shell"},
	},
	{
		ID:          "mistral-large",
		DisplayName: "Mistral Large",
		Provider:    "Mistral",
		SpeedTier:   "balanced",
		CostTier:    "medium",
		BestFor:     []string{"chat", "agent"},
	},
}

// GetModel resolves an ID to its profile. Returns a friendly
// error listing every valid ID so the user can fix a typo
// without grepping the docs.
func GetModel(id string) (*ModelProfile, error) {
	id = strings.TrimSpace(id)
	for i := range KnownModels {
		if KnownModels[i].ID == id {
			return &KnownModels[i], nil
		}
	}
	ids := make([]string, 0, len(KnownModels))
	for _, m := range KnownModels {
		ids = append(ids, m.ID)
	}
	return nil, fmt.Errorf("unknown model %q. Valid models: %s", id, strings.Join(ids, ", "))
}

// Validate is a convenience wrapper for callers that only care
// about the yes/no answer.
func Validate(id string) error {
	_, err := GetModel(id)
	return err
}

// ListModels returns every known profile. Callers must treat
// the slice as read-only.
func ListModels() []ModelProfile {
	return KnownModels
}

// DefaultForCommand returns the recommended default model for
// a given subcommand. The map favours Haiku for short / latency-
// sensitive paths and Sonnet for paths where quality matters
// (chat, tests, agent, review).
func DefaultForCommand(command string) string {
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "completion", "completions":
		return "claude-haiku-4-6"
	case "shell", "shell-explain", "shell-fix":
		return "claude-haiku-4-6"
	case "commit":
		return "claude-haiku-4-6"
	case "ask":
		return "claude-haiku-4-6"
	case "chat":
		return "claude-sonnet-4-6"
	case "test", "tests", "test-gen", "test-generation":
		return "claude-sonnet-4-6"
	case "review", "code-review":
		return "claude-sonnet-4-6"
	case "run", "agent", "agent-plan", "agent-execute":
		return "claude-sonnet-4-6"
	}
	// Conservative fallback: Haiku is cheap; if we don't know
	// the command we shouldn't surprise the user with a
	// premium-tier bill.
	return "claude-haiku-4-6"
}

// ResolveModel applies the documented priority:
//
//  1. --model flag value (if non-empty)
//  2. TALYVOR_MODEL env / config value (if non-empty)
//  3. DefaultForCommand(command)
//
// All inputs are whitespace-trimmed. No validation here — let
// the caller surface a useful error after resolution.
func ResolveModel(flagValue, envValue, command string) string {
	if f := strings.TrimSpace(flagValue); f != "" {
		return f
	}
	if e := strings.TrimSpace(envValue); e != "" {
		return e
	}
	return DefaultForCommand(command)
}
