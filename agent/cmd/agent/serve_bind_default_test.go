package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"regexp"
	"testing"

	"github.com/talyvor/code/internal/config"
	"github.com/talyvor/code/internal/mcp"
)

// The MCP server's DEFAULT bind interface decides whether a bare
// `talyvor-code serve` is reachable only from this machine or from
// every host on the network. Nothing in this repository pinned it.
//
// MEASURED 2026-08-28 (tab-k7q9, W4.32) rather than assumed: the
// `-host` default was changed from "127.0.0.1" to "0.0.0.0" and the
// WHOLE agent suite — 23 packages — stayed green, exit 0. The server
// exposes read_file over the caller's codebase root, so that flip
// publishes the user's source tree to the LAN, bearer-token-gated
// but network-reachable. `ResolveServeToken` refuses to mint an
// EPHEMERAL token for a non-loopback bind, which catches the
// no-token case — but the README tells operators to set a stable
// TALYVOR_MCP_TOKEN, and with one set that refusal never fires and
// the bind is silent apart from a stderr warning. A printed warning
// is not a guard.
//
// The property asserted is LOOPBACK, not the spelling "127.0.0.1":
// "::1" and "localhost" are equally correct and must stay green, or
// this degrades into a spelling test that blocks a legitimate edit.
//
// ⚠ WHAT THIS DOES NOT PROVE, stated so nobody reads it as wider
// than it is: it pins the default the flag ADVERTISES, read back
// through the real runServe. It does NOT prove the listener uses
// that value — `addr` is built from `host` one line above
// ListenAndServe, and a future edit that hardcodes an address while
// leaving the flag registered would keep this green.

// serveFlagDefault returns the default runServe advertises for the
// named flag, read out of the real flag set by asking `serve` for
// its own usage. Absence is a FAILURE, never a skip: a guard that
// quietly finds nothing to check is the defect it exists to catch.
func serveFlagDefault(t *testing.T, name string) string {
	t.Helper()
	var out, errb bytes.Buffer
	// -h returns at fs.Parse, before any index, token or listener.
	err := runServe(&out, &errb, config.Config{}, []string{"-h"})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("runServe(-h) = %v, want flag.ErrHelp — the usage probe this guard reads is gone", err)
	}
	usage := errb.String()
	if usage == "" {
		t.Fatal("runServe(-h) wrote no usage to stderr — nothing to measure")
	}
	// flag prints:  -host string\n    \t<desc> (default "127.0.0.1")
	re := regexp.MustCompile(fmt.Sprintf(`(?m)^\s*-%s\b[^\n]*\n[^\n]*\(default ([^)]*)\)`, regexp.QuoteMeta(name)))
	m := re.FindStringSubmatch(usage)
	if m == nil {
		t.Fatalf("serve does not advertise a -%s flag with a default.\nusage:\n%s", name, usage)
	}
	// String defaults are quoted by the flag package; numeric ones are not.
	return trimQuotes(m[1])
}

func trimQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// TestServeBindsLoopbackByDefault is the load-bearing assertion.
func TestServeBindsLoopbackByDefault(t *testing.T) {
	got := serveFlagDefault(t, "host")
	if !mcp.IsLoopbackHost(got) {
		t.Fatalf("`talyvor-code serve` defaults to -host %q, which is NOT a loopback interface.\n"+
			"A bare serve would publish the MCP server — read_file included — to every network "+
			"interface. Bind loopback by default and require an explicit --host for LAN exposure.", got)
	}
}

// TestServeDefaultPortMatchesTheDocumentedClientConfig pins the one
// number the README hands operators to paste into an MCP client:
// README.md's `"url": "http://localhost:7777/mcp"`. A drifted
// default makes that copy-pasteable block silently wrong.
func TestServeDefaultPortMatchesTheDocumentedClientConfig(t *testing.T) {
	const documented = "7777"
	if got := serveFlagDefault(t, "port"); got != documented {
		t.Fatalf("serve defaults to -port %s but README.md documents port %s in the client config "+
			"example and in the `serve` walkthrough — update both together or neither", got, documented)
	}
}
