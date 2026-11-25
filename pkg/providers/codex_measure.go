package providers

// codex_measure.go — whole-request measurement for the Codex Responses
// transport (issue #1968, spec FR-12; security finding F4). Chat and
// SerializedRequestBytes build the request through the same function, so
// the measured body is the body Chat streams.

import (
	"fmt"

	"github.com/openai/openai-go/v3/responses"
	"github.com/tidwall/sjson"
)

// codexRequestParams is the one request builder Chat and
// SerializedRequestBytes share: it resolves the model the Codex backend
// accepts and applies the native-search rule (tools.web.prefer_native:
// inject native search only when the agent loop passes
// options["native_search"]=true). fallbackReason is non-empty when the
// requested model was replaced.
func (p *CodexProvider) codexRequestParams(
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (params responses.ResponseNewParams, resolvedModel, fallbackReason string) {
	resolvedModel, fallbackReason = resolveCodexModel(model)
	useNativeSearch := p.enableWebSearch && (options["native_search"] == true)
	return buildCodexParams(messages, tools, resolvedModel, options, useNativeSearch), resolvedModel, fallbackReason
}

// SerializedRequestBytes returns the exact byte length of the body Chat
// sends for this request, serialized and never sent. Chat always streams
// (Responses.NewStreaming), whatever the stream argument says: the SDK
// marshals the params and then sets "stream": true on that body, and this
// measurement does the same two steps.
func (p *CodexProvider) SerializedRequestBytes(
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
	_ bool,
) (int, error) {
	params, _, _ := p.codexRequestParams(messages, tools, model, options)
	body, err := params.MarshalJSON()
	if err != nil {
		return 0, fmt.Errorf("codex: failed to marshal request: %w", err)
	}
	body, err = sjson.SetBytes(body, "stream", true)
	if err != nil {
		return 0, fmt.Errorf("codex: failed to mark request as streaming: %w", err)
	}
	return len(body), nil
}
