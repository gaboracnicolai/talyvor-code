package agentloop

import "context"

// Message is one conversation turn. Local to this package so the loop's mechanics
// don't couple to any provider type; the production Model adapter converts to/from
// lens.Message.
type Message struct {
	Role    string
	Content string
}

// Model is the loop's LLM seam: given the running transcript, return the next reply.
// Provider-agnostic (plain text in, plain text out) so the loop is testable with a
// scripted stub and works with any model through the existing Lens client.
type Model interface {
	Complete(ctx context.Context, messages []Message) (string, error)
}

// ModelFunc adapts a function to Model (handy for test stubs).
type ModelFunc func(ctx context.Context, messages []Message) (string, error)

func (f ModelFunc) Complete(ctx context.Context, messages []Message) (string, error) {
	return f(ctx, messages)
}
