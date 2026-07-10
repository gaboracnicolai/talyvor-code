package config

import "testing"

// B-Code-creds (a): the Talyvor base URLs were only checked non-empty — so a hostile config could point
// the client (with the user's API key attached) at an attacker host (cleartext exfil) or an internal
// address. RED: unsafe base URLs are accepted. GREEN: http-to-remote + link-local/metadata are rejected;
// https public + http-localhost-dev are allowed.
func TestValidate_RejectsUnsafeBaseURL(t *testing.T) {
	base := Config{LensAPIKey: "k", WorkspaceID: "ws"}

	bad := []string{
		"http://evil.example.com", // cleartext http to a remote host → key sent in the clear
		"https://169.254.169.254", // cloud metadata (link-local) — never a Talyvor endpoint
		"http://169.254.169.254/latest/meta-data",
	}
	for _, u := range bad {
		c := base
		c.LensURL = u
		if err := c.Validate(); err == nil {
			t.Errorf("Validate accepted unsafe base URL %q (key would be exfiltrated/leaked)", u)
		}
	}

	good := []string{
		"https://api.talyvor.com", // https public
		"http://localhost:8080",   // explicit localhost dev over http (documented exception)
		"https://127.0.0.1:8443",  // https localhost
	}
	for _, u := range good {
		c := base
		c.LensURL = u
		if err := c.Validate(); err != nil {
			t.Errorf("Validate rejected a legitimate base URL %q: %v", u, err)
		}
	}
}
