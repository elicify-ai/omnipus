package gateway

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Chief Q-B: exercise the production streaming path that owns done.Stats.
// Each wrapper calls its OWN Chat so usage and late-input fixtures do not lose
// their overrides through promotion of the embedded script's ChatStream.
func (p *wu15ScriptProvider) ChatStream(ctx context.Context, msgs []providers.Message, defs []providers.ToolDefinition, model string, opts map[string]any, onChunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	response, err := p.Chat(ctx, msgs, defs, model, opts)
	return wu15StreamResponse(response, err, onChunk)
}

func (p *wu15UsageProvider) ChatStream(ctx context.Context, msgs []providers.Message, defs []providers.ToolDefinition, model string, opts map[string]any, onChunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	response, err := p.Chat(ctx, msgs, defs, model, opts)
	return wu15StreamResponse(response, err, onChunk)
}

func (p *wu15LateWebProvider) ChatStream(ctx context.Context, msgs []providers.Message, defs []providers.ToolDefinition, model string, opts map[string]any, onChunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	response, err := p.Chat(ctx, msgs, defs, model, opts)
	return wu15StreamResponse(response, err, onChunk)
}

func (p *wu15RelayProvider) ChatStream(ctx context.Context, msgs []providers.Message, defs []providers.ToolDefinition, model string, opts map[string]any, onChunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	response, err := p.Chat(ctx, msgs, defs, model, opts)
	return wu15StreamResponse(response, err, onChunk)
}

func wu15StreamResponse(response *providers.LLMResponse, err error, onChunk func(string)) (*providers.LLMResponse, error) {
	if err != nil {
		return nil, err
	}
	if response != nil && onChunk != nil && response.Content != "" {
		onChunk(response.Content)
	}
	return response, nil
}

var _ providers.StreamingProvider = (*wu15ScriptProvider)(nil)
var _ providers.StreamingProvider = (*wu15UsageProvider)(nil)
var _ providers.StreamingProvider = (*wu15LateWebProvider)(nil)
var _ providers.StreamingProvider = (*wu15RelayProvider)(nil)
