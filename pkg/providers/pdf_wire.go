package providers

// pdf_wire.go — optional transport capabilities for native PDFs (issue
// #1968, spec FR-11 and FR-12). The agent consults them only when
// config.json pdf_limits (config.Config.ResolvePDFLimits) states the
// matching limit; no transport enforces a numeric limit itself.

import (
	anthropicprovider "github.com/elicify-ai/omnipus/pkg/providers/anthropic"
	"github.com/elicify-ai/omnipus/pkg/providers/azure"
	"github.com/elicify-ai/omnipus/pkg/providers/bedrock"
)

// RequestBodyMeasurer is implemented by transports that can serialize a
// request without sending it (FR-12). The result is the exact byte length
// of the body Chat (stream false) or ChatStream (stream true) would send.
type RequestBodyMeasurer interface {
	SerializedRequestBytes(messages []Message, tools []ToolDefinition, model string, options map[string]any, stream bool) (int, error)
}

// WireMessageGrouper is implemented by transports that merge several
// internal messages into one wire message (FR-11): each group lists the
// indexes of the internal messages that travel together. A transport
// without it sends one internal message as one wire message.
type WireMessageGrouper interface {
	WireMessageGroups(messages []Message) [][]int
}

// Compile-time capability assertions (see compliance.go for why they live
// in a non-test file): every type the factory or the Responses pool
// returns for the four wire formats measures its request, and the two
// formats that merge tool results report their grouping.
var (
	_ RequestBodyMeasurer = (*HTTPProvider)(nil)
	_ RequestBodyMeasurer = (*ClaudeProvider)(nil)
	_ RequestBodyMeasurer = (*anthropicprovider.Provider)(nil)
	_ RequestBodyMeasurer = (*bedrock.Provider)(nil)
	_ RequestBodyMeasurer = (*azure.Provider)(nil)
	_ RequestBodyMeasurer = (*CodexProvider)(nil)
	_ WireMessageGrouper  = (*ClaudeProvider)(nil)
	_ WireMessageGrouper  = (*anthropicprovider.Provider)(nil)
	_ WireMessageGrouper  = (*bedrock.Provider)(nil)
)

// SerializedRequestBytes forwards to the OpenAI-compatible transport.
func (p *HTTPProvider) SerializedRequestBytes(
	messages []Message, tools []ToolDefinition, model string, options map[string]any, stream bool,
) (int, error) {
	return p.delegate.SerializedRequestBytes(messages, tools, model, options, stream)
}

// SerializedRequestBytes forwards to the native Anthropic transport.
func (p *ClaudeProvider) SerializedRequestBytes(
	messages []Message, tools []ToolDefinition, model string, options map[string]any, stream bool,
) (int, error) {
	return p.delegate.SerializedRequestBytes(messages, tools, model, options, stream)
}

// WireMessageGroups forwards to the native Anthropic transport.
func (p *ClaudeProvider) WireMessageGroups(messages []Message) [][]int {
	return p.delegate.WireMessageGroups(messages)
}
