package providers

import (
	"context"

	anthropicprovider "github.com/elicify-ai/omnipus/pkg/providers/anthropic"
)

type ClaudeProvider struct {
	delegate *anthropicprovider.Provider
}

func NewClaudeProvider(token string) *ClaudeProvider {
	return &ClaudeProvider{
		delegate: anthropicprovider.NewProvider(token),
	}
}

func newClaudeProviderWithDelegate(delegate *anthropicprovider.Provider) *ClaudeProvider {
	return &ClaudeProvider{delegate: delegate}
}

func (p *ClaudeProvider) Chat(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (*LLMResponse, error) {
	resp, err := p.delegate.Chat(ctx, messages, tools, model, options)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// ChatStream implements StreamingProvider by forwarding to the delegate,
// exactly as HTTPProvider does.
//
// Without this method ClaudeProvider does not satisfy StreamingProvider, and
// ClaudeProvider is the ONLY thing that constructs the native Anthropic
// provider (see NewClaudeProvider — the
// inner type has no other non-test caller). The agent loop's
// `activeProvider.(providers.StreamingProvider)` assertion therefore failed
// for every Anthropic install, silently taking the non-streaming path — so the
// delegate's ChatStream, and the tool-argument progress it emits, were
// unreachable in production.
//
// The delegate is held as an unexported, NON-EMBEDDED field, so nothing is
// promoted automatically: each capability has to be forwarded deliberately.
// Any future optional interface the delegate gains needs the same treatment,
// and the compliance assertions in compliance.go must name
// THIS type — the one the factory actually returns — not the inner one.
func (p *ClaudeProvider) ChatStream(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
	onChunk func(accumulated string),
	onProgress OnToolCallProgress,
) (*LLMResponse, error) {
	return p.delegate.ChatStream(ctx, messages, tools, model, options, onChunk, onProgress)
}

func (p *ClaudeProvider) GetDefaultModel() string {
	return p.delegate.GetDefaultModel()
}
