// Package config loads CLI settings from flags + env vars. Flag
// values win over env values; both fall back to sensible defaults.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
)

// validateBaseURL rejects a Talyvor base URL that would leak the attached API key: it must be https
// (except an explicit localhost host over http, for local dev), and must never resolve to a link-local /
// cloud-metadata / unspecified address. A hostile config that pointed the client at http://attacker or
// http://169.254.169.254 would otherwise send the user's key there.
func validateBaseURL(name, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%s: invalid URL %q", name, raw)
	}
	host := u.Hostname()
	isLocal := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if u.Scheme != "https" && !isLocal {
		return fmt.Errorf("%s must be https (got %q) — the API key must not be sent in cleartext", name, raw)
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()) {
		return fmt.Errorf("%s: refusing link-local/metadata host %q", name, host)
	}
	return nil
}

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
