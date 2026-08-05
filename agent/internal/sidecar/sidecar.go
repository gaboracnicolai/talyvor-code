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
// ANTHROPIC_BASE_URL, adds the issue identifier and the Lens credential to each request, and
// forwards to Lens. The child needs no configuration and no key.
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
//   - CURSOR IS NOT SUPPORTED. It is not installed on the machine this was built on, so what it
//     honours could not be established rather than guessed. OPENAI_BASE_URL is an assumption, not
//     a finding, and shipping an untested redirect would produce silent unattributed spend that
//     looks like success.
//   - CODEX IS NOT SUPPORTED. It is installed here but its platform binary is missing, so it could
//     not be run at all. Lens does expose /v1/proxy/openai/*, so an OpenAI-format client is
//     plausible — but this sidecar only maps onto the Anthropic passthrough, and no OpenAI-shaped
//     client has been put through it.
//   - ANY TOOL WITH A HARDCODED ENDPOINT is out of reach by construction, as is any GUI application
//     not launched from the shell this command runs in — the redirect travels through the child's
//     environment and nowhere else.
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

// ChildEnv returns env with the child redirected at this sidecar.
//
// ⚠ IT REMOVES CREDENTIALS RATHER THAN ADDING ONE, and that is the opposite of the obvious design.
// Established empirically against Claude Code 2.1.221: when ANTHROPIC_API_KEY is set it prints
// "claude.ai connectors are disabled because ANTHROPIC_API_KEY or another auth source is set and
// takes precedence over your claude.ai login" and the user silently loses every connector. When it
// is unset, the connectors keep working and Claude Code sends its own claude.ai token, which the
// sidecar replaces with the Lens key on the way out. So the child needs no key, and planting one
// would break something the developer did not ask us to touch.
//
// The Lens key is never placed in the child's environment: the child does not need it, and a
// subprocess environment is readable by anything else the child spawns.
func (s *Sidecar) ChildEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		switch {
		case strings.HasPrefix(kv, "ANTHROPIC_BASE_URL="),
			strings.HasPrefix(kv, "ANTHROPIC_API_KEY="),
			strings.HasPrefix(kv, "ANTHROPIC_AUTH_TOKEN="):
			continue // replaced or deliberately dropped
		}
		out = append(out, kv)
	}
	return append(out, "ANTHROPIC_BASE_URL="+s.BaseURL())
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

	// Lens exposes provider-native passthrough routes, so no translation is needed: an Anthropic
	// request goes to the Anthropic passthrough exactly as the client wrote it.
	target := s.upstream + "/v1/proxy/anthropic" + r.URL.Path
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
