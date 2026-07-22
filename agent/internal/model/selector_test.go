package model

import (
	"strings"
	"testing"
)

func TestGetModel_KnownReturnsProfile(t *testing.T) {
	for _, id := range []string{
		"claude-haiku-4-5",
		"claude-sonnet-4-6",
		"claude-opus-4-6",
		"gpt-4o",
		"gpt-4o-mini",
		"mistral-large",
	} {
		p, err := GetModel(id)
		if err != nil {
			t.Errorf("GetModel(%s): %v", id, err)
			continue
		}
		if p.ID != id {
			t.Errorf("ID = %q, want %q", p.ID, id)
		}
	}
}

func TestGetModel_UnknownReturnsErrorWithList(t *testing.T) {
	_, err := GetModel("not-a-model")
	if err == nil {
		t.Fatal("expected error")
	}
	// Error should list a few valid models so the user can fix the typo.
	if !strings.Contains(err.Error(), "not-a-model") {
		t.Errorf("error should echo the bad id: %v", err)
	}
	if !strings.Contains(err.Error(), "claude-haiku-4-5") {
		t.Errorf("error should list valid models: %v", err)
	}
}

func TestDefaultForCommand(t *testing.T) {
	cases := map[string]string{
		"completion":  "claude-haiku-4-5",
		"completions": "claude-haiku-4-5",
		"shell":       "claude-haiku-4-5",
		"commit":      "claude-haiku-4-5",
		"ask":         "claude-haiku-4-5",
		"chat":        "claude-sonnet-4-6",
		"test":        "claude-sonnet-4-6",
		"tests":       "claude-sonnet-4-6",
		"review":      "claude-sonnet-4-6",
		"run":         "claude-sonnet-4-6",
		"agent":       "claude-sonnet-4-6",
		"unknown":     "claude-haiku-4-5", // safe default
	}
	for cmd, want := range cases {
		if got := DefaultForCommand(cmd); got != want {
			t.Errorf("DefaultForCommand(%s) = %s, want %s", cmd, got, want)
		}
	}
}

func TestListModels_ReturnsAllKnown(t *testing.T) {
	list := ListModels()
	if len(list) < 6 {
		t.Fatalf("expected ≥6 models, got %d", len(list))
	}
	// Ensure each ID appears once.
	seen := map[string]bool{}
	for _, m := range list {
		if seen[m.ID] {
			t.Errorf("duplicate model in list: %s", m.ID)
		}
		seen[m.ID] = true
	}
}

func TestResolveModel_FlagWinsOverEnvAndDefault(t *testing.T) {
	got := ResolveModel("gpt-4o", "claude-sonnet-4-6", "chat")
	if got != "gpt-4o" {
		t.Fatalf("flag should win: got %s", got)
	}
}

func TestResolveModel_EnvWinsOverDefault(t *testing.T) {
	got := ResolveModel("", "gpt-4o-mini", "completions")
	if got != "gpt-4o-mini" {
		t.Fatalf("env should win over default: got %s", got)
	}
}

func TestResolveModel_DefaultUsedWhenNothingElse(t *testing.T) {
	got := ResolveModel("", "", "tests")
	if got != "claude-sonnet-4-6" {
		t.Fatalf("default for tests: got %s", got)
	}
}

func TestResolveModel_TrimsWhitespace(t *testing.T) {
	got := ResolveModel("  gpt-4o  ", "", "chat")
	if got != "gpt-4o" {
		t.Fatalf("whitespace should be trimmed: got %q", got)
	}
}

func TestValidate_ReturnsErrorOnUnknown(t *testing.T) {
	if err := Validate("gpt-4o"); err != nil {
		t.Fatalf("known should not error: %v", err)
	}
	if err := Validate("nope"); err == nil {
		t.Fatal("unknown should error")
	}
}

func TestProvider_IsCorrect(t *testing.T) {
	cases := map[string]string{
		"claude-haiku-4-5":  "Anthropic",
		"claude-sonnet-4-6": "Anthropic",
		"claude-opus-4-6":   "Anthropic",
		"gpt-4o":            "OpenAI",
		"gpt-4o-mini":       "OpenAI",
		"mistral-large":     "Mistral",
	}
	for id, want := range cases {
		p, err := GetModel(id)
		if err != nil {
			t.Fatalf("GetModel(%s): %v", id, err)
		}
		if p.Provider != want {
			t.Errorf("%s provider = %s, want %s", id, p.Provider, want)
		}
	}
}
