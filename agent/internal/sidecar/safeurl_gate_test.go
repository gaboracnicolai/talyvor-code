package sidecar_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/talyvor/code/internal/safeurl"
	"github.com/talyvor/code/internal/sidecar"
)

// THE FOURTEENTH CALLER THAT FORGOT.
//
// internal/safeurl's own package comment records the audit that created it: the "may I attach the
// customer's API key to this URL" rule lived as an unexported helper reachable only through
// Config.Validate(), which made it OPT-IN AT THE CALL SITE — eleven subcommands remembered and two
// did not, so `talyvor-code serve` and `talyvor-code init` leaked the key in cleartext. Its stated
// fix was to stop relying on memory: "every client constructor (lens/track/docs) applies it and
// returns an error. There is no exported way to build a client that skips it — the guard is now a
// property of construction rather than a step a caller must remember."
//
// `talyvor-code exec` builds NO client. sidecar.Start takes cfg.LensURL, checks only that it is
// non-empty, and forward() then attaches `Authorization: Bearer <LensAPIKey>` to every request it
// proxies there. So exec passes through NEITHER gate: it does not call Config.Validate() (measured:
// runExec in cmd/agent/exec_cmd.go does not, and main.go's dispatch has no central call) and it
// never touches a guarded constructor. It is exactly the caller safeurl predicted would be "free to
// forget", and the original canary line reproduced verbatim through it:
//
//	LEAK  POST /v1/proxy/anthropic/v1/messages  Bearer tlv_CANARY_LENS
//
// This file moves the rule to where the sidecar is CONSTRUCTED, for the same reason safeurl moved it
// to the client constructors: a check in runExec would leave the next caller of sidecar.Start free
// to forget in turn.

type safeurlCase struct {
	URL  string `json:"url"`
	Safe bool   `json:"safe"`
	Why  string `json:"why"`
}

// loadSharedCases walks up for testdata/safeurl-cases.json — the same file the Go, TypeScript and
// Kotlin ports of this rule all assert themselves against — and FAILS LOUDLY when it is missing or
// short. A moved manifest would otherwise leave this test asserting nothing, which is the exact
// failure mode the safeurl parity test was written to end.
func loadSharedCases(t *testing.T) []safeurlCase {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		p := filepath.Join(dir, "testdata", "safeurl-cases.json")
		if _, err := os.Stat(p); err == nil {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("reading %s: %v", p, err)
			}
			var m struct {
				Cases []safeurlCase `json:"cases"`
			}
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatalf("parsing %s: %v", p, err)
			}
			return m.Cases
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("testdata/safeurl-cases.json not found above the working directory — this test would have verified nothing")
	return nil
}

// TestStartRefusesEveryBaseURLSafeurlRefuses is the gate. Its population is the SHARED corpus, not a
// list written here, so widening the rule widens this guard with no edit.
func TestStartRefusesEveryBaseURLSafeurlRefuses(t *testing.T) {
	cases := loadSharedCases(t)

	// ⚠ NON-VACUITY FLOOR. A corpus that shrank to nothing, or to safe-only, would make every
	// assertion below pass by having no work to do. These floors are the counts measured when this
	// file was written (29 = 10 safe + 19 unsafe); they may grow, never shrink.
	var safeN, unsafeN int
	for _, c := range cases {
		if c.Safe {
			safeN++
		} else {
			unsafeN++
		}
	}
	if len(cases) < 29 || safeN < 10 || unsafeN < 19 {
		t.Fatalf("corpus shrank: %d cases (%d safe, %d unsafe); floors are 29/10/19", len(cases), safeN, unsafeN)
	}

	for _, c := range cases {
		t.Run(c.URL, func(t *testing.T) {
			// ⚠ THE PREMISE, ASSERTED BEFORE THE CLAIM. This test says "the sidecar agrees with
			// safeurl". If safeurl itself stopped refusing a case, the loop below would still pass
			// while asserting something weaker than it claims. So the premise is checked first and
			// fails in its own right.
			verdict := safeurl.Validate("lens-url", c.URL)
			if c.Safe != (verdict == nil) {
				t.Fatalf("premise broken: corpus says safe=%v, safeurl.Validate says %v (%s)", c.Safe, verdict, c.Why)
			}

			s, err := sidecar.Start(sidecar.Config{
				LensURL:    c.URL,
				LensAPIKey: "tlv_TEST_KEY",
				Log:        io.Discard,
			})
			if s != nil {
				defer s.Close()
			}
			if c.Safe {
				if err != nil {
					t.Fatalf("safe base URL %q was refused by sidecar.Start: %v (%s)", c.URL, err, c.Why)
				}
				return
			}
			if err == nil {
				t.Fatalf("UNSAFE base URL %q was ACCEPTED by sidecar.Start — every request it proxies "+
					"carries Authorization: Bearer <LensAPIKey> to that host (%s)", c.URL, c.Why)
			}
			if !strings.Contains(err.Error(), "lens-url") {
				t.Fatalf("refused %q but not through the shared rule (error was %q) — a message that does "+
					"not name the rule is a second rule that will drift from it", c.URL, err)
			}
		})
	}
}

// TestExecCannotSendTheKeyToAHostSafeurlRefuses is the end-to-end half: a listener that records what
// it was sent, reached through a spelling safeurl exists to refuse. It fails on the LEAK, not on a
// missing error, so it stays meaningful even if the gate above is ever moved or weakened.
//
// The spelling is deliberate: safeurl's own comment records that Go's http.Client reaches a loopback
// listener through "0x7f000001" while net.ParseIP returns nil for it, which is why ambiguous numeric
// hosts are refused outright. That makes it a hostile URL that is also reachable in a hermetic test,
// with no non-loopback interface and no network.
func TestExecCannotSendTheKeyToAHostSafeurlRefuses(t *testing.T) {
	type seen struct {
		method, path, auth string
	}
	got := make(chan seen, 4)
	canary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- seen{r.Method, r.URL.Path, r.Header.Get("Authorization")}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer canary.Close()

	u, err := url.Parse(canary.URL)
	if err != nil {
		t.Fatal(err)
	}
	hostile := "http://0x7f000001:" + u.Port()

	// The premise again: this URL must be one safeurl refuses, or the test proves nothing.
	if safeurl.Validate("lens-url", hostile) == nil {
		t.Fatalf("premise broken: safeurl now ACCEPTS %q, so this test no longer describes a leak", hostile)
	}

	s, startErr := sidecar.Start(sidecar.Config{
		LensURL:     hostile,
		LensAPIKey:  "tlv_CANARY_LENS",
		WorkspaceID: "ws_canary",
		Log:         io.Discard,
	})
	if startErr == nil {
		defer s.Close()
		resp, err := http.Post(s.BaseURL()+"/v1/messages", "application/json", nil)
		if err == nil {
			_ = resp.Body.Close()
		}
	}

	select {
	case g := <-got:
		t.Fatalf("LEAK  %s %s  %s\n\tthe canary at %s was reached through %q, which safeurl refuses",
			g.method, g.path, g.auth, canary.URL, hostile)
	default:
	}

	if startErr == nil {
		t.Fatalf("sidecar.Start accepted %q; nothing reached the canary this run, but the proxy is live "+
			"at %s and the next request would carry the key", hostile, s.BaseURL())
	}
	fmt.Fprintln(io.Discard, startErr)
}
