package sidecar

import (
	"strings"
	"testing"
)

// ⚠ THE CHILD NEEDS THE NAME, NOT A CREDENTIAL — AND THAT IS A MEASUREMENT, NOT A COMPROMISE.
//
// `exec -- aider` started, printed a banner claiming the spend was attributed, and made NO BILLABLE
// CALL ANYWHERE: ChildEnv removed ANTHROPIC_API_KEY, and aider refuses to dial without it. The
// obvious fix is a per-child PLACEHOLDER VALUE, which needs a matcher on the child's command name
// and a decision about which way the default falls, because a value was believed to break Claude
// Code's connectors.
//
// MEASURED INSTEAD, by counting requests that ARRIVE at a loopback recorder — aider 0.86.2 and
// Claude Code 2.1.226, on this machine, one recorder, one invocation per row:
//
//	ANTHROPIC_API_KEY | aider dials      | Claude Code connectors | x-api-key it sends
//	------------------+------------------+------------------------+-------------------
//	unset             | NO — 0 requests  | intact                 | (none)
//	"" present+empty  | YES — 1 request  | intact                 | (none)
//	"sk-ant-…"        | YES — 1 request  | DISABLED               | the value
//
// ⚠ SO THE TWO CLIENTS DO NOT PLACE OPPOSITE REQUIREMENTS ON THIS VARIABLE, which is what the fix
// was scoped around. There is a third state neither of them had been driven through: the name
// PRESENT and EMPTY. aider's check is litellm's PRESENCE check — with the name unset it raises
// "litellm.AuthenticationError: Missing Anthropic API Key - A call is being made to anthropic but
// no key is set" LOCALLY, before opening a socket, which is why the zero above means "never tried"
// rather than "not redirected". Claude Code's check is for an auth SOURCE, and an empty string is
// not one: it printed no warning and sent no x-api-key, authenticating with its own claude.ai login
// exactly as it does today.
//
// ⚠ THE EMPTY VALUE IS SAFER THAN A PLACEHOLDER, not merely equivalent. A subprocess environment is
// readable by anything the child spawns, so the rule this package already holds is that the Lens key
// never goes in it. An empty string carries that further: there is no string in the child's
// environment that could be mistaken for a credential, logged as one, or pasted into an issue.
//
// ⚠ WHAT THIS DOES NOT ESTABLISH, said rather than implied:
//   - Two clients at two versions on one machine. Another Anthropic-shaped client may test the value
//     rather than the name, and would still need a placeholder — which is why the rule below is
//     pinned as a VALUE rule, so widening it later is a deliberate edit and not a drift.
//   - That Claude Code's connectors FUNCTION through the redirect is still untested (the package doc
//     has always said so). What is measured is that the warning it prints when they are disabled is
//     ABSENT here and PRESENT with a non-empty value, on the same instrument.
//   - A child that reads this name and dials somewhere OTHER than the redirect now gets a 401 where
//     it used to get "no key set". Both fail; the words differ.
//
// ⚠ THE NAMES BELOW ARE HARDCODED. Reading them out of the package's own lists would compare each
// constant to itself and pass for every possible value, including an empty list.

// TestTheChildIsGivenTheNameAndNotACredential is the whole merge in one assertion.
func TestTheChildIsGivenTheNameAndNotACredential(t *testing.T) {
	up := newUpstream(t, nil)
	s, _ := start(t, Config{LensURL: up.URL, LensAPIKey: "lens-key"})

	env := s.ChildEnv([]string{"PATH=/usr/bin", "HOME=/home/dev"})

	var values []string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "ANTHROPIC_API_KEY="); ok {
			values = append(values, v)
		}
	}
	switch len(values) {
	case 0:
		t.Fatalf("ANTHROPIC_API_KEY is not in the child environment at all — aider raises " +
			"litellm.AuthenticationError locally and dials NOTHING, so `exec -- aider` prints a banner " +
			"claiming attribution and makes no billable call anywhere (measured: 0 requests arrive)")
	case 1:
	default:
		// Which duplicate the child reads is exec(2)'s business and differs between libc
		// implementations, so two values for one name is not a tidiness point.
		t.Fatalf("ANTHROPIC_API_KEY appears %d times in the child environment: %q", len(values), values)
	}
	if values[0] != "" {
		t.Errorf("ANTHROPIC_API_KEY=%q — a NON-EMPTY value is what Claude Code 2.1.226 treats as an "+
			"auth source, and it silently disables every claude.ai connector the developer has. "+
			"Measured: empty prints no warning and sends no x-api-key; %q prints the warning",
			values[0], values[0])
	}
}

// TestADevelopersOwnKeyIsReplacedRatherThanForwarded keeps the billing half of the old rule.
//
// ⚠ THE POINT IS NOT TIDINESS. If the developer's real key reached the child, their own Anthropic
// account would be billed for work this command announces as attributed to Lens — and their
// connectors would go with it.
func TestADevelopersOwnKeyIsReplacedRatherThanForwarded(t *testing.T) {
	up := newUpstream(t, nil)
	s, _ := start(t, Config{LensURL: up.URL, LensAPIKey: "lens-key"})

	env := s.ChildEnv([]string{
		"PATH=/usr/bin",
		"ANTHROPIC_API_KEY=sk-ant-the-developers-real-key",
		"ANTHROPIC_AUTH_TOKEN=the-developers-oauth-token",
		"HOME=/home/dev",
	})
	joined := strings.Join(env, "\n")

	for _, secret := range []string{"sk-ant-the-developers-real-key", "the-developers-oauth-token", "lens-key"} {
		if strings.Contains(joined, secret) {
			t.Errorf("%q survived into the child environment, which anything the child spawns can read:\n%s", secret, joined)
		}
	}
	if !strings.Contains(joined, "PATH=/usr/bin") || !strings.Contains(joined, "HOME=/home/dev") {
		t.Errorf("the rest of the environment was not preserved:\n%s", joined)
	}
}

// TestTheAuthTokenNameIsRemovedAndNotPlanted records a measured NEGATIVE, so nobody "completes" the
// fix by planting the other name too.
//
// ⚠ MEASURED: with ANTHROPIC_AUTH_TOKEN set to a value and ANTHROPIC_API_KEY absent, aider 0.86.2
// still dialled NOTHING — 0 requests, same local litellm error. So planting this name buys no
// client anything, and it is a credential name Claude Code counts as an auth source. It stays gone.
func TestTheAuthTokenNameIsRemovedAndNotPlanted(t *testing.T) {
	up := newUpstream(t, nil)
	s, _ := start(t, Config{LensURL: up.URL, LensAPIKey: "lens-key"})

	for _, kv := range s.ChildEnv([]string{"ANTHROPIC_AUTH_TOKEN=the-developers-oauth-token"}) {
		if strings.HasPrefix(kv, "ANTHROPIC_AUTH_TOKEN=") {
			t.Errorf("ANTHROPIC_AUTH_TOKEN is in the child environment as %q — measured to satisfy no "+
				"client, and Claude Code counts it as an auth source", kv)
		}
	}
}
