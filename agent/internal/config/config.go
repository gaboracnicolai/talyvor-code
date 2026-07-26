// Package config loads CLI settings from flags + env vars. Flag
// values win over env values; both fall back to sensible defaults.
package config

import (
	"errors"
	"os"

	"github.com/talyvor/code/internal/safeurl"
)

// validateBaseURL delegates to internal/safeurl, which is now the single definition of
// the rule. It used to be implemented here and reachable ONLY through Config.Validate() —
// which made the guard opt-in at the call site, and two subcommands opted out. The
// client constructors apply the same rule at construction, so this boot check is the
// friendly early failure with a per-flag name, not the guard.
func validateBaseURL(name, raw string) error { return safeurl.Validate(name, raw) }

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
	// ReportVerdicts gates the K4 code loop: when true the agent reports mechanical build/test verdicts
	// back to Lens for the specific generation that produced the code. DEFAULT FALSE — off = the agent
	// behaves exactly as before; reporting is best-effort and NEVER blocks or fails a user's build.
	ReportVerdicts bool
	// ReportAttribution gates PR attribution: when true, after `run --iterative --pr` opens a PR the
	// agent attributes each SURVIVING generation's output_id to that PR (Lens owns the ownership gate).
	// DEFAULT FALSE — off = zero attribution calls, byte-identical; best-effort and NEVER fails the PR.
	ReportAttribution bool
	// CommitArtifact gates the H5 buildable-artifact commitment: when true, after --pr opens a PR the
	// agent commits, for each SURVIVING generation whose on-disk file still byte-equals its canonical
	// content, the module manifest to Lens (POST /v1/outputs/{id}/artifact — Lens owns ownership +
	// append-once and folds the captured content hash). DEFAULT FALSE — off = zero commit calls,
	// byte-identical; best-effort and NEVER fails the PR. Against-interest: an attested compile_failed
	// on a committed artifact can burn the workspace's own H5 bond.
	CommitArtifact bool
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
		out.Model = os.Getenv("TALYVOR_MODEL")
	}
	if !out.ReportVerdicts {
		out.ReportVerdicts = os.Getenv("TALYVOR_REPORT_VERDICTS") == "true"
	}
	if !out.ReportAttribution {
		out.ReportAttribution = os.Getenv("TALYVOR_REPORT_ATTRIBUTION") == "true"
	}
	if !out.CommitArtifact {
		out.CommitArtifact = os.Getenv("TALYVOR_COMMIT_ARTIFACT") == "true"
	}
	// Note: no hard default applied here. Each command resolves
	// its own default via internal/model.ResolveModel, which
	// honours --model first, then TALYVOR_MODEL, then the
	// per-command DefaultForCommand pick.
	return out
}

// Validate returns a single error describing every missing
// required field, joined with a newline. nil when nothing is
// missing.
func (c Config) Validate() error {
	// Reject unsafe base URLs before anything else — a hostile config must not exfiltrate the API key.
	for _, uc := range []struct{ name, val string }{
		{"lens-url", c.LensURL}, {"track-url", c.TrackURL}, {"docs-url", c.DocsURL},
	} {
		if uc.val != "" {
			if err := validateBaseURL(uc.name, uc.val); err != nil {
				return err
			}
		}
	}
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
