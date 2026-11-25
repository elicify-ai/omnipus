package openai_compat

// pdf_wire.go — the serialized-size capability of issue #1968 (spec FR-12)
// for the Chat Completions transport.

import (
	"encoding/json"
	"fmt"
)

// SerializedRequestBytes returns the exact byte length of the Chat
// Completions body Chat (stream false) or ChatStream (stream true) would
// send for this request, marshalled the same way and never sent.
func (p *Provider) SerializedRequestBytes(
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
	stream bool,
) (int, error) {
	requestBody := p.buildRequestBody(messages, tools, model, options)
	if stream {
		requestBody["stream"] = true
		requestBody["stream_options"] = map[string]any{"include_usage": true}
	}
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal request: %w", err)
	}
	return len(jsonData), nil
}
