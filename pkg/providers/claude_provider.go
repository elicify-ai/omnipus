package providers

import (
	"context"
	"time"

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

// NewClaudeProviderWithTimeout builds the SDK-backed Anthropic transport at an
// explicit base URL with an explicit streaming silence limit — the constructor
// the factory's Anthropic-protocol dispatch uses (WP-E, issue #980).
//
//   - apiBase is the request base verbatim; the empty string falls back to the
//     adapter's default (anthropicprovider.NewProviderWithBaseURL +
//     normalizeBaseURL, which also strips a trailing "/v1" the SDK re-adds in
//     its request path).
//   - timeout is the STREAM-SILENCE limit (WithStreamStallTimeout), not a
//     wall-clock cap: a stream delivering nothing for this long aborts with
//     common.ErrStreamStalled, a stream that keeps delivering is never cut,
//     and a non-positive value resolves to the shipped default.
func NewClaudeProviderWithTimeout(token, apiBase string, timeout time.Duration) *ClaudeProvider {
	return &ClaudeProvider{
		delegate: anthropicprovider.NewProviderWithBaseURL(token, apiBase).
			WithStreamStallTimeout(timeout),
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
	onReasoning func(accumulated string),
) (*LLMResponse, error) {
	return p.delegate.ChatStream(ctx, messages, tools, model, options, onChunk, onProgress, onReasoning)
}

func (p *ClaudeProvider) GetDefaultModel() string {
	return p.delegate.GetDefaultModel()
}

// SupportsThinking implements providers.ThinkingCapable by forwarding to the
// delegate. Without this forwarder the agent loop's
// `activeProvider.(providers.ThinkingCapable)` assertion
// (pkg/agent/loop_run_turn.go::prepareLLMRequest) fails for EVERY Anthropic
// turn once the factory dispatches ClaudeProvider — thinking_level would be
// silently dropped exactly the way ChatStream's absence once silently dropped
// streaming (see compliance.go's rule: assert the type the factory returns).
func (p *ClaudeProvider) SupportsThinking() bool {
	return p.delegate.SupportsThinking()
}
