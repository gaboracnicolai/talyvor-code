// Package lens is the Go Lens client used by the CLI agent. Every
// Complete call carries the X-Talyvor-Issue header so the active
// issue gets credited with the spend.
package lens

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Client struct {
	url        string
	apiKey     string
	httpClient *http.Client
}

func New(url, apiKey string) *Client {
	return &Client{
		url:        strings.TrimRight(url, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) IsConfigured() bool {
	return c.url != "" && c.apiKey != ""
}

// Complete proxies a chat completion through Lens. `feature` is
// prefixed with `code-` server-side so Lens dashboards bucket
// the spend per IDE affordance.
func (c *Client) Complete(ctx context.Context, messages []Message, model, feature, workspaceID, issueID string) (string, error) {
	out, err := c.CompleteWithUsage(ctx, messages, model, feature, workspaceID, issueID)
	if err != nil {
		return "", err
	}
	return out.Text, nil
}

// Usage tracks the token counts and estimated USD cost for a
// single Lens round-trip. MCP clients use this to report cost
// attribution back to their parent agent.
type Usage struct {
	Text         string
	InputTokens  int
	OutputTokens int
	CostUSD      float64
	// OutputID is the gateway-bound output identity Lens returns in the X-Talyvor-Output-Id response header
	// (K4 code loop). Empty when the gateway doesn't set it (verifier off, or true streaming). It lets a
	// build/test verdict be paired 1:1 with the specific generation that produced the code.
	OutputID string
}

// CompleteWithUsage is the richer sibling to Complete — same
// inputs, but returns input/output token counts and an estimated
// dollar cost. Estimation uses a small hard-coded model price
// table; the actual reconciled cost still lives in Lens analytics.
func (c *Client) CompleteWithUsage(ctx context.Context, messages []Message, model, feature, workspaceID, issueID string) (*Usage, error) {
	if !c.IsConfigured() {
		return nil, errors.New("lens: not configured")
	}
	body := map[string]any{
		"model":      model,
		"max_tokens": 2048,
		"messages":   messages,
	}
	enc, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.url+"/v1/proxy/anthropic/v1/messages", bytes.NewReader(enc))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("X-Talyvor-Feature", "code-"+feature)
	req.Header.Set("X-Talyvor-Workspace", workspaceID)
	req.Header.Set("X-Talyvor-Issue", issueID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("lens: %s", resp.Status)
	}
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return &Usage{
		Text:         b.String(),
		InputTokens:  out.Usage.InputTokens,
		OutputTokens: out.Usage.OutputTokens,
		CostUSD:      EstimateCostUSD(model, out.Usage.InputTokens, out.Usage.OutputTokens),
		OutputID:     resp.Header.Get("X-Talyvor-Output-Id"),
	}, nil
}

// ReportMechanicalVerdict self-reports a mechanical build/test verdict for an output this workspace
// produced (K4 code loop). BEST-EFFORT: it must NEVER break the caller's build — every failure is returned
// for the caller to swallow. It POSTs to the ownership-bound endpoint; the gateway verifies the caller owns
// output_id and appends the verdict (first-report-wins). A no-op when output_id is empty or not configured.
func (c *Client) ReportMechanicalVerdict(ctx context.Context, outputID, verdict string, exitCode int, tool, reason string) error {
	if !c.IsConfigured() || outputID == "" {
		return nil
	}
	enc, err := json.Marshal(map[string]any{
		"verdict":   verdict,
		"exit_code": exitCode,
		"tool":      tool,
		"reason":    reason,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.url+"/v1/output-verdicts/"+outputID+"/mechanical", bytes.NewReader(enc))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("lens: report verdict: %s", resp.Status)
	}
	return nil
}

// ErrAttributionConflict signals a 409 from the attribution endpoint: the output is
// already attributed to a DIFFERENT target_ref. Non-fatal + success-equivalent (the
// caller must NOT fail the PR), but a possible mis-attribution worth logging.
var ErrAttributionConflict = errors.New("lens: output already attributed to a different target")

// ReportAttribution attributes an output to a downstream target (a landed PR/spec) via
// POST /v1/outputs/{output_id}/attribution with {target_kind,target_ref}. Client-side,
// no authority: Lens owns the gate (ownership-bound, append-only first-wins). A 409
// (already attributed) is SUCCESS-EQUIVALENT (nil) — first-report-wins; other >=400 are
// errors. A no-op when unconfigured or output_id is empty. target_ref is opaque to Lens.
func (c *Client) ReportAttribution(ctx context.Context, outputID, targetKind, targetRef string) error {
	if !c.IsConfigured() || outputID == "" {
		return nil
	}
	enc, err := json.Marshal(map[string]any{
		"target_kind": targetKind,
		"target_ref":  targetRef,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.url+"/v1/outputs/"+outputID+"/attribution", bytes.NewReader(enc))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		// The output was already attributed to a DIFFERENT target_ref (append-only
		// first-wins). Non-fatal + success-equivalent, but a possible mis-attribution —
		// surfaced via a sentinel the caller LOGS. (An identical re-post returns 200
		// recorded:false and stays silent.)
		return ErrAttributionConflict
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("lens: report attribution: %s", resp.Status)
	}
	return nil
}

// EstimateCostUSD prices a call from per-million-token rates.
// Keep the table tight; Lens does authoritative reconciliation.
func EstimateCostUSD(model string, inputTokens, outputTokens int) float64 {
	var inRate, outRate float64
	switch model {
	case "claude-sonnet-4-6":
		inRate, outRate = 3.0, 15.0
	case "claude-opus-4-7":
		inRate, outRate = 15.0, 75.0
	default:
		// claude-haiku-4-6 and unknown models fall back to haiku rates.
		inRate, outRate = 0.25, 1.25
	}
	return (float64(inputTokens)*inRate + float64(outputTokens)*outRate) / 1_000_000
}

// ─── Streaming ─────────────────────────────────────

// StreamChunk is one event from a streaming completion. Exactly
// one of Text/Done/Error is set per chunk. The terminal chunk
// carries Done=true plus the accumulated token usage so callers
// can attribute cost without a second round-trip.
type StreamChunk struct {
	Text         string
	Done         bool
	InputTokens  int
	OutputTokens int
	Error        error
}

// StreamChunkBuffer is the channel buffer size every streaming
// caller should use. Picked so a slow consumer can fall behind
// by a sentence or two without backpressuring the producer.
const StreamChunkBuffer = 100

// CompleteStream proxies a streaming completion in the Anthropic
// `messages` SSE format. The supplied channel receives one chunk
// per content delta; the final chunk has Done=true with the
// usage counts. The channel is always closed before the function
// returns, even on error.
//
// Cancellation: cancelling ctx stops the HTTP read loop and the
// channel is closed. Callers must drain the channel to release
// the goroutine.
//
// Fallback: if the Lens response isn't actually SSE (some
// proxies ignore stream:true and return JSON), we read the body
// as a regular Anthropic response and emit it as a single text
// chunk + Done. Saves callers from a second round-trip.
func (c *Client) CompleteStream(ctx context.Context, messages []Message, model, feature, workspaceID, issueID string, chunks chan<- StreamChunk) error {
	defer close(chunks)
	resp, err := c.openStream(ctx, c.url+"/v1/proxy/anthropic/v1/messages",
		anthropicStreamBody(model, messages), model, feature, workspaceID, issueID)
	if err != nil {
		sendChunk(chunks, StreamChunk{Error: err})
		return err
	}
	defer resp.Body.Close()
	if !isEventStream(resp.Header.Get("Content-Type")) {
		return emitAnthropicJSON(resp.Body, chunks)
	}
	return readAnthropicSSE(ctx, resp.Body, chunks)
}

// CompleteStreamOpenAI mirrors CompleteStream but for the
// OpenAI `chat/completions` SSE format. Same channel contract,
// same non-SSE fallback.
func (c *Client) CompleteStreamOpenAI(ctx context.Context, messages []Message, model, feature, workspaceID, issueID string, chunks chan<- StreamChunk) error {
	defer close(chunks)
	resp, err := c.openStream(ctx, c.url+"/v1/proxy/openai/v1/chat/completions",
		openAIStreamBody(model, messages), model, feature, workspaceID, issueID)
	if err != nil {
		sendChunk(chunks, StreamChunk{Error: err})
		return err
	}
	defer resp.Body.Close()
	if !isEventStream(resp.Header.Get("Content-Type")) {
		return emitOpenAIJSON(resp.Body, chunks)
	}
	return readOpenAISSE(ctx, resp.Body, chunks)
}

// isEventStream returns true when the Content-Type indicates a
// real SSE response. Default to SSE when the header is missing
// so well-behaved servers don't get treated as JSON.
func isEventStream(contentType string) bool {
	if contentType == "" {
		return true
	}
	return strings.HasPrefix(strings.ToLower(contentType), "text/event-stream")
}

// emitAnthropicJSON reads a non-streaming Anthropic response and
// emits the concatenated text as one chunk plus a Done chunk
// with the usage counts.
func emitAnthropicJSON(body interface{ Read([]byte) (int, error) }, chunks chan<- StreamChunk) error {
	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(body).Decode(&out); err != nil {
		sendChunk(chunks, StreamChunk{Error: err})
		return err
	}
	var b strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	if b.Len() > 0 {
		sendChunk(chunks, StreamChunk{Text: b.String()})
	}
	sendChunk(chunks, StreamChunk{
		Done:         true,
		InputTokens:  out.Usage.InputTokens,
		OutputTokens: out.Usage.OutputTokens,
	})
	return nil
}

// emitOpenAIJSON reads a non-streaming OpenAI response and emits
// the first choice's message content as one chunk + Done.
func emitOpenAIJSON(body interface{ Read([]byte) (int, error) }, chunks chan<- StreamChunk) error {
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(body).Decode(&out); err != nil {
		sendChunk(chunks, StreamChunk{Error: err})
		return err
	}
	text := ""
	if len(out.Choices) > 0 {
		text = out.Choices[0].Message.Content
	}
	if text != "" {
		sendChunk(chunks, StreamChunk{Text: text})
	}
	sendChunk(chunks, StreamChunk{
		Done:         true,
		InputTokens:  out.Usage.PromptTokens,
		OutputTokens: out.Usage.CompletionTokens,
	})
	return nil
}

// CompleteAuto routes to CompleteStream or CompleteStreamOpenAI
// based on the model identifier. Anything starting with `gpt-`
// or `o1`/`o3` is OpenAI; everything else (claude, mistral, …)
// goes through the Anthropic path which Lens maps server-side.
func (c *Client) CompleteAuto(ctx context.Context, messages []Message, model, feature, workspaceID, issueID string, chunks chan<- StreamChunk) error {
	if isOpenAIModel(model) {
		return c.CompleteStreamOpenAI(ctx, messages, model, feature, workspaceID, issueID, chunks)
	}
	return c.CompleteStream(ctx, messages, model, feature, workspaceID, issueID, chunks)
}

// openStream POSTs the supplied JSON body with the canonical
// Talyvor headers + an Accept: text/event-stream hint. The
// caller owns the response body lifecycle.
func (c *Client) openStream(ctx context.Context, endpoint string, body any, model, feature, workspaceID, issueID string) (*http.Response, error) {
	if !c.IsConfigured() {
		return nil, errors.New("lens: not configured")
	}
	enc, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(enc))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("X-Talyvor-Feature", "code-"+feature)
	req.Header.Set("X-Talyvor-Workspace", workspaceID)
	req.Header.Set("X-Talyvor-Issue", issueID)
	// Streaming uses a fresh client because the parent's 30s
	// timeout would kill long replies. We rely on ctx + the SSE
	// loop's read calls for cancellation.
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, fmt.Errorf("lens: %s", resp.Status)
	}
	_ = model
	return resp, nil
}

func anthropicStreamBody(model string, messages []Message) map[string]any {
	return map[string]any{
		"model":      model,
		"max_tokens": 2048,
		"messages":   messages,
		"stream":     true,
	}
}

func openAIStreamBody(model string, messages []Message) map[string]any {
	// OpenAI uses the same {role, content} shape — no
	// translation needed beyond the stream flag.
	return map[string]any{
		"model":    model,
		"messages": messages,
		"stream":   true,
	}
}

// readAnthropicSSE parses the Anthropic streaming format. Events
// of interest:
//   - content_block_delta → emit chunk.Text
//   - message_delta       → captures usage for the final chunk
//   - message_stop / [DONE] → emit Done
//   - error               → emit Error
func readAnthropicSSE(ctx context.Context, r interface{ Read([]byte) (int, error) }, chunks chan<- StreamChunk) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	usage := StreamChunk{Done: true}
	var streamErr error

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			break
		}
		var event struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
			Usage struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Message struct {
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			// Treat parse errors as transient — skip the event.
			continue
		}
		switch event.Type {
		case "content_block_delta":
			if event.Delta.Text != "" {
				sendChunk(chunks, StreamChunk{Text: event.Delta.Text})
			}
		case "message_start":
			if event.Message.Usage.InputTokens > 0 {
				usage.InputTokens = event.Message.Usage.InputTokens
			}
		case "message_delta":
			if event.Usage.OutputTokens > 0 {
				usage.OutputTokens = event.Usage.OutputTokens
			}
		case "message_stop":
			// Terminal — break after the loop emits Done.
		case "error":
			streamErr = fmt.Errorf("lens stream: %s", event.Error.Message)
			sendChunk(chunks, StreamChunk{Error: streamErr})
			return streamErr
		}
	}
	if err := scanner.Err(); err != nil {
		sendChunk(chunks, StreamChunk{Error: err})
		return err
	}
	sendChunk(chunks, usage)
	return nil
}

// readOpenAISSE parses the OpenAI streaming format. One event
// per `data: {choices:[{delta:{content:"..."}}]}`; usage arrives
// in the last `data: {...,"usage":{...}}` event.
func readOpenAISSE(ctx context.Context, r interface{ Read([]byte) (int, error) }, chunks chan<- StreamChunk) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	usage := StreamChunk{Done: true}

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			break
		}
		var event struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}
		if event.Error != nil {
			err := fmt.Errorf("lens stream: %s", event.Error.Message)
			sendChunk(chunks, StreamChunk{Error: err})
			return err
		}
		if event.Usage != nil {
			usage.InputTokens = event.Usage.PromptTokens
			usage.OutputTokens = event.Usage.CompletionTokens
		}
		for _, ch := range event.Choices {
			if ch.Delta.Content != "" {
				sendChunk(chunks, StreamChunk{Text: ch.Delta.Content})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		sendChunk(chunks, StreamChunk{Error: err})
		return err
	}
	sendChunk(chunks, usage)
	return nil
}

// sendChunk is a non-blocking-friendly helper: when the caller's
// context is dead or the channel is full we still try to deliver,
// but we never panic on a closed channel.
func sendChunk(chunks chan<- StreamChunk, c StreamChunk) {
	defer func() { _ = recover() }()
	chunks <- c
}

// isOpenAIModel returns true when the model identifier belongs
// to the OpenAI family. Kept simple — any prefix that isn't
// covered here defaults to the Anthropic route.
func isOpenAIModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(m, "gpt-") || strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3")
}

// Status pings /healthz so the CLI's "check" command can report a
// fast yes/no without paying for a real completion.
func (c *Client) Status(ctx context.Context) (bool, error) {
	if c.url == "" {
		return false, errors.New("lens: url empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"/healthz", nil)
	if err != nil {
		return false, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200, nil
}
