package main

import (
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/talyvor/code/internal/config"
	"github.com/talyvor/code/internal/mcp"
)

// serve_bind_default_test.go pins the default the `-host` flag
// ADVERTISES, read back through the real runServe. Its own comment
// names what that cannot see:
//
//	"It does NOT prove the listener uses that value — `addr` is built
//	 from `host` one line above ListenAndServe, and a future edit that
//	 hardcodes an address while leaving the flag registered would keep
//	 this green."
//
// MEASURED 2026-08-28 (tab-n2f7, W4.33) rather than assumed. Exactly
// that edit was applied, twice, with the -host flag and its 127.0.0.1
// default untouched:
//
//	Addr: fmt.Sprintf("0.0.0.0:%d", port)          -> whole suite GREEN
//	addr := fmt.Sprintf("0.0.0.0:%d", port)        -> whole suite GREEN
//
// 23 packages, exit 0, both times. The first is the quiet form: the
// startup line still PRINTS 127.0.0.1:7777 while every interface is
// bound, and the MCP server exposes read_file over the caller's
// codebase root. A guard that reads the flag cannot see the socket.
//
// This file closes that by observing the address actually bound.
// runServe is driven for real — index, token, routes, listener — and
// the listener is the real one net.Listen returns, so ln.Addr() is a
// property of an open socket rather than of a string.

// errStopServe unwinds http.Server.Serve immediately. The socket is
// genuinely bound first (that is the whole point), but nothing is
// left accepting: Serve closes the listener on return.
var errStopServe = errors.New("bind guard: stop serving")

type stopImmediately struct{ net.Listener }

func (l stopImmediately) Accept() (net.Conn, error) { return nil, errStopServe }

type bindCall struct {
	network string
	addr    string // the string runServe handed to net.Listen
	bound   string // ln.Addr().String() — what the kernel gave back
	err     error
}

// serveGuardTimeout bounds the wait for runServe to reach the seam.
// The normal path takes single-digit milliseconds; this only ever
// elapses in the vacuity case below.
const serveGuardTimeout = 30 * time.Second

// runServeCapturingBind runs the real runServe with the given serve
// args and returns every listen it attempted.
//
// ⚠ IT WAITS ON THE SEAM, NOT ON runServe RETURNING, AND THAT IS NOT
// A STYLE CHOICE. MEASURED 2026-08-28 (control C8): reverting to
// srv.ListenAndServe() leaves serveListen declared-but-never-called,
// the socket bound directly, and runServe blocking in Accept FOREVER.
// A straight-line `err := runServe(...)` never reaches the
// "absence FAILS" assertion below — the package dies on the 10-minute
// go test timeout instead, with no named test in the output. That is
// still technically a red, but a guard whose vacuity case costs ten
// minutes and reports as a timeout panic is a guard that gets called
// flaky and deleted. It must red HERE, by name, in milliseconds.
func runServeCapturingBind(t *testing.T, args ...string) []bindCall {
	t.Helper()
	var (
		mu       sync.Mutex
		calls    []bindCall
		once     sync.Once
		listened = make(chan struct{})
	)
	orig := serveListen
	t.Cleanup(func() { serveListen = orig })
	serveListen = func(network, addr string) (net.Listener, error) {
		c := bindCall{network: network, addr: addr}
		ln, err := net.Listen(network, addr)
		if err != nil {
			c.err = err
		} else {
			c.bound = ln.Addr().String()
		}
		mu.Lock()
		calls = append(calls, c)
		mu.Unlock()
		once.Do(func() { close(listened) })
		if err != nil {
			return nil, err
		}
		return stopImmediately{ln}, nil
	}

	// -port 0 so the kernel picks a free port: a fixed port makes this
	// test fail when anything else on the machine holds it, and a flaky
	// guard is a guard that gets deleted.
	full := append([]string{"-root", t.TempDir(), "-port", "0"}, args...)
	done := make(chan error, 1)
	go func() { done <- runServe(&strings.Builder{}, &strings.Builder{}, config.Config{}, full) }()

	fail := func(err error) {
		t.Helper()
		// A listen that FAILED is a recorded result, not a harness error — the
		// caller decides whether an unbindable address is this case's finding.
		// Anything else (a bad flag, a client constructor, an index) means the
		// listener was never reached and no assertion below would mean anything.
		mu.Lock()
		n := len(calls)
		mu.Unlock()
		if err != nil && !errors.Is(err, errStopServe) && n == 0 {
			t.Fatalf("runServe(%v) = %v, want the listener to have been reached", full, err)
		}
	}

	select {
	case <-listened:
		select {
		case err := <-done:
			fail(err)
		case <-time.After(serveGuardTimeout):
			t.Fatalf("runServe(%v) reached serveListen but did not return within %s — "+
				"http.Server.Serve should return at once on the stopped listener", full, serveGuardTimeout)
		}
	case err := <-done:
		fail(err)
	case <-time.After(serveGuardTimeout):
		t.Fatalf("runServe(%v) neither returned nor called serveListen within %s.\n"+
			"The listener does not go through the seam this guard observes, so NOTHING "+
			"here is being measured — every assertion below would be vacuous. This is what "+
			"srv.ListenAndServe() looks like from in here: the socket is bound directly and "+
			"runServe blocks in Accept forever.", full, serveGuardTimeout)
	}

	mu.Lock()
	defer mu.Unlock()
	return append([]bindCall(nil), calls...)
}

// TestServeBindsTheLoopbackDefaultForReal is the load-bearing
// assertion: not what -host says, what the socket is.
func TestServeBindsTheLoopbackDefaultForReal(t *testing.T) {
	calls := runServeCapturingBind(t)

	// Absence FAILS. A guard that quietly finds no listener to inspect
	// is the defect it exists to catch.
	if len(calls) != 1 {
		t.Fatalf("runServe made %d listen calls, want exactly 1: %+v", len(calls), calls)
	}
	c := calls[0]
	if c.err != nil {
		t.Fatalf("runServe could not bind its own default: listen %s %s: %v", c.network, c.addr, c.err)
	}
	host, port, err := net.SplitHostPort(c.bound)
	if err != nil {
		t.Fatalf("bound address %q does not split: %v", c.bound, err)
	}
	if !mcp.IsLoopbackHost(host) {
		t.Fatalf("`talyvor-code serve` BOUND %s, whose host %q is NOT a loopback interface.\n"+
			"The -host flag may still advertise a loopback default — that is what "+
			"serve_bind_default_test.go checks and it cannot see this. A bare serve is "+
			"publishing the MCP server, read_file included, to every network interface.", c.bound, host)
	}
	// A real socket, not a placeholder: -port 0 must come back resolved.
	if port == "0" || port == "" {
		t.Fatalf("bound address %q has no resolved port — nothing was actually listening", c.bound)
	}
}

// TestServeBuildsAListenableAddressForEveryHostItCallsLegitimate is
// the regression guard for a defect the flag-level guard could not
// have found, because it never binds.
//
// serve_bind_default_test.go states that "::1" and "localhost" are
// "equally correct and must stay green, or this degrades into a
// spelling test that blocks a legitimate edit", and W4.32's controls
// C2/C3 asserted exactly that. MEASURED: `fmt.Sprintf("%s:%d", host,
// port)` built "::1:7777" for the IPv6 form, and net.Listen refuses
// it outright — "listen tcp: address ::1:7777: too many colons in
// address". So one of the two spellings the guard vouched for could
// never have bound anything. net.JoinHostPort brackets it.
//
// The assertion is on the ADDRESS STRING runServe constructs, not on
// a successful bind, deliberately: a machine with no IPv6 loopback
// would fail a bind assertion for an unrelated reason, and the defect
// being guarded is in the formatting, which is environment-free.
func TestServeBuildsAListenableAddressForEveryHostItCallsLegitimate(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "localhost", "::1"} {
		t.Run(host, func(t *testing.T) {
			if !mcp.IsLoopbackHost(host) {
				t.Fatalf("premise broken: IsLoopbackHost(%q) is false, so this case is not "+
					"one of the spellings serve_bind_default_test.go calls legitimate", host)
			}
			calls := runServeCapturingBind(t, "-host", host)
			if len(calls) != 1 {
				t.Fatalf("runServe made %d listen calls, want exactly 1: %+v", len(calls), calls)
			}
			addr := calls[0].addr
			gotHost, _, err := net.SplitHostPort(addr)
			if err != nil {
				t.Fatalf("runServe -host %s built the address %q, which net.SplitHostPort "+
					"cannot parse (%v) — net.Listen refuses it for the same reason. "+
					"Use net.JoinHostPort so an IPv6 literal is bracketed.", host, addr, err)
			}
			if gotHost != host {
				t.Fatalf("runServe -host %s built the address %q, whose host is %q — the "+
					"listener is not using the interface the operator asked for", host, addr, gotHost)
			}
		})
	}
}
