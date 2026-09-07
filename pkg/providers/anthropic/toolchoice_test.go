package anthropicprovider

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

func anthropicToolsFixture() []ToolDefinition {
	return []ToolDefinition{{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        "get_weather",
			Description: "Get weather",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}}
}

// TestToolChoice_Anthropic_DefaultUnchanged proves a caller that never
// touches tool_choice leaves params.ToolChoice at its zero value (omitted
// from the wire, API defaults to "auto") — this provider's exact
// pre-ADR-081 behavior (S-13).
func TestToolChoice_Anthropic_DefaultUnchanged(t *testing.T) {
	params, err := buildParams([]Message{{Role: "user", Content: "hi"}}, anthropicToolsFixture(), "claude-sonnet-4.6", map[string]any{})
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}
	if params.ToolChoice.OfAny != nil || params.ToolChoice.OfAuto != nil {
		t.Errorf("ToolChoice = %+v, want the zero value (no option set)", params.ToolChoice)
	}
}

// TestToolChoice_Anthropic_ForcedRequired maps ADR-081's Required onto the
// SDK's "any" variant — Anthropic has no literal "required", "any" is its
// equivalent (S-13, [G-B1] "anthropic — NEW tool-choice code").
func TestToolChoice_Anthropic_ForcedRequired(t *testing.T) {
	options := map[string]any{
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceRequired},
	}
	params, err := buildParams([]Message{{Role: "user", Content: "hi"}}, anthropicToolsFixture(), "claude-sonnet-4.6", options)
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}
	if params.ToolChoice.OfAny == nil {
		t.Fatalf("ToolChoice.OfAny is nil, want it set (required maps to Anthropic's \"any\")")
	}
	if params.ToolChoice.OfAuto != nil {
		t.Errorf("ToolChoice.OfAuto is set, want only OfAny")
	}
}

// TestToolChoice_Anthropic_ForcedAutoExplicit proves an explicit Auto
// request sets the SDK's OfAuto variant (not just leaving the zero value).
func TestToolChoice_Anthropic_ForcedAutoExplicit(t *testing.T) {
	options := map[string]any{
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceAuto},
	}
	params, err := buildParams([]Message{{Role: "user", Content: "hi"}}, anthropicToolsFixture(), "claude-sonnet-4.6", options)
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}
	if params.ToolChoice.OfAuto == nil {
		t.Fatalf("ToolChoice.OfAuto is nil, want it set")
	}
	if params.ToolChoice.OfAny != nil {
		t.Errorf("ToolChoice.OfAny is set, want only OfAuto")
	}
}

// TestToolChoice_Anthropic_RequiredWithNoTools_Guarded: applyToolChoice is
// only called inside the len(tools) > 0 block, so Required with no tools
// must leave ToolChoice at its zero value — Anthropic rejects tool_choice
// without tools.
func TestToolChoice_Anthropic_RequiredWithNoTools_Guarded(t *testing.T) {
	options := map[string]any{
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceRequired},
	}
	params, err := buildParams([]Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4.6", options)
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}
	if params.ToolChoice.OfAny != nil || params.ToolChoice.OfAuto != nil {
		t.Errorf("ToolChoice = %+v, want the zero value (no tools offered)", params.ToolChoice)
	}
}

// TestToolChoice_Anthropic_RequiredWithThinking_DegradesToAuto proves the
// review-round-1 fix: Anthropic's API rejects tool_choice:{type:"any"}
// combined with thinking:{type:"enabled"|"adaptive"} (HTTP 400). When both
// a forced Required tool-choice and a non-off thinking_level are resolved on
// the same request, buildParams must degrade to Auto instead of emitting the
// incompatible pair — Layers 2-3 (validation + keeper/fallback) still drive
// the model toward the narrowed tools without a hard API rejection.
func TestToolChoice_Anthropic_RequiredWithThinking_DegradesToAuto(t *testing.T) {
	options := map[string]any{
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceRequired},
		"thinking_level":                  "medium",
		"max_tokens":                      8192,
	}
	params, err := buildParams([]Message{{Role: "user", Content: "hi"}}, anthropicToolsFixture(), "claude-sonnet-4.6", options)
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}
	if params.ToolChoice.OfAny != nil {
		t.Errorf("ToolChoice.OfAny is set, want it degraded to Auto because thinking is enabled")
	}
	if params.ToolChoice.OfAuto == nil {
		t.Fatalf("ToolChoice.OfAuto is nil, want it set (degraded from Required)")
	}
	if params.Thinking.OfEnabled == nil {
		t.Fatalf("Thinking.OfEnabled is nil — the test fixture's own precondition (thinking on) is broken")
	}
}

// TestToolChoice_Anthropic_RequiredWithoutThinking_StaysForced proves the
// degrade in the test above is conditional on thinking actually being
// enabled — Required alone (no thinking_level) is untouched.
func TestToolChoice_Anthropic_RequiredWithoutThinking_StaysForced(t *testing.T) {
	options := map[string]any{
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceRequired},
	}
	params, err := buildParams([]Message{{Role: "user", Content: "hi"}}, anthropicToolsFixture(), "claude-sonnet-4.6", options)
	if err != nil {
		t.Fatalf("buildParams() error: %v", err)
	}
	if params.ToolChoice.OfAny == nil {
		t.Fatalf("ToolChoice.OfAny is nil, want it set (no thinking in play, Required stays forced)")
	}
	if params.ToolChoice.OfAuto != nil {
		t.Errorf("ToolChoice.OfAuto is set, want only OfAny (no thinking to conflict with)")
	}
}
