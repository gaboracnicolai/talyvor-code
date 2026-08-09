package sidecar

import (
	"net/http"
	"strings"
	"testing"
)

// ⚠ `exec -- aider --model gpt-4o` SPENT THE DEVELOPER'S OWN OPENAI MONEY AND TOLD THEM IT WAS
// ATTRIBUTED. This file is the OpenAI half of the same defect the Anthropic half was fixed for in
// the two previous merges, and it is the failure this command exists to prevent: not an error, a
// SUCCESS that bills the wrong account and records nothing.
//
// MEASURED, by counting requests that ARRIVE at loopback recorders — aider 0.86.2, the real
// `talyvor-code exec` binary built from this tree, one prompt per row, a fake Lens on one port and
// a decoy standing in for the destination the developer's own environment names on another:
//
//	row  model                 env under test              → Lens   → elsewhere   exit  banner
//	---  --------------------  --------------------------  ------  -----------  ----  -----------
//	A    anthropic/claude-…    (none)                           1            0     0  attributed
//	B    gpt-4o                OPENAI_BASE_URL=decoy            0            1     0  attributed
//	C    gpt-4o                OPENAI_API_BASE=decoy            0            1     0  attributed
//	D    gpt-4o                (no redirect at all)             0            0     0  attributed
//
// Row A is the control that makes every zero readable: the same binary, the same recorder, the
// shipped Anthropic path, one POST /v1/proxy/anthropic/v1/messages arriving.
//
// ⚠ ROW D IS POSITIVE PROOF OF THE DESTINATION, NOT AN ABSENCE. With no redirect variable set —
// the ordinary case — aider reached api.openai.com itself. It came back
// `Incorrect API key provided: sk-not-a***************robe`, which is BYTE-IDENTICAL to what
// curl gets from api.openai.com for that key, redaction and all. The request left the machine.
//
// ⚠ AND THE CHILD'S ENVIRONMENT SAYS WHY, in one look. `exec -- env` prints:
//
//	ANTHROPIC_BASE_URL=http://127.0.0.1:49713   ← ours
//	ANTHROPIC_API_BASE=http://127.0.0.1:49713   ← ours
//	ANTHROPIC_API_KEY=                          ← ours, the name with nothing behind it
//	OPENAI_BASE_URL=https://decoy.invalid       ← THE DEVELOPER'S, UNTOUCHED
//	OPENAI_API_BASE=https://decoy2.invalid      ← THE DEVELOPER'S, UNTOUCHED
//	OPENAI_API_KEY=<their real key>             ← THE DEVELOPER'S, UNTOUCHED
//
// One provider's environment is controlled completely and the other's is not touched at all.
//
// ⚠ THE THIRD STATE HOLDS ONE PROVIDER OVER — measured on the same instrument, aider straight at a
// recorder, so this is not carried over from the Anthropic table by analogy:
//
//	OPENAI_API_KEY | aider dials     | Authorization it sends
//	---------------+-----------------+-----------------------
//	unset          | NO — 0 requests | (never opened a socket)
//	"" present     | YES — 1 request | ABSENT — no header at all
//	"sk-…"         | YES — 1 request | the value
//
// So the empty name serves here too, and it is stronger than in the Anthropic case: with an empty
// value the OpenAI client sends no Authorization header whatsoever.
//
// ⚠ THE ROUTING IS BY THE DOOR WE HANDED OUT, NOT BY SNIFFING THE REQUEST. The sidecar gives the
// OpenAI names a base URL carrying a `/openai/v1` prefix and routes anything arriving under
// `/openai` to Lens's OpenAI passthrough; everything else keeps going to the Anthropic one. Sniffing
// would have to guess a protocol from a path, and `/v1/models` exists in both — a guess with no
// tie-breaker. The prefix is a fact about which door the child was given.
//
// MEASURED, because a prefix in a base URL is a thing clients are free to drop: aider writes
// `POST /openai/v1/chat/completions` when handed `http://127.0.0.1:PORT/openai/v1`, and
// `POST /openai/chat/completions` when handed `.../openai` — the prefix survives verbatim either
// way, and the `/v1` is the client's only when the base carries it. Handed a bare host it writes
// `POST /chat/completions`, so the forwarded path is the client's, not ours, in every case.
//
// ⚠ WHAT LENS DOES WITH IT IS READ FROM SOURCE, NOT OBSERVED AGAINST A DEPLOYMENT — the same
// standard, and the same limit, as the ledger step this package already documents.
// cmd/lens/main.go routes `POST /v1/proxy/openai/*` to HandleOpenAI under the SAME proxyScope as
// `/v1/proxy/anthropic/*`, and both handlers are one line calling the same p.serve(), so metering,
// budget gates and X-Talyvor-Issue attribution are the same code for both. What is NOT claimed:
// that a given Lens deployment has an OpenAI provider key configured. Without one the child gets an
// error — which is the loud direction, and the whole point of this merge is that today it gets
// silence.
//
// ⚠ THE NAMES BELOW ARE HARDCODED, for the reason credential_test.go states: a test that reads the
// package's own list compares each constant to itself and passes for every value, empty included.

// postTo runs one request through the sidecar at an arbitrary path. The existing post() helper is
// pinned to /v1/messages, which is exactly the assumption this file exists to break.
func postTo(t *testing.T, s *Sidecar, path, body string, header http.Header) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", s.BaseURL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s through the sidecar: %v", path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// childEnvValues returns every value the child would see for one name. Duplicates matter: which one
// exec(2) hands the child differs between libc implementations, so two is not a tidiness point.
func childEnvValues(env []string, name string) []string {
	var out []string
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			out = append(out, v)
		}
	}
	return out
}

// TestTheOpenAIDoorIsPointedAtTheSidecar is rows B and C: either name alone steers the child, so a
// name left at the developer's value is a redirect this tool does not control.
func TestTheOpenAIDoorIsPointedAtTheSidecar(t *testing.T) {
	up := newUpstream(t, nil)
	s, _ := start(t, Config{LensURL: up.URL, LensAPIKey: "lens-key"})

	env := s.ChildEnv([]string{
		"PATH=/usr/bin",
		"OPENAI_BASE_URL=https://decoy.invalid",
		"OPENAI_API_BASE=https://decoy2.invalid",
	})

	// The literal is written out rather than composed from the package's own constant.
	want := s.BaseURL() + "/openai/v1"
	for _, name := range []string{"OPENAI_BASE_URL", "OPENAI_API_BASE"} {
		got := childEnvValues(env, name)
		if len(got) != 1 {
			t.Fatalf("%s appears %d times in the child environment (%q); measured: aider 0.86.2 "+
				"honours BOTH names, so either one left at the developer's value sends the spend "+
				"somewhere this tool does not control while the banner says otherwise", name, len(got), got)
		}
		if got[0] != want {
			t.Errorf("%s = %q, want %q", name, got[0], want)
		}
	}
	if joined := strings.Join(env, "\n"); strings.Contains(joined, "decoy") {
		t.Errorf("a stale OpenAI endpoint survived into the child environment:\n%s", joined)
	}
}

// TestTheOpenAIChildIsGivenTheNameAndNotACredential is the third state, measured for this provider
// rather than assumed from the Anthropic one.
func TestTheOpenAIChildIsGivenTheNameAndNotACredential(t *testing.T) {
	up := newUpstream(t, nil)
	s, _ := start(t, Config{LensURL: up.URL, LensAPIKey: "lens-key"})

	env := s.ChildEnv([]string{"PATH=/usr/bin", "OPENAI_API_KEY=sk-the-developers-real-openai-key"})

	values := childEnvValues(env, "OPENAI_API_KEY")
	switch len(values) {
	case 0:
		t.Fatalf("OPENAI_API_KEY is not in the child environment at all — measured, aider raises " +
			"litellm.AuthenticationError locally and dials NOTHING, so the redirect would be handed " +
			"to a client that never opens a socket")
	case 1:
	default:
		t.Fatalf("OPENAI_API_KEY appears %d times in the child environment: %q", len(values), values)
	}
	if values[0] != "" {
		t.Errorf("OPENAI_API_KEY=%q — the name must carry NOTHING. A subprocess environment is "+
			"readable by anything the child spawns, and a non-empty value here would also bill the "+
			"account it belongs to for work this command announces as attributed to Lens", values[0])
	}
	if strings.Contains(strings.Join(env, "\n"), "sk-the-developers-real-openai-key") {
		t.Errorf("the developer's own OpenAI key survived into the child environment")
	}
}

// TestTheOpenAIDoorForwardsOntoLensOpenAIPassthrough is the merge in one assertion.
func TestTheOpenAIDoorForwardsOntoLensOpenAIPassthrough(t *testing.T) {
	up := newUpstream(t, nil)
	s, _ := start(t, Config{LensURL: up.URL, LensAPIKey: "k"})

	postTo(t, s, "/openai/v1/chat/completions", `{"model":"gpt-4o","messages":[]}`, nil)

	if up.gotPath != "/v1/proxy/openai/v1/chat/completions" {
		t.Errorf("upstream path = %q, want /v1/proxy/openai/v1/chat/completions — a request through "+
			"the OpenAI door reaching the ANTHROPIC passthrough is a 400 at best and a wrongly "+
			"priced row at worst", up.gotPath)
	}
}

// TestTheOpenAIDoorCarriesTheAttribution — being metered is not the same as being attributed, and
// the whole product claim is the second one.
func TestTheOpenAIDoorCarriesTheAttribution(t *testing.T) {
	up := newUpstream(t, nil)
	s, _ := start(t, Config{LensURL: up.URL, LensAPIKey: "lens-key", Issue: "ENG-42", WorkspaceID: "ws-7"})

	postTo(t, s, "/openai/v1/chat/completions", `{"messages":[]}`, nil)

	for _, want := range []struct{ header, value string }{
		{"Authorization", "Bearer lens-key"},
		{"X-Talyvor-Feature", "code-exec"},
		{"X-Talyvor-Issue", "ENG-42"},
		{"X-Talyvor-Workspace", "ws-7"},
	} {
		if got := up.gotHeader.Get(want.header); got != want.value {
			t.Errorf("%s reaching Lens = %q, want %q", want.header, got, want.value)
		}
	}
}

// TestTheOpenAIDoorNeverCarriesTheChildsOwnCredential re-checks the allowlist ON THE NEW DOOR.
//
// ⚠ IT IS NOT ENOUGH THAT THE ANTHROPIC DOOR IS CLEAN. A guard at one copy of a seam says nothing
// about a second copy, and an OpenAI client sends its credential under a DIFFERENT header name
// (Authorization, not x-api-key) — so a denylist tuned to the first door would pass this one
// straight through.
func TestTheOpenAIDoorNeverCarriesTheChildsOwnCredential(t *testing.T) {
	up := newUpstream(t, nil)
	s, logged := start(t, Config{LensURL: up.URL, LensAPIKey: "lens-key"})

	h := http.Header{}
	h.Set("Authorization", "Bearer sk-THE-USERS-OWN-OPENAI-KEY")
	h.Set("OpenAI-Organization", "org-the-users-employer")
	h.Set("OpenAI-Project", "proj-unreleased-codename")
	postTo(t, s, "/openai/v1/chat/completions", `{"messages":[]}`, h)

	if got := up.gotHeader.Get("Authorization"); got != "Bearer lens-key" {
		t.Errorf("Authorization reaching Lens = %q, want the Lens key", got)
	}
	for _, name := range []string{"OpenAI-Organization", "OpenAI-Project"} {
		if got := up.gotHeader.Get(name); got != "" {
			t.Errorf("%s reached Lens with %q — the allowlist must not have grown", name, got)
		}
	}
	if strings.Contains(logged.String(), "THE-USERS-OWN") || strings.Contains(logged.String(), "codename") {
		t.Errorf("something the child sent was written to the log:\n%s", logged.String())
	}
}

// TestTheDoorDecidesTheProviderNotThePath pins the routing RULE rather than one example of it.
//
// A request that LOOKS like OpenAI but arrives at the root door goes to the Anthropic passthrough,
// because the root is the door Claude Code was handed. Sniffing the path instead would have to
// decide what `/v1/models` is — a route both protocols define — with nothing to decide it on.
func TestTheDoorDecidesTheProviderNotThePath(t *testing.T) {
	up := newUpstream(t, nil)
	s, _ := start(t, Config{LensURL: up.URL, LensAPIKey: "k"})

	postTo(t, s, "/v1/chat/completions", `{"messages":[]}`, nil)
	if up.gotPath != "/v1/proxy/anthropic/v1/chat/completions" {
		t.Errorf("upstream path = %q — a request at the ROOT door must keep going to the Anthropic "+
			"passthrough whatever it looks like", up.gotPath)
	}

	postTo(t, s, "/openai/v1/messages", `{"messages":[]}`, nil)
	if up.gotPath != "/v1/proxy/openai/v1/messages" {
		t.Errorf("upstream path = %q — a request at the OPENAI door must keep going to the OpenAI "+
			"passthrough whatever it looks like", up.gotPath)
	}
}

// TestTheAnthropicDoorIsUnchanged is the floor this merge must not fall through: the supported
// client's behaviour is the risk half of any change to ChildEnv, and it is not left to argument.
func TestTheAnthropicDoorIsUnchanged(t *testing.T) {
	up := newUpstream(t, nil)
	s, _ := start(t, Config{LensURL: up.URL, LensAPIKey: "lens-key"})

	post(t, s, `{"messages":[]}`, nil)
	if up.gotPath != "/v1/proxy/anthropic/v1/messages" {
		t.Errorf("upstream path = %q, want /v1/proxy/anthropic/v1/messages", up.gotPath)
	}

	env := s.ChildEnv([]string{"PATH=/usr/bin"})
	for _, name := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_BASE"} {
		got := childEnvValues(env, name)
		if len(got) != 1 || got[0] != s.BaseURL() {
			t.Errorf("%s = %q, want exactly [%q] — the Anthropic door is the ROOT and must not "+
				"acquire a prefix", name, got, s.BaseURL())
		}
	}
	if got := childEnvValues(env, "ANTHROPIC_API_KEY"); len(got) != 1 || got[0] != "" {
		t.Errorf("ANTHROPIC_API_KEY = %q, want exactly one empty value", got)
	}
}
