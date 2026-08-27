package lens

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/talyvor/code/internal/model"
)

// THE CATALOGUE ADVERTISES A PROVIDER THE DISPATCH LAYER CANNOT REACH, AND THE GATEWAY HAS THE ROUTE.
//
// model.KnownModels is the list every surface offers — `talyvor-code models`, the VS Code QuickPick,
// the JetBrains SelectModelAction — and each entry carries a Provider the user is shown. The dispatch
// layer does NOT read that field. CompleteAuto classifies by id prefix and has exactly two outcomes:
// `gpt-`/`o1`/`o3` go to the OpenAI route, EVERYTHING ELSE to the Anthropic route. So `mistral-large`,
// listed as Provider "Mistral", is POSTed to /v1/proxy/anthropic/v1/messages with an Anthropic body.
//
// CompleteAuto's own comment used to justify that with a claim about another repo — that the Anthropic
// path is where "mistral, …" belongs because Lens maps it server-side. MEASURED in talyvor-lens at
// 0efb8c6, read-only, and it is false in both halves:
//
//   - The provider is pinned by the ROUTE, not by the model. HandleAnthropic is
//     p.serve(w, r, p.configForProvider("anthropic")), and inference.ConfigFor("anthropic") sets the
//     upstream to AnthropicURL and the credential to `x-api-key: AnthropicKey`. Nothing on the serve
//     path maps a model id to a provider — providerFromID exists only in internal/catalog/resolve.go,
//     which prices a request and does not route it.
//   - Lens already HAS the route this model needs. cmd/lens/main.go registers
//     /v1/proxy/mistral/* -> HandleExtraProvider("mistral"), beside google, bedrock, groq and vllm.
//     No client in this repo has ever called any of them.
//
// So the one entry all three ports agree on is the one that cannot work, and it compounds with the
// finding W4.13 recorded and did not fix: Lens's catalogue id is `mistral-large-latest`, so even the
// spelling is unpriced and bills on a fallback bound.
//
// WHY NOTHING COULD SEE IT: the catalogue is hand-copied into three ports and no test crossed the
// catalogue with the router. selector_test.go asserts the catalogue against a restatement of itself;
// client_test.go asserts the router against literal ids. Neither ever asked whether a model the
// product OFFERS is a model the product can SEND.
//
// This file measures the Go port's real dispatch — a live httptest server, the real CompleteAuto,
// the path it actually POSTs to. extension/src/model/model-routing.test.ts measures the TypeScript
// port's real dispatch against the same table. Fixing one side alone reds that side.
//
// NOT COVERED, SAID PLAINLY: the JetBrains port is identically affected —
// SsePure.providerForModel is the same two-way classifier and LensClient.kt picks the same two
// endpoints from it — and it is NOT in this harness. There is no Java runtime on the machine this
// was written on, so a Kotlin assertion could not be executed here, and a guard whose green has
// never been observed is not a guard. It wants the same table and its own merge.

type routingCase struct {
	ID              string `json:"id"`
	CatalogProvider string `json:"catalogProvider"`
	RoutedEndpoint  string `json:"routedEndpoint"`
	RoutedProvider  string `json:"routedProvider"`
	Dispatchable    bool   `json:"dispatchable"`
	Why             string `json:"why"`
	// The NON-STREAMING dispatch. Complete/CompleteWithUsage are a second dispatcher and the
	// fields above cannot see them — see the block above blockingDispatchPathFor.
	BlockingEndpoint     string `json:"blockingEndpoint"`
	BlockingProvider     string `json:"blockingProvider"`
	BlockingDispatchable bool   `json:"blockingDispatchable"`
	WhyBlocking          string `json:"whyBlocking"`
}

type routingTable struct {
	Cases []routingCase `json:"cases"`
}

// routingFloor is a floor, not a count. The table may grow with the catalogue, but an emptied or
// truncated one must not read as a pass — and the population rule below cannot catch that on its
// own, because a catalogue emptied alongside the table satisfies it vacuously.
const routingFloor = 6

func loadRoutingTable(t *testing.T) []routingCase {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		p := filepath.Join(dir, "testdata", "model-routing.json")
		if _, err := os.Stat(p); err == nil {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("reading %s: %v", p, err)
			}
			var m routingTable
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatalf("parsing %s: %v", p, err)
			}
			if len(m.Cases) < routingFloor {
				t.Fatalf("%s has %d cases, expected at least %d — a shrunken table proves less than it claims", p, len(m.Cases), routingFloor)
			}
			return m.Cases
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("testdata/model-routing.json not found walking up from the package directory")
	return nil
}

// dispatchPathFor drives the REAL CompleteAuto against a live server and returns the path it POSTed
// to. Measured, not inferred from isOpenAIModel: the endpoint literal lives inside CompleteStream /
// CompleteStreamOpenAI, so a classifier that stayed right while an endpoint moved would still be a
// misroute, and only driving the real call can see it.
func dispatchPathFor(t *testing.T, modelID string) string {
	t.Helper()
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		// Answer as non-SSE JSON so both stream paths take their documented fallback and return
		// cleanly. The body shape does not matter here; the PATH is the measurement.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c, err := New(srv.URL, "tlv_test_key")
	if err != nil {
		t.Fatalf("New(%q): %v", srv.URL, err)
	}
	chunks := make(chan StreamChunk, StreamChunkBuffer)
	go func() {
		_ = c.CompleteAuto(context.Background(), []Message{{Role: "user", Content: "hi"}},
			modelID, "parity", "ws", "issue", chunks)
	}()
	for range chunks { //nolint:revive // draining is the contract
	}
	if got == "" {
		t.Fatalf("model %q: no request reached the server — the dispatch was not measured", modelID)
	}
	return got
}

// TestEveryOfferedModelIsDispatchableToItsOwnProvider is the rule the product needs: a model the
// catalogue advertises with a Provider must be sent to that provider's route.
func TestEveryOfferedModelIsDispatchableToItsOwnProvider(t *testing.T) {
	for _, c := range loadRoutingTable(t) {
		if !c.Dispatchable {
			continue
		}
		if strings.ToLower(c.CatalogProvider) != c.RoutedProvider {
			t.Errorf("%s: catalogue says provider %q but the client routes it to %q — mark dispatchable=false and say why, or fix the routing",
				c.ID, c.CatalogProvider, c.RoutedProvider)
		}
	}
}

// TestRoutedEndpointIsWhatTheClientActuallyPosts pins the measured dispatch. This is the rule that
// reds the day the routing is decided either way, so the decision cannot land silently.
func TestRoutedEndpointIsWhatTheClientActuallyPosts(t *testing.T) {
	for _, c := range loadRoutingTable(t) {
		if got := dispatchPathFor(t, c.ID); got != c.RoutedEndpoint {
			t.Errorf("%s: CompleteAuto POSTed to %q, table says %q", c.ID, got, c.RoutedEndpoint)
		}
	}
}

// THE TABLE NAMED ONE DISPATCHER AND THIS REPOSITORY HAS TWO.
//
// Everything above measures CompleteAuto — the STREAMING path — and the table's note used to call
// `routedEndpoint` "the gateway endpoint each client actually POSTs it to", singular. There is a
// second dispatcher and it classifies by nothing at all: Complete and CompleteWithUsage build an
// Anthropic body and POST it to a LITERAL `/v1/proxy/anthropic/v1/messages`, for every model.
//
// ⚠ THAT PATH IS NOT A CORNER. Sixteen production call sites in this module use it — cmd/agent
// (ask, plan, commit ×2, review, test, chat), internal/shell/generator.go ×3,
// internal/projectctx/loader.go, internal/mcp/server.go ×4, cmd/agent/iterative.go — and
// cmd/agent's streamWithFallback FALLS BACK TO IT when a stream errors before its first chunk. So
// `talyvor ask --model gpt-4o` streams to OpenAI on a good day and blocks to Anthropic on a bad one,
// from one command, with no message to the user either way.
//
// ⚠ SO `dispatchable: true` FOR gpt-4o WAS AN OVER-CLAIM BY THIS FILE'S OWN GUARD, not a wrong
// value: the rule "is a model the product offers one it can SEND" was answered by measuring one of
// the two ways the product sends it. It is the same shape talyvor-docs found in
// check-test-manifest.mjs, which counted one of that repository's two vitest projects and reported a
// clean tree.
//
// ⚠ W4.19 HANDED THIS ON AS UNVERIFIED, IN THOSE WORDS: "I measured the literal and the call sites
// but did NOT drive it end to end, so it is reported as unverified-by-execution rather than
// claimed." It is driven now, in both ports, and the literal was right.
//
// blockingDispatchPathFor drives the REAL Complete against a live server and returns the path it
// POSTed to — measured for the same reason dispatchPathFor is: the endpoint is a literal inside the
// function, so nothing short of driving the call can see it move.
func blockingDispatchPathFor(t *testing.T, modelID string) string {
	t.Helper()
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		// An Anthropic-shaped reply so the client decodes and returns cleanly. The PATH is the
		// measurement; the body shape is not asserted here.
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	c, err := New(srv.URL, "tlv_test_key")
	if err != nil {
		t.Fatalf("New(%q): %v", srv.URL, err)
	}
	if _, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}},
		modelID, "parity", "ws", "issue"); err != nil {
		t.Fatalf("Complete(%q): %v", modelID, err)
	}
	if got == "" {
		t.Fatalf("model %q: no request reached the server — the blocking dispatch was not measured", modelID)
	}
	return got
}

// TestBlockingEndpointIsWhatTheClientActuallyPosts is the second path's version of the rule above,
// and it is what reds the day the non-streaming dispatch is routed either way.
func TestBlockingEndpointIsWhatTheClientActuallyPosts(t *testing.T) {
	for _, c := range loadRoutingTable(t) {
		if c.BlockingEndpoint == "" {
			t.Errorf("%s: no blockingEndpoint in the table — the non-streaming dispatch is unmeasured for this model, which is how it went unseen for a year", c.ID)
			continue
		}
		if got := blockingDispatchPathFor(t, c.ID); got != c.BlockingEndpoint {
			t.Errorf("%s: Complete POSTed to %q, table says %q", c.ID, got, c.BlockingEndpoint)
		}
	}
}

// TestBlockingDispatchableAgreesWithTheCatalogue mirrors the streaming rule: a model the catalogue
// advertises with a Provider must reach that provider's route on the path being claimed.
func TestBlockingDispatchableAgreesWithTheCatalogue(t *testing.T) {
	for _, c := range loadRoutingTable(t) {
		if !c.BlockingDispatchable {
			continue
		}
		if strings.ToLower(c.CatalogProvider) != c.BlockingProvider {
			t.Errorf("%s: catalogue says provider %q but the non-streaming path routes it to %q — mark blockingDispatchable=false and say why, or fix the routing",
				c.ID, c.CatalogProvider, c.BlockingProvider)
		}
	}
}

// TestABlockingDefectMayBePinnedButNotSilently — the same rule the streaming half already applies to
// `why`, on the field that records the second path. A pinned defect that stops saying what it is
// becomes an accepted value.
func TestABlockingDefectMayBePinnedButNotSilently(t *testing.T) {
	for _, c := range loadRoutingTable(t) {
		if c.BlockingDispatchable {
			if strings.TrimSpace(c.WhyBlocking) != "" {
				t.Errorf("%s: blockingDispatchable=true but carries a whyBlocking — that field records why a model CANNOT reach its provider on the non-streaming path", c.ID)
			}
			continue
		}
		if strings.TrimSpace(c.WhyBlocking) == "" {
			t.Errorf("%s: blockingDispatchable=false with no whyBlocking — a pinned defect must say what it is", c.ID)
		}
	}
}

// TestTheTwoDispatchPathsDoNotDisagreeSilently is the rule that exists because of what this file
// missed. Two dispatchers sending the same model to two providers is a defect whatever the reason,
// so a case whose endpoints differ may not ALSO claim the non-streaming path is fine.
func TestTheTwoDispatchPathsDoNotDisagreeSilently(t *testing.T) {
	for _, c := range loadRoutingTable(t) {
		if c.RoutedEndpoint == c.BlockingEndpoint {
			continue
		}
		if c.BlockingDispatchable {
			t.Errorf("%s: the streaming path POSTs to %q and the non-streaming path to %q, and the table calls the second one dispatchable. One model, two providers, chosen by whether the call happened to stream — say which is wrong",
				c.ID, c.RoutedEndpoint, c.BlockingEndpoint)
		}
	}
}

// TestTablePopulationEqualsTheShippedCatalogue keeps the table honest in both directions — a model
// added to the catalogue with no case, or a case for a model no longer offered.
func TestTablePopulationEqualsTheShippedCatalogue(t *testing.T) {
	cases := loadRoutingTable(t)
	inTable := map[string]routingCase{}
	for _, c := range cases {
		inTable[c.ID] = c
	}
	inCatalogue := map[string]model.ModelProfile{}
	for _, m := range model.KnownModels {
		inCatalogue[m.ID] = m
	}
	for id, m := range inCatalogue {
		c, ok := inTable[id]
		if !ok {
			t.Errorf("%s is offered by model.KnownModels and has no case — widen testdata/model-routing.json AND the TypeScript port, or drop the model", id)
			continue
		}
		if c.CatalogProvider != m.Provider {
			t.Errorf("%s: catalogue declares provider %q, table says %q", id, m.Provider, c.CatalogProvider)
		}
	}
	for id := range inTable {
		if _, ok := inCatalogue[id]; !ok {
			t.Errorf("%s has a case and is not in model.KnownModels — drop the case, or restore the model", id)
		}
	}
}

// TestUndispatchableCasesCarryTheirReason stops the exception set from becoming a place defects go
// quietly. An entry may be pinned as not-dispatchable; it may not be pinned silently.
func TestUndispatchableCasesCarryTheirReason(t *testing.T) {
	for _, c := range loadRoutingTable(t) {
		if c.Dispatchable {
			if strings.TrimSpace(c.Why) != "" {
				t.Errorf("%s: dispatchable=true but carries a `why` — the field records why a model CANNOT reach its provider", c.ID)
			}
			continue
		}
		if strings.TrimSpace(c.Why) == "" {
			t.Errorf("%s: dispatchable=false with no `why` — a pinned defect must say what it is", c.ID)
		}
	}
}
