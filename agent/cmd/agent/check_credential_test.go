package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/talyvor/code/internal/config"
)

// ⚠ THE DEFECT THIS FILE PINS: `talyvor-code check` — whose own usage line is "Probe Lens and
// report whether everything is wired up" — probes GET /healthz, WHICH LENS SERVES
// UNAUTHENTICATED (talyvor-lens internal/dashboard/handler.go: "already unauthenticated"). The
// API key is never put on the wire, so a wrong, revoked or expired key produces
// "✓ Lens reachable at …" and EXIT 0 — the one diagnostic the product ships is silent in
// exactly the failure it exists to find.
//
// The fix probes GET /v1/auth/me (authenticated, free, side-effect-free, already served by Lens)
// AFTER the reachability probe, and is FAIL-OPEN: only 401/403 is a verdict. A 404 (older Lens
// without the route), a 5xx or a transport error leaves the report exactly as it is today, so
// this can never turn a working install red.

// lensStub serves /healthz unauthenticated (always 200) and /v1/auth/me with a status that
// depends on the Authorization header, which is the shape of the real deployment.
func lensStub(t *testing.T, meStatus func(auth string) int) (url string, meHits *int64) {
	t.Helper()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok","version":"9.9.9"}`))
		case "/v1/auth/me":
			atomic.AddInt64(&hits, 1)
			w.WriteHeader(meStatus(r.Header.Get("Authorization")))
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &hits
}

func checkCfg(url, key string) config.Config {
	return config.Config{LensURL: url, LensAPIKey: key, WorkspaceID: "ws-1"}
}

// C1 — THE RED. A key Lens rejects must not be reported as a working setup.
func TestCheck_RejectedKeyIsReportedAndFails(t *testing.T) {
	url, meHits := lensStub(t, func(string) int { return http.StatusUnauthorized })
	var out bytes.Buffer
	err := runCheck(&out, checkCfg(url, "sk-wrong"))
	if err == nil {
		t.Fatalf("check exited 0 with a key Lens rejects; output was:\n%s", out.String())
	}
	if !strings.Contains(strings.ToLower(err.Error()+out.String()), "key") {
		t.Fatalf("the failure never names the key: err=%v out=%s", err, out.String())
	}
	if atomic.LoadInt64(meHits) == 0 {
		t.Fatalf("check never sent an authenticated request — the key was not exercised at all")
	}
}

// C2 — 403 is the same verdict as 401 (a key that authenticates but is not permitted here).
func TestCheck_ForbiddenKeyAlsoFails(t *testing.T) {
	url, _ := lensStub(t, func(string) int { return http.StatusForbidden })
	var out bytes.Buffer
	if err := runCheck(&out, checkCfg(url, "sk-forbidden")); err == nil {
		t.Fatalf("403 reported as healthy; output:\n%s", out.String())
	}
}

// C3 — POSITIVE CONTROL, THE OTHER DIRECTION: a key Lens accepts still succeeds, and the
// success line says the key was checked. Without this, "always fail" would pass C1 and C2.
func TestCheck_AcceptedKeySucceedsAndSaysSo(t *testing.T) {
	url, meHits := lensStub(t, func(auth string) int {
		if auth == "Bearer sk-good" {
			return http.StatusOK
		}
		return http.StatusUnauthorized
	})
	var out bytes.Buffer
	if err := runCheck(&out, checkCfg(url, "sk-good")); err != nil {
		t.Fatalf("a key the server accepts was reported as broken: %v\n%s", err, out.String())
	}
	if atomic.LoadInt64(meHits) != 1 {
		t.Fatalf("expected exactly one credential probe, got %d", atomic.LoadInt64(meHits))
	}
	if !strings.Contains(out.String(), "reachable") {
		t.Fatalf("lost the reachability line: %s", out.String())
	}
	if !strings.Contains(strings.ToLower(out.String()), "key") {
		t.Fatalf("success never states that the key was verified: %s", out.String())
	}
}

// C4 — FAIL-OPEN, AND THIS IS THE CONTROL THAT STOPS THE FIX BREAKING WORKING INSTALLS: an
// older Lens with no /v1/auth/me route 404s. That MUST leave the report as it is today —
// success, exit 0, and no claim either way about the key.
func TestCheck_UnknownStatusIsFailOpen(t *testing.T) {
	url, _ := lensStub(t, func(string) int { return http.StatusNotFound })
	var out bytes.Buffer
	if err := runCheck(&out, checkCfg(url, "sk-unknown")); err != nil {
		t.Fatalf("a 404 on the credential probe turned a working install red: %v\n%s", err, out.String())
	}
	if strings.Contains(strings.ToLower(out.String()), "key verified") {
		t.Fatalf("claimed the key was verified when the probe could not tell: %s", out.String())
	}
}

// C5 — a 5xx on the credential probe is also fail-open (Lens degraded is not "your key is bad").
func TestCheck_ServerErrorIsFailOpen(t *testing.T) {
	url, _ := lensStub(t, func(string) int { return http.StatusBadGateway })
	var out bytes.Buffer
	if err := runCheck(&out, checkCfg(url, "sk-5xx")); err != nil {
		t.Fatalf("a 502 on the credential probe turned a working install red: %v\n%s", err, out.String())
	}
}
