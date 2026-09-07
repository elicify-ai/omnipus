package anthropicmessages

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

func anthropicMessagesToolsFixture() []ToolDefinition {
	return []ToolDefinition{{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        "get_weather",
			Description: "Get weather",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}}
}

// TestToolChoice_AnthropicMessages_DefaultUnchanged proves a caller that
// never touches tool_choice leaves the key entirely absent from the wire
// body — this builder's exact pre-ADR-081 behavior (S-13).
func TestToolChoice_AnthropicMessages_DefaultUnchanged(t *testing.T) {
	body, err := buildRequestBody(
		[]Message{{Role: "user", Content: "hi"}}, anthropicMessagesToolsFixture(), "claude-sonnet-4.6",
		map[string]any{"max_tokens": 1024},
	)
	if err != nil {
		t.Fatalf("buildRequestBody() error: %v", err)
	}
	if _, present := body["tool_choice"]; present {
		t.Errorf("tool_choice = %v, want the key absent (no option set)", body["tool_choice"])
	}
}

// TestToolChoice_AnthropicMessages_ForcedRequired maps ADR-081's Required
// onto Anthropic's wire "any" type (S-13, [G-B1] "anthropic_messages — NEW
// tool-choice code").
func TestToolChoice_AnthropicMessages_ForcedRequired(t *testing.T) {
	options := map[string]any{
		"max_tokens":                      1024,
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceRequired},
	}
	body, err := buildRequestBody([]Message{{Role: "user", Content: "hi"}}, anthropicMessagesToolsFixture(), "claude-sonnet-4.6", options)
	if err != nil {
		t.Fatalf("buildRequestBody() error: %v", err)
	}
	want := map[string]any{"type": "any"}
	got, ok := body["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("tool_choice = %#v (%T), want a map[string]any", body["tool_choice"], body["tool_choice"])
	}
	if got["type"] != want["type"] {
		t.Errorf("tool_choice.type = %v, want %v", got["type"], want["type"])
	}
}

// TestToolChoice_AnthropicMessages_ForcedAutoExplicit proves an explicit
// Auto request sets an explicit {"type":"auto"}, distinct from the
// "no option" absent-key default.
func TestToolChoice_AnthropicMessages_ForcedAutoExplicit(t *testing.T) {
	options := map[string]any{
		"max_tokens":                      1024,
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceAuto},
	}
	body, err := buildRequestBody([]Message{{Role: "user", Content: "hi"}}, anthropicMessagesToolsFixture(), "claude-sonnet-4.6", options)
	if err != nil {
		t.Fatalf("buildRequestBody() error: %v", err)
	}
	got, ok := body["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("tool_choice = %#v (%T), want a map[string]any", body["tool_choice"], body["tool_choice"])
	}
	if got["type"] != "auto" {
		t.Errorf("tool_choice.type = %v, want %q", got["type"], "auto")
	}
}

// TestToolChoice_AnthropicMessages_RequiredWithNoTools_Guarded: the forcing
// logic lives inside the len(tools) > 0 block, so Required with no tools
// must leave tool_choice entirely absent.
func TestToolChoice_AnthropicMessages_RequiredWithNoTools_Guarded(t *testing.T) {
	options := map[string]any{
		"max_tokens":                      1024,
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceRequired},
	}
	body, err := buildRequestBody([]Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", options)
	if err != nil {
		t.Fatalf("buildRequestBody() error: %v", err)
	}
	if _, present := body["tools"]; present {
		t.Fatalf("tools key present with an empty tools list")
	}
	if _, present := body["tool_choice"]; present {
		t.Errorf("tool_choice = %v, want the key absent (no tools offered)", body["tool_choice"])
	}
}
