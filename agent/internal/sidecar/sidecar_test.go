package sidecar

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The sidecar is the most sensitive thing in this repository: a proxy on a developer's machine
// that every prompt they write passes through. These tests exist to pin the properties that make
// it safe to run, and each one is written so that it FAILS if the property is lost.

// upstream stands in for Lens. It records what actually arrived — headers and body — so the
// assertions are on what LEFT the machine, never on what the sidecar intended to send.
type upstream struct {
	*httptest.Server
	gotHeader http.Header
	gotBody   []byte
	gotPath   string
}

func newUpstream(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *upstream {
	t.Helper()
	u := &upstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.gotHeader = r.Header.Clone()
		u.gotPath = r.URL.Path
		u.gotBody, _ = io.ReadAll(r.Body)
		if handler != nil {
			handler(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	t.Cleanup(u.Close)
	return u
}

func start(t *testing.T, cfg Config) (*Sidecar, *bytes.Buffer) {
	t.Helper()
	var logged bytes.Buffer
	cfg.Log = &logged
	s, err := Start(cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, &logged
}

func post(t *testing.T, s *Sidecar, body string, header http.Header) *http.Response {
	t.Helper()
	req, err := http.NewRequest("POST", s.BaseURL()+"/v1/messages", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST through the sidecar: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// ⚠ THE PROMPT IS NEVER LOGGED, STORED OR INSPECTED.
//
// This is the property that decides whether anyone can be asked to run this. A proxy that sees
// every prompt is only acceptable if it demonstrably does nothing with them, and "demonstrably"
// means a test that fails the moment one is written down.
//
// The sentinel is a plausible secret — the kind of thing genuinely typed into a coding agent — so
// a failure reads as the incident it would be.
func TestThePromptIsNeverWrittenDown(t *testing.T) {
	const secret = "PROMPT-SENTINEL-rotate-the-prod-db-password-hunter2"
	up := newUpstream(t, nil)
	s, logged := start(t, Config{LensURL: up.URL, LensAPIKey: "lens-key", Issue: "ENG-42"})

	body := `{"model":"claude-opus-4","messages":[{"role":"user","content":"` + secret + `"}]}`
	post(t, s, body, nil)

	if strings.Contains(logged.String(), secret) {
		t.Errorf("the prompt was written to the sidecar's log:\n%s", logged.String())
	}
	// Anything resembling the message payload is a failure, not just the sentinel — a sidecar that
	// logged the body minus the sentinel would still be reading it.
	for _, leak := range []string{"messages", "role", "content"} {
		if strings.Contains(logged.String(), leak) {
			t.Errorf("the sidecar logged request-body content (%q):\n%s", leak, logged.String())
		}
	}

	// And it must have PASSED THROUGH, byte for byte. A sidecar that parsed the body to redact it
	// would also pass the assertion above while having read every prompt.
	if string(up.gotBody) != body {
		t.Errorf("the body was not forwarded verbatim:\n got %s\nwant %s", up.gotBody, body)
	}
}

// ⚠ THE CHILD'S OWN CREDENTIAL MUST NOT REACH LENS.
//
// Established empirically: when no API key is set, Claude Code sends its claude.ai OAuth token
// (sk-ant-oat01-…). The sidecar sits in the middle and MUST replace it — forwarding a developer's
// personal login to a Talyvor server would be a credential leak caused entirely by our tool.
func TestTheChildsOwnCredentialIsReplacedNotForwarded(t *testing.T) {
	const oauth = "Bearer sk-ant-oat01-THE-USERS-PERSONAL-LOGIN"
	up := newUpstream(t, nil)
	s, logged := start(t, Config{LensURL: up.URL, LensAPIKey: "lens-key", Issue: "ENG-42"})

	h := http.Header{}
	h.Set("Authorization", oauth)
	h.Set("x-api-key", "sk-ant-THE-USERS-OTHER-KEY")
	post(t, s, `{"messages":[]}`, h)

	if got := up.gotHeader.Get("Authorization"); got != "Bearer lens-key" {
		t.Errorf("Authorization reaching Lens = %q, want the Lens key", got)
	}
	if got := up.gotHeader.Get("x-api-key"); got != "" {
		t.Errorf("the child's x-api-key reached Lens: %q", got)
	}
	if strings.Contains(logged.String(), "oat01") || strings.Contains(logged.String(), "sk-ant") {
		t.Errorf("a credential was written to the log:\n%s", logged.String())
	}
}

// ⚠ THE BRANCH NAME IS NEVER SENT — the leak #36 was built to prevent.
//
// Lens reads X-Talyvor-Branch (internal/attribution/context.go) and stores it. Branch names carry
// customer names, incident numbers and unreleased codenames. Only the identifier travels.
func TestOnlyTheIdentifierTravelsNeverTheBranch(t *testing.T) {
	up := newUpstream(t, nil)
	s, _ := start(t, Config{LensURL: up.URL, LensAPIKey: "k", Issue: "ENG-42", Branch: "fix/acme-corp-breach-ENG-42"})
	post(t, s, `{}`, nil)

	if got := up.gotHeader.Get("X-Talyvor-Issue"); got != "ENG-42" {
		t.Errorf("X-Talyvor-Issue = %q, want ENG-42", got)
	}
	for _, h := range []string{"X-Talyvor-Branch", "X-Talyvor-Repo", "X-Talyvor-Author"} {
		if got := up.gotHeader.Get(h); got != "" {
			t.Errorf("%s reached Lens with %q — only the identifier may travel", h, got)
		}
	}
	for _, v := range up.gotHeader {
		for _, s := range v {
			if strings.Contains(s, "acme-corp") {
				t.Errorf("the branch name leaked through a header value: %q", s)
			}
		}
	}
}

// No issue detected means NO header at all, which Lens records as unattributed. That is honest;
// a guessed identifier would produce a wrong bill.
func TestNoIssueSendsNoHeaderRatherThanAnEmptyOne(t *testing.T) {
	up := newUpstream(t, nil)
	s, _ := start(t, Config{LensURL: up.URL, LensAPIKey: "k"})
	post(t, s, `{}`, nil)

	if _, present := up.gotHeader["X-Talyvor-Issue"]; present {
		t.Errorf("an empty X-Talyvor-Issue was sent; it should be absent entirely")
	}
}

// The Anthropic path a client calls maps onto Lens's own passthrough route.
func TestItForwardsOntoLensAnthropicPassthrough(t *testing.T) {
	up := newUpstream(t, nil)
	s, _ := start(t, Config{LensURL: up.URL, LensAPIKey: "k"})
	post(t, s, `{}`, nil)

	if up.gotPath != "/v1/proxy/anthropic/v1/messages" {
		t.Errorf("upstream path = %q, want /v1/proxy/anthropic/v1/messages", up.gotPath)
	}
}

// ⚠ THE CLIENT'S OPENING PROBE MUST NOT BE SENT TO LENS.
//
// Claude Code opens every session with HEAD /api/hello. Forwarding it addressed a route Lens does
// not have, so each session began by sending Lens a request it could only reject. Found by running
// the real client end to end, not by reading the code.
func TestTheConnectivityProbeIsAnsweredLocally(t *testing.T) {
	up := newUpstream(t, nil)
	s, _ := start(t, Config{LensURL: up.URL, LensAPIKey: "k"})

	resp, err := http.Head(s.BaseURL() + "/api/hello")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("probe returned %d, want 200", resp.StatusCode)
	}
	if up.gotPath != "" {
		t.Errorf("the probe was forwarded to Lens as %q; it must be answered locally", up.gotPath)
	}
}

// ⚠ STREAMING MUST SURVIVE.
//
// The assertion is that a chunk ARRIVES BEFORE THE NEXT IS SENT — not that the total is correct.
// A sidecar that buffered the whole response would still return the right bytes and would still
// make Claude Code feel broken, so total-correctness cannot be the test.
//
// There is no sleep here: upstream blocks on a channel until the test has actually read chunk one,
// so this proves ordering rather than merely being fast enough.
func TestChunksArriveBeforeTheNextIsSent(t *testing.T) {
	readFirst := make(chan struct{})
	up := newUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("upstream recorder cannot flush")
			return
		}
		fmt.Fprint(w, "data: chunk-one\n\n")
		f.Flush()
		select {
		case <-readFirst: // the client has it — only now send the rest
		case <-time.After(5 * time.Second):
			t.Error("chunk one was never read by the client: the sidecar buffered the stream")
			return
		}
		fmt.Fprint(w, "data: chunk-two\n\n")
		f.Flush()
	})
	s, _ := start(t, Config{LensURL: up.URL, LensAPIKey: "k"})
	resp := post(t, s, `{"stream":true}`, nil)

	br := bufio.NewReader(resp.Body)
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("reading the first chunk: %v", err)
	}
	if !strings.Contains(line, "chunk-one") {
		t.Fatalf("first line = %q, want chunk-one", line)
	}
	close(readFirst) // proves chunk one arrived while chunk two was still unsent

	rest, _ := io.ReadAll(br)
	if !strings.Contains(string(rest), "chunk-two") {
		t.Errorf("the rest of the stream did not arrive: %q", rest)
	}
}

// ⚠ LOOPBACK ONLY. A proxy holding a Lens API key must not be reachable from the network — a
// laptop on a café or conference network would otherwise be handing out billable API access.
func TestItListensOnLoopbackOnly(t *testing.T) {
	up := newUpstream(t, nil)
	s, _ := start(t, Config{LensURL: up.URL, LensAPIKey: "k"})

	host, _, err := net.SplitHostPort(strings.TrimPrefix(s.BaseURL(), "http://"))
	if err != nil {
		t.Fatal(err)
	}
	if host != "127.0.0.1" {
		t.Errorf("listening on %q, want 127.0.0.1", host)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		t.Errorf("%q is not a loopback address", host)
	}
}

// With no port requested the OS picks a free one, so the ordinary path cannot collide. A PINNED
// port can, and the failure must name the port and say what to do — not surface a bare EADDRINUSE.
func TestAPinnedPortAlreadyInUseSaysSo(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	port := taken.Addr().(*net.TCPAddr).Port

	up := newUpstream(t, nil)
	_, err = Start(Config{LensURL: up.URL, LensAPIKey: "k", Port: port, Log: io.Discard})
	if err == nil {
		t.Fatal("starting on a taken port succeeded; it must fail")
	}
	msg := err.Error()
	if !strings.Contains(msg, fmt.Sprint(port)) {
		t.Errorf("the error does not name the port %d: %s", port, msg)
	}
	if !strings.Contains(msg, "--port") {
		t.Errorf("the error does not say how to choose another port: %s", msg)
	}
}

// Two sidecars at once must both work — the default path picks a free port every time, so a
// developer with two repositories open is not told to close one.
func TestTwoSidecarsCoexist(t *testing.T) {
	up := newUpstream(t, nil)
	a, _ := start(t, Config{LensURL: up.URL, LensAPIKey: "k"})
	b, _ := start(t, Config{LensURL: up.URL, LensAPIKey: "k"})
	if a.BaseURL() == b.BaseURL() {
		t.Errorf("both sidecars took %s", a.BaseURL())
	}
}

// ⚠ CLOSING IT ACTUALLY CLOSES THE PORT. An orphaned proxy holding a Lens key is worse than no
// proxy at all, so "closed" has to mean the socket is gone, not that a flag was set.
func TestCloseReleasesThePort(t *testing.T) {
	up := newUpstream(t, nil)
	s, _ := start(t, Config{LensURL: up.URL, LensAPIKey: "k"})
	addr := strings.TrimPrefix(s.BaseURL(), "http://")
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if c, err := net.DialTimeout("tcp", addr, 2*time.Second); err == nil {
		c.Close()
		t.Errorf("%s still accepts connections after Close", addr)
	}
}

// ⚠ THE CHILD IS POINTED AT THE SIDECAR, AND NO KEY IS PLANTED IN ITS ENVIRONMENT.
//
// Established empirically against Claude Code 2.1.221: setting ANTHROPIC_API_KEY makes it print
// "claude.ai connectors are disabled because ANTHROPIC_API_KEY or another auth source is set" and
// drop the user's connectors. Leaving it unset keeps them, and the sidecar supplies the Lens
// credential itself. So the tool must never plant one.
//
// ⚠ THIS TEST USED TO ASSERT THE NAME WAS ABSENT, AND ITS REASON FOR IT WAS WRONG — the corrected
// rule is about the VALUE. Re-measured against Claude Code 2.1.226 by counting requests at a
// loopback recorder: an EMPTY ANTHROPIC_API_KEY prints NO warning and sends NO x-api-key, while a
// non-empty one prints the warning on the same instrument. Asserting the name was the accident that
// left aider unable to dial for two releases (see credential_test.go). What must never appear is a
// VALUE; the name itself is now planted deliberately and is pinned there.
func TestTheChildEnvironmentRedirectsWithoutPlantingAKey(t *testing.T) {
	up := newUpstream(t, nil)
	s, _ := start(t, Config{LensURL: up.URL, LensAPIKey: "lens-key"})

	env := s.ChildEnv([]string{"PATH=/usr/bin", "ANTHROPIC_API_KEY=pre-existing", "HOME=/home/dev"})
	joined := strings.Join(env, "\n")

	if !strings.Contains(joined, "ANTHROPIC_BASE_URL="+s.BaseURL()) {
		t.Errorf("ANTHROPIC_BASE_URL was not pointed at the sidecar:\n%s", joined)
	}
	for _, line := range env {
		if v, ok := strings.CutPrefix(line, "ANTHROPIC_API_KEY="); ok && v != "" {
			t.Errorf("a key VALUE was left in the child environment (%q) — this silently disables the user's claude.ai connectors", line)
		}
		if strings.HasPrefix(line, "ANTHROPIC_AUTH_TOKEN=") {
			t.Errorf("an auth token was left in the child environment (%q)", line)
		}
	}
	if !strings.Contains(joined, "ANTHROPIC_API_KEY=") {
		t.Errorf("the name is gone entirely, so aider dials nothing and spends nothing:\n%s", joined)
	}
	if !strings.Contains(joined, "PATH=/usr/bin") || !strings.Contains(joined, "HOME=/home/dev") {
		t.Errorf("the rest of the environment was not preserved:\n%s", joined)
	}
	if strings.Contains(joined, "lens-key") {
		t.Errorf("the Lens key was exposed to the child process:\n%s", joined)
	}
}
