package lens

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// THE CENSUS: EVERY CALL THIS CLIENT MAKES, PINNED BY METHOD *AND* PATH.
//
// ⚠ MEASURED, NOT ASSUMED. A method-mutation census over the agent (change one call's HTTP method,
// run `go test ./...`) found SIX sites in this package whose method could be changed with the
// whole suite still green: Complete/CompleteWithUsage, ReportMechanicalVerdict, CompleteAuto's
// non-stream branch, VerifyCredential, Status, and Embed. A path mutation on
// ReportMechanicalVerdict was likewise invisible. The fakes in this repository do not look at the
// method, and several do not look at the path either.
//
// ⚠ THE EXPECTED VALUES COME FROM talyvor-lens's ROUTER, read at 171dbdc (read-only; that repo was
// held by another tab):
//
//	authed.With(proxyScope).Post("/v1/proxy/anthropic/*", …)   cmd/lens/main.go:2156
//	authed.With(proxyScope).Post("/v1/proxy/openai/*",    …)   cmd/lens/main.go:2155
//	authed.Post("/v1/output-verdicts/{output_id}/mechanical")  cmd/lens/main.go:2167
//	authed.Get("/v1/auth/me")
//
// All matched at 8eee6f0, so this closes a coverage gap rather than fixing a break. CI here checks
// out only talyvor-code: this pins what the CLIENT sends and cannot notice the server moving.
//
// ⚠ /healthz IS DELIBERATELY IN THE TABLE DESPITE BEING UNAUTHENTICATED. W4.22 found all three
// clients reporting "✅ Connected" off this probe WITHOUT ever sending the key; the repair was
// VerifyCredential against /v1/auth/me. Pinning both here keeps the two apart — a Status() that
// silently started asking /v1/auth/me, or the reverse, is exactly the confusion that produced that
// defect.

type recorded struct{ method, path string }

// strictFake answers only the exact (method, path) pairs it is given and 404s everything else —
// deliberately LESS forgiving than a real server.
func strictFake(t *testing.T, rec *[]recorded, allow map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*rec = append(*rec, recorded{r.Method, r.URL.Path})
		body, ok := allow[r.Method+" "+r.URL.Path]
		if !ok {
			http.Error(w, "no such route", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func TestWireContract_MethodAndPath(t *testing.T) {
	const anthropic = "/v1/proxy/anthropic/v1/messages"
	const openaiEmbed = "/v1/proxy/openai/v1/embeddings"
	msgBody := `{"content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1,"output_tokens":1}}`

	cases := []struct {
		name   string
		allow  map[string]string
		call   func(c *Client) error
		expect recorded
	}{
		{
			name:  "Complete posts to the anthropic proxy",
			allow: map[string]string{"POST " + anthropic: msgBody},
			call: func(c *Client) error {
				_, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}},
					"claude-sonnet-4-5", "chat", "ws-1", "ENG-42")
				return err
			},
			expect: recorded{"POST", anthropic},
		},
		{
			name:  "ReportMechanicalVerdict posts to the verdict route",
			allow: map[string]string{"POST /v1/output-verdicts/out-1/mechanical": `{}`},
			call: func(c *Client) error {
				return c.ReportMechanicalVerdict(context.Background(), "out-1", "pass", 0, "go test", "")
			},
			expect: recorded{"POST", "/v1/output-verdicts/out-1/mechanical"},
		},
		{
			name:  "ReportAttribution posts to the attribution route",
			allow: map[string]string{"POST /v1/outputs/out-1/attribution": `{}`},
			call: func(c *Client) error {
				return c.ReportAttribution(context.Background(), "out-1", "pr", "org/repo#1")
			},
			expect: recorded{"POST", "/v1/outputs/out-1/attribution"},
		},
		{
			name:  "Embed posts to the openai embeddings proxy",
			allow: map[string]string{"POST " + openaiEmbed: `{"data":[{"embedding":[0.1]}]}`},
			call: func(c *Client) error {
				_, err := c.Embed(context.Background(), []string{"x"}, "text-embedding-3-small",
					"index", "ws-1", "")
				return err
			},
			expect: recorded{"POST", openaiEmbed},
		},
		{
			name:  "VerifyCredential GETs /v1/auth/me — the AUTHENTICATED probe",
			allow: map[string]string{"GET /v1/auth/me": `{}`},
			call: func(c *Client) error {
				if v, _ := c.VerifyCredential(context.Background()); v == CredentialUnknown {
					t.Errorf("VerifyCredential returned Unknown against a 200")
				}
				return nil
			},
			expect: recorded{"GET", "/v1/auth/me"},
		},
		{
			name:  "Status GETs /healthz — the UNAUTHENTICATED reachability probe",
			allow: map[string]string{"GET /healthz": `{}`},
			call: func(c *Client) error {
				_, err := c.Status(context.Background())
				return err
			},
			expect: recorded{"GET", "/healthz"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rec []recorded
			srv := strictFake(t, &rec, tc.allow)
			defer srv.Close()
			if err := tc.call(mustNew(t, srv.URL, "tlv_k")); err != nil {
				t.Fatalf("call: %v", err)
			}
			if len(rec) != 1 {
				t.Fatalf("requests = %+v, want exactly 1", rec)
			}
			if rec[0] != tc.expect {
				t.Errorf("request = %+v, want %+v", rec[0], tc.expect)
			}
		})
	}
}

// ⚠ THE STREAMING PAIR NEEDS ITS OWN TEST, AND THE METHOD IS WHY. Both stream entry points share
// openStream (client.go:441), so a PATH mutation on either reds an existing test — but the METHOD
// lives in the shared helper and NOTHING checked it: `http.MethodPost` there could become PUT and
// the whole agent suite stayed green. The two cases below cover the shared helper through both of
// its callers, so the pair cannot diverge silently either.
//
// The stream body is deliberately minimal: this asserts WHAT WAS REQUESTED, not what was parsed,
// so it stays true if the SSE dialect changes. A transport error is tolerated for the same reason
// — the request has already been recorded by then.
func TestWireContract_StreamingMethodAndPath(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		call       func(c *Client, chunks chan StreamChunk) error
	}{
		{
			name: "CompleteStream posts to the anthropic proxy",
			path: "/v1/proxy/anthropic/v1/messages",
			call: func(c *Client, ch chan StreamChunk) error {
				return c.CompleteStream(context.Background(), []Message{{Role: "user", Content: "hi"}},
					"claude-sonnet-4-5", "chat", "ws-1", "ENG-42", ch)
			},
		},
		{
			name: "CompleteStreamOpenAI posts to the openai proxy",
			path: "/v1/proxy/openai/v1/chat/completions",
			call: func(c *Client, ch chan StreamChunk) error {
				return c.CompleteStreamOpenAI(context.Background(), []Message{{Role: "user", Content: "hi"}},
					"gpt-4o", "chat", "ws-1", "ENG-42", ch)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rec []recorded
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rec = append(rec, recorded{r.Method, r.URL.Path})
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			ch := make(chan StreamChunk, StreamChunkBuffer)
			done := make(chan struct{})
			go func() {
				for range ch {
				}
				close(done)
			}()
			_ = tc.call(mustNew(t, srv.URL, "tlv_k"), ch)
			<-done

			if len(rec) != 1 {
				t.Fatalf("requests = %+v, want exactly 1", rec)
			}
			if want := (recorded{"POST", tc.path}); rec[0] != want {
				t.Errorf("request = %+v, want %+v", rec[0], want)
			}
		})
	}
}
