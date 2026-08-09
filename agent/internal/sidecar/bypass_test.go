package sidecar

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ⚠ THE REDIRECT TRAVELS THROUGH THE CHILD'S ENVIRONMENT, SO EVERY VARIABLE THAT STEERS THE CHILD
// IS PART OF THE SEAM — NOT ONLY THE ONE WE SET.
//
// ChildEnv shipped knowing three names (ANTHROPIC_BASE_URL, ANTHROPIC_API_KEY,
// ANTHROPIC_AUTH_TOKEN). MEASURED against the real clients, on this machine, by counting requests
// that ARRIVE at a loopback recorder rather than by reading anything:
//
//   - ANTHROPIC_API_BASE — aider 0.86.2 honours it AND PREFERS IT over ANTHROPIC_BASE_URL. Driven
//     both ways round so the answer is about the NAME and not about which value was written first:
//     API_BASE=A/BASE_URL=B put the POST /v1/messages at A; reversed, it went to B. Each name alone
//     hits its own recorder, so the harness reads. ChildEnv left this one in place, so a developer
//     who has it set in their shell had the sidecar's redirect silently overruled.
//   - CLAUDE_CODE_USE_BEDROCK — Claude Code 2.1.226 sends ZERO requests to the sidecar and FIVE to
//     the Bedrock endpoint, GET /inference-profiles?type=SYSTEM_DEFINED among them. That is
//     POSITIVE proof of where it went, not an absence: the control run put 3 hits on the sidecar
//     and 0 on the alternate recorder.
//   - CLAUDE_CODE_USE_VERTEX — zero requests reach the sidecar. ⚠ Where they go instead was NOT
//     established (the run needs GCP credentials this machine has none of and timed out), so this
//     is recorded as "leaves the sidecar", which is all the attribution question needs.
//
// MEASURED CLEAN, so nobody re-checks: ANTHROPIC_AUTH_TOKEN, ANTHROPIC_BEDROCK_BASE_URL,
// ANTHROPIC_VERTEX_BASE_URL and ANTHROPIC_DEFAULT_SONNET_MODEL each left the redirect intact on
// BOTH clients — the sweep is not a blanket zero.
//
// ⚠ THE NAMES BELOW ARE HARDCODED ON PURPOSE. Reading them out of the package's own list would
// compare the constant to itself and pass for every possible value, including an empty list.

// theseAreTheRedirectsMeasuredToWork is written by hand from the measurement above.
var theseAreTheRedirectsMeasuredToWork = []string{
	"ANTHROPIC_BASE_URL",
	"ANTHROPIC_API_BASE",
}

// theseBypassTheSidecarEntirely name a provider the sidecar cannot carry: Lens exposes an
// Anthropic-native passthrough, and Bedrock/Vertex are a different wire protocol with a different
// credential. Pointing them at the proxy is not possible, so the only honest answers are to refuse
// or to lie.
var theseBypassTheSidecarEntirely = []string{
	"CLAUDE_CODE_USE_BEDROCK",
	"CLAUDE_CODE_USE_VERTEX",
}

// TestEveryMeasuredRedirectPointsAtTheSidecar is the first half of the fix.
func TestEveryMeasuredRedirectPointsAtTheSidecar(t *testing.T) {
	up := newUpstream(t, nil)
	s, _ := start(t, Config{LensURL: up.URL, LensAPIKey: "lens-key"})

	// A developer's shell, with a stale endpoint under every name a client might read.
	developerEnv := []string{
		"PATH=/usr/bin",
		"HOME=/home/dev",
		"ANTHROPIC_BASE_URL=https://api.anthropic.com",
		"ANTHROPIC_API_BASE=https://some-other-gateway.internal",
	}
	got := map[string]string{}
	for _, kv := range s.ChildEnv(developerEnv) {
		if k, v, ok := strings.Cut(kv, "="); ok {
			if _, dup := got[k]; dup {
				// Two values for one name is not a detail: which one the child reads is
				// exec(2)'s business, and the answer differs between libc implementations.
				t.Errorf("%s appears TWICE in the child environment — the child picks one and we do not control which", k)
			}
			got[k] = v
		}
	}

	for _, name := range theseAreTheRedirectsMeasuredToWork {
		v, ok := got[name]
		if !ok {
			t.Errorf("%s is not set in the child environment: a client that reads this name dials wherever it likes", name)
			continue
		}
		if v != s.BaseURL() {
			t.Errorf("%s = %q, want the sidecar at %q — this redirect is not ours, so the child's spend is unattributed while the banner claims otherwise",
				name, v, s.BaseURL())
		}
	}

	// The rest of the environment is still the developer's.
	if got["PATH"] != "/usr/bin" || got["HOME"] != "/home/dev" {
		t.Errorf("the rest of the environment was not preserved: %v", got)
	}
}

// TestTheSidecarRefusesAnEnvironmentThatWouldBypassIt is the second half.
//
// ⚠ IT ASSERTS THE CHILD NEVER RAN, NOT MERELY THAT AN ERROR CAME BACK. A refusal that still
// starts the child is the failure this exists to prevent, and an error value alone cannot tell the
// two apart.
func TestTheSidecarRefusesAnEnvironmentThatWouldBypassIt(t *testing.T) {
	for _, name := range theseBypassTheSidecarEntirely {
		t.Run(name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "the-child-ran")
			t.Setenv(name, "1")

			code, err := Run(
				Config{LensURL: lensStub(t), LensAPIKey: "lens-key"},
				[]string{"/bin/sh", "-c", "touch " + marker},
				Stdio{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard},
				nil,
			)
			if err == nil {
				t.Fatalf("%s was set and exec started anyway (exit %d) — every request goes to another provider while the banner says the spend is attributed to Lens", name, code)
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("the refusal does not name %s, so the developer cannot act on it: %v", name, err)
			}
			if _, statErr := os.Stat(marker); statErr == nil {
				t.Errorf("%s: the child RAN despite the refusal", name)
			}
		})
	}
}

// TestTheBypassPredicateIsTheOneThatWasMeasured pins the exact values that engage the bypass.
//
// ⚠ THE OFF VALUES MATTER AS MUCH AS THE ON ONES. Refusing on CLAUDE_CODE_USE_BEDROCK=0 would
// break a developer who explicitly turned it off, and "any non-empty value" is the obvious
// implementation that does exactly that. MEASURED against Claude Code 2.1.226 by counting requests
// that arrive at the sidecar: unset, "", "0" and "false" all reached it (3 hits each); "1", "true"
// and "yes" all sent it ZERO.
func TestTheBypassPredicateIsTheOneThatWasMeasured(t *testing.T) {
	measured := []struct {
		value  string
		engage bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"1", true},
		{"true", true},
		{"yes", true},
	}
	for _, m := range measured {
		t.Run("CLAUDE_CODE_USE_BEDROCK="+m.value, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "the-child-ran")
			t.Setenv("CLAUDE_CODE_USE_BEDROCK", m.value)

			_, err := Run(
				Config{LensURL: lensStub(t), LensAPIKey: "lens-key"},
				[]string{"/bin/sh", "-c", "touch " + marker},
				Stdio{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard},
				nil,
			)
			_, ranErr := os.Stat(marker)
			childRan := ranErr == nil

			if m.engage {
				if err == nil || childRan {
					t.Errorf("value %q was measured to bypass the sidecar but exec proceeded (err=%v, childRan=%v)", m.value, err, childRan)
				}
				return
			}
			if err != nil {
				t.Errorf("value %q was measured NOT to bypass the sidecar, but exec refused: %v", m.value, err)
			}
			if !childRan {
				t.Errorf("value %q: the child did not run, so this case proves nothing", m.value)
			}
		})
	}
}
