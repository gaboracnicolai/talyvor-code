// Package config loads CLI settings from flags + env vars. Flag
// values win over env values; both fall back to sensible defaults.
package config

import (
	"errors"
	"os"
)

type Config struct {
	LensURL     string
	LensAPIKey  string
	TrackURL    string
	TrackAPIKey string
	DocsURL     string
	DocsAPIKey  string
	WorkspaceID string
	ActiveIssue string
	Model       string
}

// Load merges flag inputs with TALYVOR_* env vars. Empty flag
// values defer to the env var; if both are empty the default is
// used. The CLI's cobra bindings wire flag values in directly —
// this helper bridges them with the env fallback in one place.
func Load(flags Config) Config {
	out := flags
	if out.LensURL == "" {
		out.LensURL = os.Getenv("TALYVOR_LENS_URL")
	}
	if out.LensAPIKey == "" {
		out.LensAPIKey = os.Getenv("TALYVOR_LENS_API_KEY")
	}
	if out.TrackURL == "" {
		out.TrackURL = os.Getenv("TALYVOR_TRACK_URL")
	}
	if out.TrackAPIKey == "" {
		out.TrackAPIKey = os.Getenv("TALYVOR_TRACK_API_KEY")
	}
	if out.DocsURL == "" {
		out.DocsURL = os.Getenv("TALYVOR_DOCS_URL")
	}
	if out.DocsAPIKey == "" {
		out.DocsAPIKey = os.Getenv("TALYVOR_DOCS_API_KEY")
	}
	if out.WorkspaceID == "" {
		out.WorkspaceID = os.Getenv("TALYVOR_WORKSPACE_ID")
	}
	if out.ActiveIssue == "" {
		out.ActiveIssue = os.Getenv("TALYVOR_ISSUE")
	}
	if out.Model == "" {
		out.Model = "claude-haiku-4-6"
	}
	return out
}

// Validate returns a single error describing every missing
// required field, joined with a newline. nil when nothing is
// missing.
func (c Config) Validate() error {
	var missing []string
	if c.LensURL == "" {
		missing = append(missing, "--lens-url or TALYVOR_LENS_URL")
	}
	if c.LensAPIKey == "" {
		missing = append(missing, "--lens-key or TALYVOR_LENS_API_KEY")
	}
	if c.WorkspaceID == "" {
		missing = append(missing, "--workspace or TALYVOR_WORKSPACE_ID")
	}
	if len(missing) == 0 {
		return nil
	}
	msg := "missing required configuration:"
	for _, m := range missing {
		msg += "\n  - " + m
	}
	return errors.New(msg)
}
