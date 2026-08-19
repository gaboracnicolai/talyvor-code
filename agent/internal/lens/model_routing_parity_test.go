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
