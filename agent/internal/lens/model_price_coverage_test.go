package lens

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/talyvor/code/internal/model"
)

// model_price_coverage_test.go — EVERY PRICE THIS AGENT QUOTES, AND WHICH BRANCH QUOTED IT.
//
// ⚠ THE FINDING: `EstimateCostUSD` HAS A CASE FOR A MODEL THAT HAS NEVER EXISTED IN THIS PRODUCT,
// AND THE OPUS THE PRODUCT ACTUALLY OFFERS IS PRICED AT THE FALLBACK RATE.
//
// The switch reads `case "claude-opus-4-7"`. `model.KnownModels` — the catalogue every surface
// offers from — holds `claude-haiku-4-5`, `claude-sonnet-4-6`, `claude-opus-4-6`, `gpt-4o`,
// `gpt-4o-mini`, `mistral-large`. MEASURED with a whole-tree grep: the string
// `claude-opus-4-7` appears at exactly ONE place in this repository, and it is that case label.
// Not the catalogue, not the extension, not the docs, not talyvor-lens's catalog seed (which
// carries opus-4-5, 4-6 and 4-8). **The branch cannot be reached by any model the product offers.**
//
// The consequence is not that a branch is dead. It is that `claude-opus-4-6` — the most expensive
// model in the catalogue — falls through to `default`, whose own comment says it is there for
// "claude-haiku-4-5 and unknown models". The priciest model in the product is quoted at the
// cheapest model's rate, and `Usage.CostUSD` is what eleven production call sites return: the
// agent loop, `ask`, `commit`, `review`, `plan`, shell generation and four MCP tools.
//
// ⚠⚠ WHAT THIS FILE DOES NOT DO, AND WHY. **It does not change a rate.** The queue forbids a
// session changing a price, and W4.11 set the precedent in this very repository: it corrected the
// false COST CLAIMS and added a guard while recording "THE PRICE CONSTANT IS UNCHANGED AND THE
// DECISION BELOW IS STILL OPEN FOR NICOLAI". The same is true here, and there is a second reason
// to keep hands off the numbers: the rates in the dead branch (15.0/75.0) are not talyvor-lens's
// opus rates either. Lens `internal/catalog/seed.go`, which THIS function's own comment names as
// "the source of truth", carries `claude-opus-4-6` at **5.00 / 25.00** and `claude-haiku-4-5` at
// **1.00 / 5.00** — while the default here quotes **0.80 / 4.00** and cites that file for it.
// So the correct rate is a question with three candidate answers already in play, and picking one
// is exactly the decision a session may not take. THE REPAIR IS THE DEAD BRANCH AND NOTHING ELSE.
//
// ⚠ AND DELETING IT MOVES NO PRICE, WHICH IS ASSERTED RATHER THAN ARGUED — see
// TestEstimateCostUSD_NoCataloguePriceMoves below, which pins the quoted cost of every catalogue
// model at a fixed token count.

// switchCases returns the string literals of every `case` inside EstimateCostUSD, read from the
// SOURCE rather than by calling the function.
//
// ⚠ IT HAS TO BE THE SOURCE. A dead case is invisible from the outside by definition: calling
// EstimateCostUSD with every catalogue id can only ever exercise the branches those ids reach, so
// a label naming a model nobody offers is precisely the thing a behavioural test cannot see. That
// is why this rule reads the file — and why the floor below exists, because a parse that finds
// nothing agrees with a switch that is perfect.
func switchCases(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("client.go")
	if err != nil {
		t.Fatalf("read client.go: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "func EstimateCostUSD(")
	if start < 0 {
		t.Fatal("EstimateCostUSD is not declared in client.go — this rule reads that function by " +
			"name, so a rename silently empties it. Re-anchor deliberately.")
	}
	end := strings.Index(body[start:], "\n}\n")
	if end < 0 {
		t.Fatal("could not find the end of EstimateCostUSD")
	}
	fn := body[start : start+end]
	var out []string
	for _, m := range regexp.MustCompile(`case\s+"([^"]+)"`).FindAllStringSubmatch(fn, -1) {
		out = append(out, m[1])
	}
	return out
}

// ⚠ THE VACUITY FLOOR. Every rule below iterates the parsed cases, so a reformat, a rename or a
// switch rewritten as a map empties the parse and reports a clean price table having read nothing.
// The floor is a literal — never len() of the thing it protects.
const expectedPriceCases = 1

func TestEstimateCostUSD_TheParseStillFindsThePriceBranches(t *testing.T) {
	got := switchCases(t)
	if len(got) < expectedPriceCases {
		t.Fatalf("parsed %d case labels out of EstimateCostUSD, want at least %d. Either the "+
			"switch was rewritten or the parse broke — which silently empties every rule below "+
			"and reports a price table nobody read. Got: %v", len(got), expectedPriceCases, got)
	}
}

// ⚠ THE FINDING. A price branch that names a model the catalogue does not offer prices nothing,
// and it hides the fact that the model it LOOKS like it prices is on the fallback rate.
func TestEstimateCostUSD_EveryPriceBranchNamesAModelTheProductOffers(t *testing.T) {
	known := map[string]bool{}
	for _, m := range model.KnownModels {
		known[m.ID] = true
	}
	var orphans []string
	for _, c := range switchCases(t) {
		if !known[c] {
			orphans = append(orphans, c)
		}
	}
	if len(orphans) > 0 {
		t.Errorf("EstimateCostUSD prices %v, and model.KnownModels offers none of them.\n"+
			"A case label naming a model the product does not sell cannot be reached, and the "+
			"damage is not the dead code: the catalogue model whose name it resembles is being "+
			"quoted at the `default` rate, which that branch's own comment reserves for haiku "+
			"and for UNKNOWN models. Catalogue: %v", orphans, ids())
	}
}

func ids() []string {
	out := make([]string, 0, len(model.KnownModels))
	for _, m := range model.KnownModels {
		out = append(out, m.ID)
	}
	return out
}

// ⚠ THE CENSUS, AND IT IS THE HALF THAT SURVIVES THE REPAIR. Deleting the dead branch does not
// give opus a price; it only stops the switch implying one. This table writes down which
// catalogue models are quoted from an explicit branch and which fall to the fallback, so the
// answer is a pinned fact rather than something a reader has to re-derive — and so a model
// silently JOINING the fallback set (a new frontier model, priced at haiku) is loud.
//
// ⚠ BOTH DIRECTIONS. A model moving OFF the fallback reds too. That is deliberate: it is what a
// price decision landing looks like, and it should not land silently on this file's watch.
func TestEstimateCostUSD_WhichCatalogueModelsAreQuotedFromTheFallback(t *testing.T) {
	explicit := map[string]bool{}
	for _, c := range switchCases(t) {
		explicit[c] = true
	}

	// MEASURED at 8179336, not predicted. `true` = this catalogue model has a branch of its own.
	want := map[string]bool{
		"claude-haiku-4-5":  false,
		"claude-sonnet-4-6": true,
		"claude-opus-4-6":   false, // ⚠ THE MOST EXPENSIVE MODEL IN THE CATALOGUE, ON THE CHEAPEST RATE.
		"gpt-4o":            false,
		"gpt-4o-mini":       false,
		"mistral-large":     false,
	}

	if len(want) != len(model.KnownModels) {
		t.Fatalf("this census pins %d models and the catalogue offers %d. A model was added or "+
			"removed and this table did not move — which is exactly how a new model comes to be "+
			"quoted at the fallback rate with nothing saying so. Catalogue: %v",
			len(want), len(model.KnownModels), ids())
	}
	for _, m := range model.KnownModels {
		w, pinned := want[m.ID]
		if !pinned {
			t.Errorf("%s is in the catalogue and not in this census", m.ID)
			continue
		}
		if explicit[m.ID] != w {
			t.Errorf("%s: has its own price branch = %v, pinned %v. If a rate was just given to "+
				"this model, that is a PRICE DECISION and it must be taken deliberately, not "+
				"noticed here; if a rate was taken away, the model just moved onto the fallback "+
				"the default reserves for unknown models.", m.ID, explicit[m.ID], w)
		}
	}
}

// ⚠ THE MUST-STAY-GREEN COMPANION, AND IT IS WHAT MAKES THE REPAIR SAFE TO MERGE. Removing a
// branch from a pricing switch is the kind of change that should have to prove it moved nothing.
// These are the quoted costs at 8179336, BEFORE the dead branch was removed; they must be
// identical after. A rate change reds here first, which is the point — this file must not be the
// place a price moves quietly.
func TestEstimateCostUSD_NoCataloguePriceMoves(t *testing.T) {
	const in, out = 1_000_000, 1_000_000
	want := map[string]float64{
		"claude-haiku-4-5":  0.80 + 4.00,
		"claude-sonnet-4-6": 3.00 + 15.00,
		"claude-opus-4-6":   0.80 + 4.00,
		"gpt-4o":            0.80 + 4.00,
		"gpt-4o-mini":       0.80 + 4.00,
		"mistral-large":     0.80 + 4.00,
	}
	for _, m := range model.KnownModels {
		w, ok := want[m.ID]
		if !ok {
			t.Errorf("%s is in the catalogue and this pin does not price it", m.ID)
			continue
		}
		if got := EstimateCostUSD(m.ID, in, out); got != w {
			t.Errorf("EstimateCostUSD(%s, 1M, 1M) = %v, pinned %v. THIS IS A PRICE MOVING. If it "+
				"is deliberate it is Nicolai's call and this pin is where it is recorded; if it "+
				"is a side effect of touching the switch, it is the defect this file exists for.",
				m.ID, got, w)
		}
	}
}
