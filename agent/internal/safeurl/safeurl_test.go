package safeurl_test

import (
	"testing"

	"github.com/talyvor/code/internal/safeurl"
)

// This package is now the SINGLE definition of "a base URL it is safe to attach an API key
// to" — internal/config and all three client constructors delegate here. A rule with one
// definition and no tests is one edit away from being wrong everywhere at once, so the
// table below is the authority for the rule itself; construct_guard_test.go proves the
// constructors actually apply it.
func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
		why     string
	}{
		// Allowed.
		{"empty is optional-integration, not an error", "", false, "Track/Docs are optional; empty yields an unconfigured client"},
		{"https anywhere", "https://lens.talyvor.com", false, ""},
		{"https with port and path", "https://lens.example.com:8443/base", false, ""},
		{"http on localhost", "http://localhost:8080", false, "local dev"},
		{"http on 127.0.0.1", "http://127.0.0.1:8080", false, "local dev"},
		{"http on ::1", "http://[::1]:8080", false, "local dev"},

		// Refused — the leak.
		{"http on a remote host", "http://lens.talyvor.com", true, "the key would cross the network in the clear"},
		{"http on a host that RESOLVES to loopback", "http://127.0.0.1.nip.io:39111", true,
			"the audit's canary address: resolving to loopback is not the same as being loopback, and DNS is attacker-influenced"},
		{"http on a bare LAN IP", "http://192.168.1.10:8080", true, "still cleartext across a network"},

		// Refused — metadata / link-local, over either scheme.
		{"link-local over http", "http://169.254.169.254", true, "cloud metadata service"},
		{"link-local over https", "https://169.254.169.254", true, "https does not make the metadata endpoint a legitimate target"},
		{"unspecified address", "http://0.0.0.0:8080", true, ""},

		// Refused — malformed.
		{"no scheme", "lens.talyvor.com", true, ""},
		{"no host", "https://", true, ""},
		{"non-http scheme", "file:///etc/passwd", true, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := safeurl.Validate("lens-url", tc.raw)
			if tc.wantErr && err == nil {
				t.Fatalf("Validate(%q) = nil, want an error — %s", tc.raw, tc.why)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate(%q) = %v, want nil — %s", tc.raw, err, tc.why)
			}
		})
	}
}

// The error carries the caller-supplied name so an operator sees WHICH setting is wrong;
// three clients share this rule and the message is the only thing distinguishing them.
func TestValidate_ErrorNamesTheSetting(t *testing.T) {
	for _, name := range []string{"lens-url", "track-url", "docs-url"} {
		err := safeurl.Validate(name, "http://remote.example.com")
		if err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		if got := err.Error(); len(got) == 0 || got[:len(name)] != name {
			t.Errorf("error should start with %q; got %q", name, got)
		}
	}
}
