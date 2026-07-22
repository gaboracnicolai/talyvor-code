package lens

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIsConfigured(t *testing.T) {
	if New("", "").IsConfigured() {
		t.Fatal("expected unconfigured for empty url+key")
	}
	if !New("http://lens", "tlv_k").IsConfigured() {
		t.Fatal("expected configured for url+key")
	}
}

func TestComplete_SendsAttributionHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hi"}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "tlv_k")
	out, err := c.Complete(context.Background(),
		[]Message{{Role: "user", Content: "say hi"}},
		"claude-haiku-4-5", "chat", "ws-1", "ENG-42")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if out != "hi" {
		t.Fatalf("body = %q, want hi", out)
	}
	if got.Get("Authorization") != "Bearer tlv_k" {
		t.Fatalf("missing bearer: %q", got.Get("Authorization"))
	}
	if got.Get("X-Talyvor-Feature") != "code-chat" {
		t.Fatalf("feature header wrong: %q", got.Get("X-Talyvor-Feature"))
	}
	if got.Get("X-Talyvor-Workspace") != "ws-1" {
		t.Fatalf("workspace header wrong: %q", got.Get("X-Talyvor-Workspace"))
	}
	if got.Get("X-Talyvor-Issue") != "ENG-42" {
		t.Fatalf("issue header wrong: %q", got.Get("X-Talyvor-Issue"))
	}
}

func TestComplete_ErrorsWhenUnconfigured(t *testing.T) {
	_, err := New("", "").Complete(context.Background(), nil, "m", "f", "w", "i")
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected unconfigured error, got %v", err)
	}
}

// ─── streaming ─────────────────────────────────────

// sseAnthropic writes a fake Anthropic SSE stream to the
// httptest response. Each line is a single `data:` frame
// terminated by a blank line per the SSE spec.
func sseAnthropic(events []string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, ev := range events {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", ev)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func TestCompleteStream_EmitsAnthropicDeltas(t *testing.T) {
	srv := httptest.NewServer(sseAnthropic([]string{
		`{"type":"message_start","message":{"usage":{"input_tokens":120}}}`,
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello "}}`,
		`{"type":"content_block_delta","delta":{"type":"text_delta","text":"world"}}`,
		`{"type":"message_delta","usage":{"output_tokens":4}}`,
		`{"type":"message_stop"}`,
		`[DONE]`,
	}))
	defer srv.Close()
	c := New(srv.URL, "tlv_k")
	chunks := make(chan StreamChunk, StreamChunkBuffer)
	go func() {
		_ = c.CompleteStream(context.Background(),
			[]Message{{Role: "user", Content: "hi"}},
			"claude-sonnet-4-6", "chat", "ws-1", "ENG-42",
			chunks)
	}()
	var text strings.Builder
	var final StreamChunk
	for ch := range chunks {
		if ch.Error != nil {
			t.Fatalf("stream error: %v", ch.Error)
		}
		if ch.Done {
			final = ch
			continue
		}
		text.WriteString(ch.Text)
	}
	if text.String() != "Hello world" {
		t.Fatalf("text = %q, want 'Hello world'", text.String())
	}
	if !final.Done {
		t.Fatal("missing terminal Done chunk")
	}
	if final.InputTokens != 120 || final.OutputTokens != 4 {
		t.Fatalf("usage wrong: %+v", final)
	}
}

func TestCompleteStream_HandlesDoneOnly(t *testing.T) {
	srv := httptest.NewServer(sseAnthropic([]string{`[DONE]`}))
	defer srv.Close()
	c := New(srv.URL, "tlv_k")
	chunks := make(chan StreamChunk, StreamChunkBuffer)
	go func() {
		_ = c.CompleteStream(context.Background(),
			[]Message{{Role: "user", Content: "x"}},
			"claude-haiku-4-5", "chat", "ws-1", "i",
			chunks)
	}()
	got := []StreamChunk{}
	for ch := range chunks {
		got = append(got, ch)
	}
	if len(got) != 1 || !got[0].Done {
		t.Fatalf("expected single Done chunk, got %+v", got)
	}
}

func TestCompleteStream_BubblesErrorEvent(t *testing.T) {
	srv := httptest.NewServer(sseAnthropic([]string{
		`{"type":"error","error":{"message":"upstream exploded"}}`,
		`[DONE]`,
	}))
	defer srv.Close()
	c := New(srv.URL, "tlv_k")
	chunks := make(chan StreamChunk, StreamChunkBuffer)
	go func() {
		_ = c.CompleteStream(context.Background(),
			[]Message{{Role: "user", Content: "x"}},
			"claude-haiku-4-5", "chat", "ws-1", "i",
			chunks)
	}()
	var sawErr bool
	for ch := range chunks {
		if ch.Error != nil {
			if !strings.Contains(ch.Error.Error(), "upstream exploded") {
				t.Fatalf("wrong error: %v", ch.Error)
			}
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatal("expected error chunk on the channel")
	}
}

func TestCompleteStream_ContextCancelStopsStream(t *testing.T) {
	// Server holds the connection open with one delta every 50ms.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 50; i++ {
			_, _ = fmt.Fprintf(w,
				"data: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"tick \"}}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(50 * time.Millisecond)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "tlv_k")
	ctx, cancel := context.WithCancel(context.Background())
	chunks := make(chan StreamChunk, StreamChunkBuffer)
	go func() {
		_ = c.CompleteStream(ctx,
			[]Message{{Role: "user", Content: "x"}},
			"claude-haiku-4-5", "chat", "ws-1", "i",
			chunks)
	}()
	// Drain a couple of chunks then cancel.
	go func() {
		count := 0
		for range chunks {
			count++
			if count == 2 {
				cancel()
			}
		}
	}()
	// Wait until the channel actually closes; the test fails if it
	// doesn't within a reasonable budget.
	done := make(chan struct{})
	go func() {
		for range chunks {
		}
		close(done)
	}()
	select {
	case <-done:
		// channel closed — stream stopped
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not stop after context cancel")
	}
}

// sseOpenAI writes a fake OpenAI streaming response.
func sseOpenAI(events []string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for _, ev := range events {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", ev)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func TestCompleteStreamOpenAI_EmitsDeltas(t *testing.T) {
	srv := httptest.NewServer(sseOpenAI([]string{
		`{"choices":[{"delta":{"content":"Hello "}}]}`,
		`{"choices":[{"delta":{"content":"there"}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":50,"completion_tokens":2}}`,
		`[DONE]`,
	}))
	defer srv.Close()
	c := New(srv.URL, "tlv_k")
	chunks := make(chan StreamChunk, StreamChunkBuffer)
	go func() {
		_ = c.CompleteStreamOpenAI(context.Background(),
			[]Message{{Role: "user", Content: "hi"}},
			"gpt-4o", "chat", "ws-1", "i", chunks)
	}()
	var text strings.Builder
	var final StreamChunk
	for ch := range chunks {
		if ch.Done {
			final = ch
			continue
		}
		text.WriteString(ch.Text)
	}
	if text.String() != "Hello there" {
		t.Fatalf("text = %q", text.String())
	}
	if final.OutputTokens != 2 {
		t.Fatalf("usage = %+v", final)
	}
}

func TestCompleteAuto_RoutesOpenAIVsAnthropic(t *testing.T) {
	var hitAnthropic, hitOpenAI bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "openai") {
			hitOpenAI = true
		}
		if strings.Contains(r.URL.Path, "anthropic") {
			hitAnthropic = true
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	c := New(srv.URL, "tlv_k")

	for _, m := range []string{"gpt-4o", "gpt-4o-mini", "o1-mini"} {
		chunks := make(chan StreamChunk, StreamChunkBuffer)
		go func() {
			_ = c.CompleteAuto(context.Background(), nil, m, "chat", "ws-1", "i", chunks)
		}()
		for range chunks {
		}
	}
	if !hitOpenAI {
		t.Fatal("expected at least one OpenAI route hit")
	}
	hitAnthropic = false
	for _, m := range []string{"claude-haiku-4-5", "claude-sonnet-4-6", "mistral-large"} {
		chunks := make(chan StreamChunk, StreamChunkBuffer)
		go func() {
			_ = c.CompleteAuto(context.Background(), nil, m, "chat", "ws-1", "i", chunks)
		}()
		for range chunks {
		}
	}
	if !hitAnthropic {
		t.Fatal("expected at least one Anthropic route hit")
	}
}

func TestIsOpenAIModel(t *testing.T) {
	for _, m := range []string{"gpt-4o", "gpt-4o-mini", "o1", "o3-mini"} {
		if !isOpenAIModel(m) {
			t.Errorf("expected OpenAI: %s", m)
		}
	}
	for _, m := range []string{"claude-haiku-4-5", "mistral-large", "llama-3.1"} {
		if isOpenAIModel(m) {
			t.Errorf("expected non-OpenAI: %s", m)
		}
	}
}

// TestComplete_BubblesLensErrors covers the upstream-failure
// paths. 401 and 5xx both surface as a lens:-prefixed error so
// the CLI's runAsk can show a clean message to the user.
func TestComplete_BubblesLensErrors(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusInternalServerError} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "denied", code)
		}))
		c := New(srv.URL, "tlv_k")
		_, err := c.Complete(context.Background(), nil, "m", "f", "w", "i")
		if err == nil || !strings.Contains(err.Error(), "lens:") {
			t.Errorf("status %d: expected lens error, got %v", code, err)
		}
		srv.Close()
	}
}

// TestEstimateCostUSD pins the estimate table to the Lens catalog rates
// (internal/catalog/seed.go is the source of truth; Lens reconciles
// authoritatively). The haiku bucket is the DEFAULT case, so it also
// prices unknown models — it must carry the real claude-haiku-4-5 rates
// (0.80/4.00 per 1M), not the retired 0.25/1.25 pair that under-reported
// estimates ~3x.
func TestEstimateCostUSD(t *testing.T) {
	cases := []struct {
		model string
		want  float64
	}{
		{"claude-haiku-4-5", 0.80 + 4.00}, // 1M in + 1M out at catalog rates
		{"claude-sonnet-4-6", 3.00 + 15.00},
		{"totally-unknown", 0.80 + 4.00}, // unknown falls back to the haiku bucket
	}
	for _, tc := range cases {
		got := EstimateCostUSD(tc.model, 1_000_000, 1_000_000)
		if diff := got - tc.want; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("EstimateCostUSD(%s) = %v, want %v", tc.model, got, tc.want)
		}
	}
}
