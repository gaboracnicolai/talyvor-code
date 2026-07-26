package lens_test

import (
	"strings"
	"testing"

	"github.com/talyvor/code/internal/docs"
	"github.com/talyvor/code/internal/lens"
	"github.com/talyvor/code/internal/track"
)

// AUDIT FINDING (the shape). The https guard was opt-in at the call site: eleven
// subcommands called Config.Validate and two did not, so `serve` and `init` shipped the
// customer's API key in cleartext. Patching those two would leave the next new subcommand
// free to forget in exactly the same way.
//
// These tests pin the STRUCTURAL fix: a client that would leak the key CANNOT BE
// CONSTRUCTED. No caller can opt out, because there is no exported constructor that skips
// validation. They cover all three key-bearing clients, since lens/track/docs each attach
// an Authorization header and each had the identical unchecked constructor.

// hostile is a non-localhost host over cleartext http — exactly the canary's address in
// the audit, and the shape that leaked.
const hostile = "http://127.0.0.1.nip.io:39111"

func TestNew_RefusesCleartextRemoteHost(t *testing.T) {
	t.Run("lens", func(t *testing.T) {
		if _, err := lens.New(hostile, "tlv_secret"); err == nil {
			t.Fatal("lens.New built a client for a cleartext remote host — the key would be sent in the clear")
		}
	})
	t.Run("track", func(t *testing.T) {
		if _, err := track.New(hostile, "tlv_secret"); err == nil {
			t.Fatal("track.New built a client for a cleartext remote host")
		}
	})
	t.Run("docs", func(t *testing.T) {
		if _, err := docs.New(hostile, "tlv_secret"); err == nil {
			t.Fatal("docs.New built a client for a cleartext remote host")
		}
	})
}

// The error must explain itself: an operator who sees it should reach for https, not for a
// different URL.
func TestNew_ErrorNamesTheReason(t *testing.T) {
	_, err := lens.New(hostile, "k")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"https", "cleartext"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q; got: %v", want, err)
		}
	}
}

// Cloud-metadata / link-local addresses are refused even over http-on-loopback-ish shapes:
// a hostile config pointing at 169.254.169.254 must not collect the key.
func TestNew_RefusesLinkLocalAndMetadata(t *testing.T) {
	for _, raw := range []string{
		"http://169.254.169.254",
		"https://169.254.169.254",
		"http://0.0.0.0:8080",
	} {
		if _, err := lens.New(raw, "k"); err == nil {
			t.Errorf("lens.New accepted %q — link-local/metadata hosts must be refused", raw)
		}
	}
}

// The legitimate cases must still build, or the guard would simply break the product:
// https anywhere, and http on explicit loopback for local dev.
func TestNew_AcceptsHTTPSAndLoopback(t *testing.T) {
	for _, raw := range []string{
		"https://lens.talyvor.com",
		"http://localhost:8080",
		"http://127.0.0.1:8080",
		"http://[::1]:8080",
	} {
		if _, err := lens.New(raw, "k"); err != nil {
			t.Errorf("lens.New(%q) = %v, want ok — this is a legitimate configuration", raw, err)
		}
	}
}

// An EMPTY URL stays legal: Track and Docs are optional integrations whose clients report
// IsConfigured()==false. Without this the guard would turn "Docs not configured" into a
// hard failure of every command.
func TestNew_EmptyURLIsNotAnError(t *testing.T) {
	c, err := docs.New("", "")
	if err != nil {
		t.Fatalf("docs.New(\"\") = %v, want ok (optional integration)", err)
	}
	if c.IsConfigured() {
		t.Error("a client with no URL reports IsConfigured() == true")
	}
}
