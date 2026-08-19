package safeurl_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/talyvor/code/internal/safeurl"
)

// ONE RULE, TWO PORTS, AND THEY DID NOT AGREE.
//
// safeurl.Validate and the extension's safeBaseUrl are the same sentence — "a base URL it is safe to
// attach a Talyvor API key to" — written twice. Measured before this file existed, they disagreed on
// 6 of the 29 cases in testdata/safeurl-cases.json, in both directions, and the extension's half had
// no test of any kind.
//
// The disagreements came from host normalisation, not from intent. Go's url.Hostname() strips the
// IPv6 brackets and leaves legacy IPv4 spellings alone; Node's URL.hostname keeps the brackets and
// rewrites 0xa9fea9fe into 169.254.169.254. Both ports then compared strings against whatever shape
// their own runtime produces, so each was blind exactly where the other was not.
//
// This file asserts the Go port against the shared cases; extension/src/safeurl-pure.test.ts asserts
// the TypeScript port against the same file. Fixing one side alone reds that side.
//
// NOT IN THE TABLE, DELIBERATELY: the empty URL. Go returns nil for it (Track and Docs are optional,
// and their clients report IsConfigured()==false) while the extension returns "" (the same "not
// configured", spelled the way a string-returning function has to spell it). That is one meaning in
// two type systems, not a disagreement, and pinning it here would assert a difference in return
// convention rather than a difference in verdict.

type safeurlCase struct {
	URL  string `json:"url"`
	Safe bool   `json:"safe"`
	Why  string `json:"why"`
}

type safeurlCases struct {
	Cases []safeurlCase `json:"cases"`
}

// loadCases walks up for the shared file and FAILS LOUDLY when it is missing or short. A moved
// manifest would otherwise leave this test asserting nothing — which is the state the extension's
// half of this rule was already in.
func loadCases(t *testing.T) []safeurlCase {
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
			var m safeurlCases
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatalf("parsing %s: %v", p, err)
			}
			// A floor, not a count: the file may grow, but a truncated or emptied one must not
			// look like a pass. 29 cases at the commit that introduced it.
			if len(m.Cases) < 29 {
				t.Fatalf("%s has %d cases, expected at least 29 — a shrunken table proves less than it claims", p, len(m.Cases))
			}
			return m.Cases
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("testdata/safeurl-cases.json not found walking up from the package directory")
	return nil
}

func TestGoPortMatchesTheSharedCases(t *testing.T) {
	cases := loadCases(t)

	// Both verdicts must be represented, or a port that answered one way for everything would pass.
	var safe, unsafe int
	for _, c := range cases {
		if c.Safe {
			safe++
		} else {
			unsafe++
		}
	}
	if safe == 0 || unsafe == 0 {
		t.Fatalf("table is one-sided (%d safe, %d unsafe) — it could not catch a constant answer", safe, unsafe)
	}

	for _, c := range cases {
		err := safeurl.Validate("lens-url", c.URL)
		got := err == nil
		if got != c.Safe {
			t.Errorf("Validate(%q) = safe:%v, shared table says safe:%v\n  why: %s\n  err: %v",
				c.URL, got, c.Safe, c.Why, err)
		}
	}
}
