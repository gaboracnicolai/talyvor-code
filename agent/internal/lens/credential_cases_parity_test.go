package lens

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ONE RULE, THREE PORTS — asserted from one file, the way safeurl-cases.json already is.
//
// testdata/credential-verdict-cases.json records what a client may conclude about the user's API
// key from the HTTP status of GET /v1/auth/me. The Kotlin port asserts itself against it in
// jetbrains/src/test/kotlin/com/talyvor/code/CredentialVerdictParityTest.kt and the TypeScript port
// in extension/src/lens/credential-cases.test.ts. Fixing one port alone reds that port; editing the
// shared file alone reds all three.
//
// It exists because all three ports previously agreed on the WRONG answer: each tested the
// connection with an unauthenticated GET /healthz and reported success, so a wrong, revoked or
// expired key produced a green tick everywhere.

type verdictCase struct {
	Status  int    `json:"status"`
	Verdict string `json:"verdict"`
	Why     string `json:"why"`
}

// casesPath walks up for the shared file and FAILS LOUDLY when it is missing or short — a moved or
// truncated manifest would otherwise leave this test asserting nothing.
func loadVerdictCases(t *testing.T) []verdictCase {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		p := filepath.Join(dir, "testdata", "credential-verdict-cases.json")
		if _, err := os.Stat(p); err == nil {
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("read %s: %v", p, err)
			}
			var doc struct {
				Cases []verdictCase `json:"cases"`
			}
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("parse %s: %v", p, err)
			}
			if len(doc.Cases) < 13 {
				t.Fatalf("credential-verdict-cases.json holds %d cases; it held 13 when this test was "+
					"written. A shrinking table is a test asserting less, not a pass.", len(doc.Cases))
			}
			return doc.Cases
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("testdata/credential-verdict-cases.json not found walking up from the test's cwd")
		}
		dir = parent
	}
}

func TestCredentialVerdictParity(t *testing.T) {
	want := map[string]CredentialVerdict{
		"ok":       CredentialOK,
		"rejected": CredentialRejected,
		"unknown":  CredentialUnknown,
	}
	for _, c := range loadVerdictCases(t) {
		expected, ok := want[c.Verdict]
		if !ok {
			t.Fatalf("unknown verdict %q in the shared table", c.Verdict)
		}
		if got := verdictForStatus(c.Status); got != expected {
			t.Errorf("HTTP %d (%s): got %v, table says %v", c.Status, c.Why, got, expected)
		}
	}
}

// POSITIVE CONTROL ON THE POPULATION: the table must exercise all three verdicts. A table that had
// drifted to a single verdict would pass the loop above while proving nothing.
func TestCredentialVerdictTableExercisesEveryVerdict(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range loadVerdictCases(t) {
		seen[c.Verdict] = true
	}
	for _, v := range []string{"ok", "rejected", "unknown"} {
		if !seen[v] {
			t.Errorf("the shared table never exercises verdict %q", v)
		}
	}
}
