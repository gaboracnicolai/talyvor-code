package cmdguard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// ⚠ THE SHARED CORPUS CANNOT EXPRESS THE ONE DRIFT THAT EXISTS.
//
// cmdguard-pure.ts:12 states the contract this pair runs on — "THIS IS A PORT ... Two guards that
// disagree are worse than one, because the weaker one is the one that matters ... a drift fails a
// build" — and testdata/cmdguard-corpus.json is what is supposed to make that true. But a
// corpusCase carries ONE `decision` field for BOTH implementations, so it can only ever record a
// verdict the two already share. It is structurally unable to say "these two disagree"; it can only
// say "these two agree on X". Adding a case for an axis where they diverge means first choosing the
// answer, which is the permission decision nobody has taken.
//
// ⚠ AND THEY DO DIVERGE. Measured (W4.34, tab-h4w7), not read: this package trims with
// strings.TrimSpace, which is unicode.IsSpace; the port trims with String.prototype.trim, which is
// ECMAScript WhiteSpace + LineTerminator. Those are not the same set. They differ on exactly two
// code points out of the 29 either one claims:
//
//	U+0085 NEXT LINE                  unicode.IsSpace: yes   JS trim: no    -> Go allows, TS confirms
//	U+FEFF ZERO WIDTH NO-BREAK SPACE  unicode.IsSpace: no    JS trim: yes   -> TS allows, Go confirms
//
// Neither is an escalation to an arbitrary command — the head must still be in the allowlist, and
// both call sites treat Confirm as a prompt rather than a block. What it costs is that one side runs
// unattended what the other stops to ask about, on the boundary whose own header says two guards
// that disagree are worse than one.
//
// ⚠ THIS TEST DOES NOT FIX IT, BECAUSE WHICH WAY TO CONVERGE IS A PERMISSION DECISION. Matching Go
// widens the extension on U+0085; matching JS widens this agent on U+FEFF; trimming neither narrows
// both and widens nothing. That is a call for a human. What this test does is make the drift fail a
// build the way the header already claimed it did: every verdict in the manifest is measured from
// the shipped code, this side asserts its own column, and BOTH sides assert that the divergence is
// exactly the two code points below. A third divergence reds. A fixed divergence reds too, and says
// so — see wantDivergent.
//
// extension/src/agent/cmdguard-whitespace.test.ts is the mirror of this file.

type wsSide struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

type wsCase struct {
	Codepoint string `json:"codepoint"`
	Name      string `json:"name"`
	Shape     string `json:"shape"`
	Command   string `json:"command"`
	Go        wsSide `json:"go"`
	TS        wsSide `json:"ts"`
	Agree     bool   `json:"agree"`
}

type wsManifest struct {
	DivergentCodepoints  []string `json:"divergentCodepoints"`
	MinimumCases         int      `json:"minimumCases"`
	MinimumAgreeingCases int      `json:"minimumAgreeingCases"`
	Cases                []wsCase `json:"cases"`
}

// ⚠ HARD LITERAL FLOORS, NOT THE MANIFEST'S OWN NUMBERS. minimumCases lives in the file, so a
// blinded file that sets it to zero and empties `cases` would satisfy a floor read from itself —
// the exact shape of a count that is green in the world it exists to detect. These are the numbers
// this test refuses to run below, and they live in code the manifest cannot edit.
const (
	wsFloorCases     = 150
	wsFloorAgreeing  = 100
	wsFloorCodepoint = 25
)

// wantDivergent is the measured, filed divergence. It is an EXACT set on purpose.
//
// ⚠ IF YOU ARE READING THIS BECAUSE THE TEST WENT RED HERE: a code point was added or removed from
// the divergence, which means either a new drift appeared between the two ports, or somebody
// converged them and this pin is now stale. If it is the second — good, that is the decision being
// taken. Regenerate testdata/cmdguard-whitespace.json, shrink this set, and when it is empty delete
// this file and fold the cases into cmdguard-corpus.json, which can hold them once there is a single
// agreed verdict to hold.
var wantDivergent = []string{"U+0085", "U+FEFF"}

// loadWhitespaceManifest walks up for the shared file. ⚠ LOUD, NOT EMPTY — the same rule
// loadCorpus follows, and for the same reason: a manifest that has moved must not leave this suite
// verifying nothing.
func loadWhitespaceManifest(t *testing.T) wsManifest {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		p := filepath.Join(dir, "testdata", "cmdguard-whitespace.json")
		if _, err := os.Stat(p); err == nil {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("reading %s: %v", p, err)
			}
			var m wsManifest
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatalf("parsing %s: %v", p, err)
			}
			return m
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("testdata/cmdguard-whitespace.json not found above the working directory — "+
		"this suite would have verified nothing. %s", "restore it or delete this test deliberately")
	return wsManifest{}
}

// TestWhitespaceManifestIsNotVacuous is the floor. Everything below compares against the manifest,
// so an empty or shrunken manifest would make every other assertion trivially true.
func TestWhitespaceManifestIsNotVacuous(t *testing.T) {
	m := loadWhitespaceManifest(t)

	if len(m.Cases) < wsFloorCases {
		t.Fatalf("only %d cases in the whitespace manifest, floor is %d — a shrunken corpus "+
			"cannot show a drift", len(m.Cases), wsFloorCases)
	}
	if m.MinimumCases < wsFloorCases {
		t.Errorf("manifest declares minimumCases=%d, below this test's floor of %d",
			m.MinimumCases, wsFloorCases)
	}
	if m.MinimumAgreeingCases < wsFloorAgreeing {
		t.Errorf("manifest declares minimumAgreeingCases=%d, below this test's floor of %d",
			m.MinimumAgreeingCases, wsFloorAgreeing)
	}

	agreeing, codepoints := 0, map[string]bool{}
	for _, c := range m.Cases {
		codepoints[c.Codepoint] = true
		if c.Agree {
			agreeing++
		}
	}
	// ⚠ BOTH DIRECTIONS HAVE TO BE POPULATED. A corpus of nothing but divergences proves the two
	// sides differ and says nothing about the far larger space where they must keep agreeing; a
	// corpus of nothing but agreements is what let this drift sit unnoticed in the first place.
	if agreeing < wsFloorAgreeing {
		t.Errorf("only %d AGREEING cases, floor is %d — the corpus no longer covers the space "+
			"where the two ports must not drift", agreeing, wsFloorAgreeing)
	}
	if len(codepoints) < wsFloorCodepoint {
		t.Errorf("only %d distinct code points, floor is %d — the union of unicode.White_Space "+
			"and ECMAScript WhiteSpace is what this axis is", len(codepoints), wsFloorCodepoint)
	}
	if agreeing == len(m.Cases) {
		t.Errorf("every case agrees, so nothing in this manifest is pinning a divergence — " +
			"if the ports really were converged, wantDivergent should be empty too")
	}
}

// TestWhitespaceParityGoSide is the load-bearing one: it reads the SHIPPED behaviour of this
// package and holds it to the manifest. Decision AND reason — W4.34 measured that a one-sided
// widening for a head already in allowedHeads moves the reason and leaves the decision alone, so a
// comparison of the decision bit alone is blind to half the drift it exists to catch.
func TestWhitespaceParityGoSide(t *testing.T) {
	m := loadWhitespaceManifest(t)
	checked := 0
	for _, c := range m.Cases {
		v := Check(c.Command)
		if got := v.Decision.String(); got != c.Go.Decision {
			t.Errorf("%s %s (%s) %q: Go decision is %q, manifest says %q — this package's "+
				"whitespace handling moved; regenerate the manifest and re-read the divergence",
				c.Codepoint, c.Name, c.Shape, c.Command, got, c.Go.Decision)
		}
		if v.Reason != c.Go.Reason {
			t.Errorf("%s %s (%s) %q: Go reason is %q, manifest says %q",
				c.Codepoint, c.Name, c.Shape, c.Command, v.Reason, c.Go.Reason)
		}
		checked++
	}
	if checked < wsFloorCases {
		t.Fatalf("checked only %d commands, floor is %d", checked, wsFloorCases)
	}
}

// TestWhitespaceDivergenceIsExactlyTheFiledSet holds the header's claim to the file. The manifest's
// `agree` column and its `divergentCodepoints` list are two statements about the same fact, so
// deriving one from the other and comparing catches an edit to either alone.
func TestWhitespaceDivergenceIsExactlyTheFiledSet(t *testing.T) {
	m := loadWhitespaceManifest(t)

	derived := map[string]bool{}
	for _, c := range m.Cases {
		sameDecision := c.Go.Decision == c.TS.Decision
		sameReason := c.Go.Reason == c.TS.Reason
		if c.Agree != (sameDecision && sameReason) {
			t.Errorf("%s %s (%s): `agree` says %v but the two columns say %v — the manifest "+
				"contradicts itself", c.Codepoint, c.Name, c.Shape, c.Agree, sameDecision && sameReason)
		}
		if !c.Agree {
			derived[c.Codepoint] = true
		}
	}

	if diff := setDiff(derived, m.DivergentCodepoints); diff != "" {
		t.Errorf("divergentCodepoints does not match the cases it summarises: %s", diff)
	}
	if diff := setDiff(derived, wantDivergent); diff != "" {
		t.Errorf("THE MEASURED DIVERGENCE MOVED: %s\n"+
			"A code point appearing here is a NEW drift between the Go guard and its TypeScript "+
			"port. A code point disappearing means somebody converged them — if that is what "+
			"happened, this pin has done its job: regenerate the manifest, shrink wantDivergent, "+
			"and when it is empty delete this file and fold the cases into cmdguard-corpus.json.",
			diff)
	}
}

// setDiff names the members rather than reporting a count. "the sets differ" says something moved;
// naming the code point says which invisible character a shell may now be handed unattended.
func setDiff(got map[string]bool, want []string) string {
	w := map[string]bool{}
	for _, s := range want {
		w[s] = true
	}
	var extra, missing []string
	for s := range got {
		if !w[s] {
			extra = append(extra, s)
		}
	}
	for s := range w {
		if !got[s] {
			missing = append(missing, s)
		}
	}
	sort.Strings(extra)
	sort.Strings(missing)
	if len(extra) == 0 && len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf("measured-divergent-but-not-filed=%v filed-but-no-longer-divergent=%v",
		extra, missing)
}
