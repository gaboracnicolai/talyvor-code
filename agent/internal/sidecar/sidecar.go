// Package sidecar attributes model spend from tools Talyvor did not write.
//
// ⚠ WHY THIS EXISTS. Talyvor Code attributes its own calls, but a developer running plain Claude
// Code or Cursor against Lens attributes NOTHING — every request lands as unattributed spend, and
// the per-issue cost that is the whole point of the product is silently empty for exactly the
// people using the most expensive tools. Detection has to happen where the work happens, on the
// developer's machine, next to the git checkout. There is no server-side fix: by the time a
// request reaches Lens the branch it came from is gone. So this is our job and nobody else's.
//
// ⚠ WHAT IT IS. `talyvor-code exec -- claude` starts a loopback proxy, points the child at it with
// every redirect name it honours, adds the issue identifier and the Lens credential to each
// request, and forwards to Lens. The child needs no configuration and no key.
//
// ⚠ IT HANDS OUT TWO DOORS AND ROUTES BY WHICH ONE WAS USED. The root is Anthropic's; openAIDoor
// is OpenAI's. Nothing here sniffs a body or infers a protocol from a path — `/v1/models` is a
// route both protocols define, and a guess there has no tie-breaker.
//
// ⚠ IT NEVER READS THE PROMPT. The request body is streamed from the child to Lens without being
// parsed, buffered, logged or inspected — see forward(). This is the most sensitive thing in the
// repository and sidecar_test.go pins it with a sentinel that must never appear in the log.
//
// ⚠ IT NEVER SENDS THE BRANCH NAME. Lens reads X-Talyvor-Branch and stores it, and branch names
// carry customer names and unreleased codenames (see internal/issueref). Only the identifier
// travels, which is the same rule Talyvor Code's own client follows.
//
// # What this cannot reach
//
// Stated plainly, because a tool that quietly covers less than it appears to is worse than one
// with a documented edge.
//
//   - CURSOR IS NOT SUPPORTED, AND THE OPENAI DOOR DOES NOT CHANGE THAT. What OPENAI_BASE_URL does
//     for aider is now measured; what CURSOR honours is still unestablished, because it is not
//     installed on the machine this was built on and is closed source. The obstacle below is the
//     structural one and it stands whatever it honours: the redirect travels through the child's
//     environment, so a GUI application not launched from this shell is out of reach.
//   - CODEX IS NOT SUPPORTED. It is installed here but its platform binary is missing, so it could
//     not be run at all. An OpenAI-shaped client HAS now been put through this sidecar (aider on
//     /v1/proxy/openai/*), so the mapping it would need exists — what is missing is a codex that
//     runs, not a route.
//   - ANY TOOL WITH A HARDCODED ENDPOINT is out of reach by construction, as is any GUI application
//     not launched from the shell this command runs in — the redirect travels through the child's
//     environment and nowhere else.
//   - AIDER IS METERED AND WAS NOT. aider 0.86.2 honours both redirect names, so the ROUTING half
//     has worked since the previous merge — but ChildEnv removed ANTHROPIC_API_KEY, and litellm
//     raises "Missing Anthropic API Key" LOCALLY, before opening a socket. Measured end to end
//     through the real binary against a fake Lens: `exec -- aider` put ZERO requests on Lens, exited
//     0, and printed a banner saying the spend was attributed.
//     ⚠ IT IS FIXED BY GIVING THE CHILD THE NAME AND NOT A CREDENTIAL — see ChildEnv. The two
//     clients were believed to place opposite requirements on this variable; measured, they do not.
//     Same instrument, same prompt, one billable POST /v1/messages now reaches Lens carrying the
//     Lens bearer token and X-Talyvor-Feature: code-exec, and Claude Code's rows are byte-for-byte
//     what they were before the change.
//     ⚠ AND ITS OPENAI PATH IS NOW METERED TOO, WHICH IT WAS NOT — the same defect one provider
//     over, and the one this package had written down as "not claimed" rather than measured.
//     `exec -- aider --model gpt-4o` reached api.openai.com ON THE DEVELOPER'S OWN KEY, put ZERO
//     requests on Lens, exited 0 and printed the banner. Counted at loopback recorders with the
//     real binary: 1 request at whatever destination the developer's environment named, 0 at Lens;
//     with no redirect set at all the reply was `Incorrect API key provided: sk-not-a***…robe`,
//     byte-identical to what curl gets from api.openai.com for that key, so the request left the
//     machine. See OpenAIRedirectVars and openAIDoor; it now arrives at
//     /v1/proxy/openai/v1/chat/completions carrying the Lens bearer token, X-Talyvor-Feature and
//     the issue.
//     ⚠ WHAT IS STILL NOT CLAIMED: that a given Lens deployment has an OpenAI provider key
//     configured — without one the child gets an error, which is the loud direction. And every
//     OTHER provider Lens exposes (google, bedrock, mistral, groq, vllm) is still unmapped: no
//     client has been put through those doors, so no redirect is written for them.
//     ⚠ AND THE SEAM WAS RE-CHECKED RATHER THAN ASSUMED SAFE: forward() copies an ALLOWLIST, so the
//     child's x-api-key never reaches Lens — confirmed on the wire in the end-to-end run, where the
//     request that arrived carried none.
//   - CONNECTORS ARE UNVERIFIED BEYOND THE WARNING. Claude Code prints "claude.ai connectors are
//     disabled…" when a key is set and prints nothing when one is not, which is why no key is
//     planted. That the connectors then FUNCTION through the redirect was not tested.
//   - WORK OUTSIDE A BRANCH IS STILL UNATTRIBUTED. On main, on a detached HEAD, or outside a
//     repository, no identifier exists and none is invented; Lens records the spend with no issue.
//   - THE LEDGER STEP IS VERIFIED FROM SOURCE, NOT AT RUNTIME. That the header arrives at Lens was
//     proven end to end with the real client. That Lens then writes it to request_attribution.
//     issue_id was read in internal/attribution/context.go, not observed against a live deployment.
package sidecar

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Config is everything the proxy needs. It is deliberately small: anything not listed here is
// something the sidecar cannot send.
type Config struct {
	// LensURL is the Lens deployment to forward to, e.g. https://lens.talyvor.com.
	LensURL string
	// LensAPIKey authenticates US to Lens. It is never exposed to the child process.
	LensAPIKey string
	// Issue is the Track identifier, already resolved by internal/issueref. Empty means send no
	// header at all, which Lens records as unattributed.
	Issue string
	// Branch is accepted ONLY so the tool can tell the developer what it detected from. It is
	// never transmitted; the test that pins this passes a branch containing a customer name.
	Branch string
	// WorkspaceID is optional; Lens falls back to the key's own workspace when it is empty.
	WorkspaceID string
	// Port pins the listener. Zero — the normal case — lets the OS pick a free one, so two
	// checkouts open at once do not collide.
	Port int
	// Log receives operational messages ONLY: never a request body, never a credential.
	Log io.Writer
}

// Sidecar is a running loopback proxy.
type Sidecar struct {
	cfg      Config
	listener net.Listener
	server   *http.Server
	upstream string
}

// Start binds the loopback listener and begins serving.
//
// ⚠ 127.0.0.1 IS NOT A DEFAULT HERE, IT IS THE ONLY OPTION. This process holds a Lens API key; a
// listener reachable from a café or conference network would be handing out billable API access to
// whoever is on the same wifi. There is no flag to widen it.
func Start(cfg Config) (*Sidecar, error) {
	if cfg.Log == nil {
		cfg.Log = io.Discard
	}
	upstream := strings.TrimSuffix(cfg.LensURL, "/")
	if upstream == "" {
		return nil, errors.New("no Lens URL configured: set TALYVOR_LENS_URL")
	}

	addr := fmt.Sprintf("127.0.0.1:%d", cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// A pinned port that is taken is the one collision a developer can actually hit, and
		// "bind: address already in use" does not say what to do about it.
		if cfg.Port != 0 {
			return nil, fmt.Errorf("port %d is already in use, so the sidecar cannot start — "+
				"leave --port off to let the system choose a free one, or pass --port with a different number: %w",
				cfg.Port, err)
		}
		return nil, fmt.Errorf("cannot listen on loopback: %w", err)
	}

	s := &Sidecar{cfg: cfg, listener: ln, upstream: upstream}
	s.server = &http.Server{
		Handler: http.HandlerFunc(s.forward),
		// No read timeout: an interactive session holds a streaming response open for as long as
		// the model is talking, and cutting it off mid-answer would look like a model failure.
		ReadHeaderTimeout: 30 * time.Second,
	}
	go func() { _ = s.server.Serve(ln) }()
	return s, nil
}

// BaseURL is what the child process should be pointed at.
func (s *Sidecar) BaseURL() string {
	return "http://" + s.listener.Addr().String()
}

// Close stops accepting and releases the port. Callers must treat this as mandatory on every exit
// path — an orphaned proxy holding a Lens key is worse than no proxy at all.
//
// ⚠ THE LISTENER IS CLOSED DIRECTLY, NOT ONLY THROUGH THE SERVER, and that is the whole point of
// this function rather than a belt-and-braces extra.
//
// http.Server.Close() closes only the listeners it is TRACKING, and a listener becomes tracked
// inside Serve — which Start runs in a goroutine. So Start can return, and Close can run, before
// that goroutine has been scheduled at all; Close then finds nothing to close and returns happily
// while the socket is still bound and still completing handshakes into its backlog. Serve tidies up
// whenever it eventually runs, so the port does close — just not by the time Close said it had.
//
// Measured, not theorised: 6 runs in 8 on Linux dialled a "closed" port successfully. It never
// failed once in 10 runs on macOS, which is why it reached main. A Close that has not closed makes
// every promise in this package conditional on goroutine scheduling.
func (s *Sidecar) Close() error {
	// Closing the listener first is what releases the port deterministically; the server close then
	// tears down any connection already accepted.
	lerr := s.listener.Close()
	serr := s.server.Close()

	// ⚠ "ALREADY CLOSED" IS NOT A FAILURE OF Close, FROM EITHER OF THEM. Both of these close the
	// same socket by design, so whichever runs second normally reports it — and if Serve has already
	// tracked the listener, http.Server.Close() closes it too and returns exactly that. Treating it
	// as an error made this function report failure on the very path it was added to fix, which is
	// its own small lesson: the postcondition here is that the port is released, and a second close
	// is evidence of that, not evidence against it. A second Close by a caller is fine for the same
	// reason — Run defers one and tests make their own.
	for _, err := range []error{serr, lerr} {
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			return err
		}
	}
	return nil
}

// RedirectVars are every environment variable MEASURED to name the endpoint an Anthropic-shaped
// client dials. All of them are pointed at the sidecar, so no precedence order between them can
// send the child somewhere else.
//
// ⚠ ONE NAME IS NOT ENOUGH, AND THAT WAS A DEFECT RATHER THAN A REFINEMENT. This list held only
// ANTHROPIC_BASE_URL. Measured by counting requests that ARRIVE at a loopback recorder: aider
// 0.86.2 honours ANTHROPIC_API_BASE **in preference to** ANTHROPIC_BASE_URL — driven both ways
// round, so the answer is about the name and not about which value was written first. A developer
// with that variable in their shell therefore had the redirect silently overruled while the exec
// banner announced that the spend was going to Lens. Claude Code 2.1.226 ignores the name entirely,
// which is why nothing here noticed for two releases: the supported client is blind to it.
var RedirectVars = []string{
	"ANTHROPIC_BASE_URL",
	"ANTHROPIC_API_BASE",
}

// openAIDoor is the path prefix the OpenAI redirect names are pointed at, and the prefix forward()
// routes on. It is ONE constant because the two halves are one decision: hand out a door and route
// what arrives at it.
//
// ⚠ THE DOOR DECIDES THE PROVIDER, NOT THE SHAPE OF THE REQUEST. Sniffing would have to name a
// protocol from a path, and `/v1/models` is a route both protocols define — a guess with no
// tie-breaker. Which door the child was given is a fact, not an inference.
const openAIDoor = "/openai"

// OpenAIRedirectVars are every environment variable MEASURED to name the endpoint an OpenAI-shaped
// client dials. Like RedirectVars, all of them are set, so no precedence between them can send the
// child somewhere else.
//
// ⚠ THIS WAS THE LAST WAY `exec` COULD ANNOUNCE ATTRIBUTION AND DELIVER NONE. Counted at loopback
// recorders with the real binary and aider 0.86.2, one prompt per row: `exec -- aider --model
// gpt-4o` put ZERO requests on Lens and ONE on whatever destination the developer's own environment
// named, exited 0, and printed the banner. With no redirect set at all it reached api.openai.com
// itself and came back `Incorrect API key provided: sk-not-a***…robe` — byte-identical to what curl
// gets from api.openai.com for that key, so the request left the machine. The control row on the
// same instrument (an `anthropic/…` model) put one POST on Lens, which is what makes those zeros
// readable as "went elsewhere" rather than "never tried".
//
// The value carries openAIDoor plus `/v1`: measured, the prefix in a base URL survives verbatim
// into the request line (`.../openai/v1` ⇒ POST /openai/v1/chat/completions), and `/v1` is the
// client's own only when the base carries it — so this makes the forwarded suffix identical to what
// a client pointed straight at api.openai.com/v1 would write, which is the shape Lens already sees
// from talyvor-code's own semantic index.
var OpenAIRedirectVars = []string{
	"OPENAI_BASE_URL",
	"OPENAI_API_BASE",
}

// emptiedCredentialVars are set to an EMPTY value in the child's environment: the NAME is present,
// and there is no string behind it that could be mistaken for a credential.
//
// ⚠ THIS IS A MEASUREMENT, NOT A COMPROMISE BETWEEN TWO CLIENTS. Counting requests that ARRIVE at a
// loopback recorder, aider 0.86.2 and Claude Code 2.1.226:
//
//	unset      aider dials NOTHING (0 requests) · Claude Code connectors intact
//	""         aider dials (1 POST /v1/messages) · Claude Code connectors intact, sends no x-api-key
//	"sk-ant-…" aider dials · CLAUDE CODE PRINTS THE CONNECTOR WARNING
//
// aider's requirement is litellm's PRESENCE check — unset, it raises AuthenticationError locally
// before opening a socket. Claude Code's is for an auth SOURCE, and an empty string is not one. So
// one value serves both and no per-child matcher is needed.
//
// ⚠ OPENAI_API_KEY IS HERE ON ITS OWN MEASUREMENT, not by analogy with the row above. Same
// instrument, aider 0.86.2 straight at a recorder, `--model gpt-4o`:
//
//	unset      0 requests — litellm raises AuthenticationError locally
//	""         1 POST /chat/completions, and NO Authorization header at all
//	"sk-…"     1 POST, carrying the value
//
// The empty state is stronger here than in the Anthropic case: the client sends no credential
// header whatsoever, so nothing of the developer's reaches the seam even before forward()'s
// allowlist drops it.
var emptiedCredentialVars = []string{
	"ANTHROPIC_API_KEY",
	"OPENAI_API_KEY",
}

// droppedCredentialVars are removed outright rather than emptied.
//
// ⚠ MEASURED, so this is a decision and not an omission: with ANTHROPIC_AUTH_TOKEN set and
// ANTHROPIC_API_KEY absent, aider still dialled NOTHING. Planting this name satisfies no client,
// and it is a name Claude Code counts as an auth source.
var droppedCredentialVars = []string{
	"ANTHROPIC_AUTH_TOKEN",
}

// BypassVars name a provider this sidecar cannot carry. Lens exposes an Anthropic-NATIVE
// passthrough; Bedrock and Vertex are a different wire protocol with a different credential, so
// there is no URL that could be substituted here — the traffic simply leaves.
//
// ⚠ MEASURED, WITH POSITIVE PROOF OF THE DESTINATION rather than an absence: with
// CLAUDE_CODE_USE_BEDROCK=1 and the Bedrock endpoint pointed at a second recorder, Claude Code
// 2.1.226 sent ZERO requests to the sidecar and FIVE to that recorder, beginning with
// GET /inference-profiles?type=SYSTEM_DEFINED. The control run (neither variable set) put 3 on the
// sidecar and 0 on the alternate, so the instrument reads.
//
// ⚠ CLAUDE_CODE_USE_VERTEX is recorded on weaker evidence AND IS LISTED ANYWAY: zero requests
// reach the sidecar, but where they go instead was not established (that path wants GCP
// credentials this machine has none of). "The spend does not reach Lens" is the whole question
// here, and that half is measured.
var BypassVars = []string{
	"CLAUDE_CODE_USE_BEDROCK",
	"CLAUDE_CODE_USE_VERTEX",
}

// ChildEnv returns env with the child redirected at this sidecar.
//
// ⚠ IT GIVES THE CHILD A CREDENTIAL NAME AND NO CREDENTIAL, which is neither of the two obvious
// designs. Established empirically against Claude Code 2.1.221 and re-measured on 2.1.226: when
// ANTHROPIC_API_KEY holds a VALUE it prints "claude.ai connectors are disabled because
// ANTHROPIC_API_KEY or another auth source is set and takes precedence over your claude.ai login"
// and the user silently loses every connector. So a placeholder VALUE is out.
//
// ⚠ AND SIMPLY REMOVING THE NAME HAD A MEASURED COST THAT WENT UNPAID FOR TWO RELEASES: aider 0.86.2
// makes NO CALL AT ALL when the name is absent — litellm raises "Missing Anthropic API Key" LOCALLY,
// before opening a socket — so `exec -- aider` started, printed a banner claiming the spend was
// attributed, and spent nothing anywhere. Counted at a loopback recorder: 0 requests.
//
// The measured third state serves both: the name PRESENT and EMPTY. aider's check is for presence
// and passes; Claude Code's is for an auth SOURCE, and an empty string is not one — it printed no
// warning and sent no x-api-key, authenticating with its own claude.ai login exactly as before.
// See emptiedCredentialVars for the table and credential_test.go for what it does not establish.
//
// The Lens key is never placed in the child's environment: the child does not need it, and a
// subprocess environment is readable by anything else the child spawns. The empty value carries
// that rule further — there is no string in there to read.
func (s *Sidecar) ChildEnv(env []string) []string {
	replaced := func(kv string) bool {
		for _, name := range slices.Concat(RedirectVars, OpenAIRedirectVars, emptiedCredentialVars, droppedCredentialVars) {
			if strings.HasPrefix(kv, name+"=") {
				return true
			}
		}
		return false
	}
	out := make([]string, 0, len(env)+len(RedirectVars)+len(OpenAIRedirectVars)+len(emptiedCredentialVars))
	for _, kv := range env {
		if replaced(kv) {
			continue // re-added below, or deliberately dropped
		}
		out = append(out, kv)
	}
	// Every redirect is set, not just the one this tool used to know about. A name left at the
	// developer's stale value is a redirect we do not control.
	for _, name := range RedirectVars {
		out = append(out, name+"="+s.BaseURL())
	}
	// ⚠ THE OPENAI NAMES GET A DIFFERENT VALUE, NOT THE SAME ONE. The prefix is what tells forward()
	// which passthrough this child's traffic belongs to; pointing them at the root would send an
	// OpenAI body to Lens's Anthropic passthrough.
	for _, name := range OpenAIRedirectVars {
		out = append(out, name+"="+s.BaseURL()+openAIDoor+"/v1")
	}
	// ⚠ THE NAME, WITH NOTHING BEHIND IT. Anything non-empty here disables the developer's
	// connectors; anything absent stops aider dialling at all.
	for _, name := range emptiedCredentialVars {
		out = append(out, name+"=")
	}
	return out
}

// BypassReason reports why this environment would route the child past the sidecar, or "" when it
// would not. env is in the exec(3) "NAME=value" form.
//
// ⚠ THE PREDICATE IS NOT "SET TO ANYTHING NON-EMPTY", which is the obvious implementation and is
// wrong in the direction that costs a developer their day: it would refuse to run for someone who
// had explicitly written CLAUDE_CODE_USE_BEDROCK=0. Measured against Claude Code 2.1.226 by
// counting requests that reach the sidecar: unset, "", "0" and "false" all reached it (3 each);
// "1", "true" and "yes" all sent it zero.
func BypassReason(env []string) string {
	for _, name := range BypassVars {
		for _, kv := range env {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k != name || !engagesBypass(v) {
				continue
			}
			return fmt.Sprintf(
				"%s=%s sends this client to another provider entirely, so the sidecar would see no "+
					"request and the spend would be recorded nowhere — while this command told you it "+
					"was attributed. Measured: Claude Code makes ZERO calls to the proxy in that mode. "+
					"Unset %s to run through Lens, or drop `talyvor-code exec` and run the tool "+
					"directly, which is what is really happening today.",
				name, v, name)
		}
	}
	return ""
}

func engagesBypass(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false":
		return false
	}
	return true
}

// forward proxies one request to Lens.
//
// ⚠ THE BODY IS NEVER READ BY THIS PROCESS. r.Body is handed to the outbound request as an
// io.Reader and copied by the transport straight onto the wire. Nothing here parses it, buffers
// it, measures it or logs it — there is deliberately no place in this function where a prompt
// exists as a value that could be printed.
func (s *Sidecar) forward(w http.ResponseWriter, r *http.Request) {
	// ⚠ THE CLIENT'S CONNECTIVITY PROBE IS ANSWERED HERE, NOT FORWARDED. Claude Code opens with
	// HEAD /api/hello; forwarding it produced /v1/proxy/anthropic/api/hello, which is not a route
	// Lens has — so every session would have opened by sending Lens a request it can only reject.
	// Observed end to end before this was added. It answers for the SIDECAR's reachability, which
	// is what the probe is actually asking; a Lens outage surfaces on the first real request as a
	// 502 that says so, rather than being hidden behind a synthetic success here.
	if r.URL.Path == "/api/hello" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Lens exposes provider-native passthrough routes, so no translation is needed: a request goes
	// to its provider's passthrough exactly as the client wrote it.
	//
	// ⚠ WHICH PROVIDER IS DECIDED BY THE DOOR, NOT BY THE REQUEST. The root is Anthropic's — it is
	// what Claude Code has always been handed — and openAIDoor is the OpenAI one. Nothing here
	// inspects the body or guesses a protocol from a path.
	provider, path := "anthropic", r.URL.Path
	if rest, ok := strings.CutPrefix(r.URL.Path, openAIDoor+"/"); ok {
		provider, path = "openai", "/"+rest
	} else if r.URL.Path == openAIDoor {
		provider, path = "openai", "/"
	}
	target := s.upstream + "/v1/proxy/" + provider + path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		s.fail(w, "could not build the upstream request", err)
		return
	}

	// ⚠ AN ALLOWLIST, NOT A DENYLIST. Copying the client's headers and deleting the sensitive ones
	// would mean every future header Claude Code invents is forwarded by default — including
	// whatever it starts sending next. Only these carry through.
	for _, h := range []string{"Content-Type", "Accept", "Anthropic-Version", "Anthropic-Beta"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	// The child's own credential is REPLACED, never forwarded: Claude Code sends the developer's
	// personal claude.ai OAuth token when no key is set, and passing that to a Talyvor server would
	// be a credential leak our tool caused.
	req.Header.Set("Authorization", "Bearer "+s.cfg.LensAPIKey)
	req.Header.Set("X-Talyvor-Feature", "code-exec")
	if s.cfg.WorkspaceID != "" {
		req.Header.Set("X-Talyvor-Workspace", s.cfg.WorkspaceID)
	}
	// Absent, not empty, when nothing was detected: Lens records that as unattributed, which is
	// honest. A guessed identifier would produce a wrong bill.
	if s.cfg.Issue != "" {
		req.Header.Set("X-Talyvor-Issue", s.cfg.Issue)
	}

	// No client timeout, for the same reason the server has no write timeout: a long answer is
	// normal and must not be severed. The request context already ends when the child disconnects.
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		s.fail(w, "could not reach Lens", err)
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// ⚠ STREAMED, NOT BUFFERED. io.Copy with a flush after each read is what makes a token appear
	// as the model produces it. Buffering would return byte-identical output and still make the
	// tool feel broken, which is why the test asserts a chunk ARRIVES before the next is SENT.
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 8*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return // the child hung up
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil {
			return
		}
	}
}

// fail reports a transport problem to the child in the shape it expects, and to the developer in
// words. ⚠ The error is never given the request body, only the failure.
func (s *Sidecar) fail(w http.ResponseWriter, what string, err error) {
	fmt.Fprintf(s.cfg.Log, "talyvor exec: %s: %v\n", what, err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	fmt.Fprintf(w, `{"type":"error","error":{"type":"api_error","message":%q}}`,
		"talyvor exec could not reach Lens: "+what)
}
