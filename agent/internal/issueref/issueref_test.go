package issueref

import "testing"

// The extraction rules. The HEADER these produce is asserted separately in internal/lens — a
// correct extractor whose value never reaches the wire is the failure this file cannot see.

func TestFromBranch_TheShapesDevelopersActuallyUse(t *testing.T) {
	for _, c := range []struct{ branch, want string }{
		{"feature/ENG-42-add-login", "ENG-42"},
		{"eng-42/fix", "ENG-42"}, // ⚠ lowercase must normalise or it attributes to nothing
		{"bugfix/ENG-7", "ENG-7"},
		{"ENG-1", "ENG-1"}, // single digit is a real issue number
		{"chore/mkt-1234-rewrite-copy", "MKT-1234"},
		{"refs/heads/feature/ENG-42", "ENG-42"}, // a ref prefix is not part of the chosen name
		{"origin/eng-42", "ENG-42"},
		// ⚠ NOT "ABC-123". Track builds the identifier from a team identifier that is required but
		// otherwise unvalidated, so a two-letter or a longer team must work too.
		{"feature/QA-3-flaky", "QA-3"},
		{"feature/PLATFORM-88-migrate", "PLATFORM-88"},
	} {
		if got := FromBranch(c.branch); got != c.want {
			t.Errorf("FromBranch(%q) = %q, want %q", c.branch, got, c.want)
		}
	}
}

// ⚠ NEVER GUESS. Each of these must attribute NOTHING.
func TestFromBranch_RefusesToGuess(t *testing.T) {
	for _, branch := range []string{
		"main", "master", "develop", "HEAD", // HEAD is what a detached checkout reports
		"MAIN", "Master",
		"spike/try-something",
		"feature/add-login",
		"",
		"   ",
	} {
		if got := FromBranch(branch); got != "" {
			t.Errorf("FromBranch(%q) = %q, want empty — attributing this would be a guess", branch, got)
		}
	}
}

func TestResolve_Precedence(t *testing.T) {
	branch := func(name string) func() (string, error) {
		return func() (string, error) { return name, nil }
	}
	failing := func() (string, error) { return "", errNotARepo }

	if id, src := Resolve("ENG-9", branch("feature/ENG-42")); id != "ENG-9" || src != "explicit" {
		t.Errorf("explicit did not win: (%q,%q)", id, src)
	}
	if id, src := Resolve("", branch("feature/ENG-42")); id != "ENG-42" || src != "branch" {
		t.Errorf("detection did not apply: (%q,%q)", id, src)
	}
	if id, src := Resolve("", branch("main")); id != "" || src != "none" {
		t.Errorf("main attributed something: (%q,%q)", id, src)
	}
	// ⚠ NOT A REPO IS THE SAME ANSWER AS NO MATCH: attribute nothing, do not fail the command.
	if id, src := Resolve("", failing); id != "" || src != "none" {
		t.Errorf("a git failure did not degrade to unattributed: (%q,%q)", id, src)
	}
	if id, src := Resolve("", nil); id != "" || src != "none" {
		t.Errorf("a nil branch reader did not degrade to unattributed: (%q,%q)", id, src)
	}
}

// ⚠ THE ABSENCE MUST BE VISIBLE. "my costs are not appearing in Track" is only diagnosable if the
// tool says it attributed nothing — and the branch name must never appear in what it says.
func TestDescribe_SaysWhenItResolvedToNothing(t *testing.T) {
	if got := Describe("", "none"); got == "" || !contains(got, "unattributed") {
		t.Errorf("Describe(none) = %q — it must say the work will be unattributed", got)
	}
	if got := Describe("ENG-42", "branch"); !contains(got, "ENG-42") || !contains(got, "branch") {
		t.Errorf("Describe(branch) = %q — it must name the identifier and where it came from", got)
	}
	// The branch name is never an input to Describe, so it cannot be printed. Pinned by signature.
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}

var errNotARepo = errNotRepo{}

type errNotRepo struct{}

func (errNotRepo) Error() string { return "not a git repository" }
