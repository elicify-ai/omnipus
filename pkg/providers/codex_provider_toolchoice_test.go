// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"testing"

	"github.com/openai/openai-go/v3/responses"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

func codexToolsFixture() []ToolDefinition {
	return []ToolDefinition{{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        "get_weather",
			Description: "Get weather",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}}
}

// TestToolChoice_CodexProvider_DefaultUnchanged proves a caller that never
// touches tool_choice gets an explicit Auto — this builder never set
// ToolChoice before, so this is NEW code, but functionally identical to the
// prior state: the Responses API defaults an omitted tool_choice to "auto"
// server-side, same as this explicit value (S-13, [G-B1]).
func TestToolChoice_CodexProvider_DefaultUnchanged(t *testing.T) {
	params := buildCodexParams([]Message{{Role: "user", Content: "hi"}}, codexToolsFixture(), "gpt-5", map[string]any{}, false)
	if !params.ToolChoice.OfToolChoiceMode.Valid() {
		t.Fatalf("ToolChoice.OfToolChoiceMode is not set")
	}
	if got := params.ToolChoice.OfToolChoiceMode.Value; got != responses.ToolChoiceOptionsAuto {
		t.Errorf("ToolChoice mode = %q, want %q", got, responses.ToolChoiceOptionsAuto)
	}
}

// TestToolChoice_CodexProvider_ForcedRequired maps ADR-081's Required onto
// the Responses API union (S-13, [G-B1] "codex_provider — NEW tool-choice
// code").
func TestToolChoice_CodexProvider_ForcedRequired(t *testing.T) {
	options := map[string]any{
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceRequired},
	}
	params := buildCodexParams([]Message{{Role: "user", Content: "hi"}}, codexToolsFixture(), "gpt-5", options, false)
	if !params.ToolChoice.OfToolChoiceMode.Valid() {
		t.Fatalf("ToolChoice.OfToolChoiceMode is not set")
	}
	if got := params.ToolChoice.OfToolChoiceMode.Value; got != responses.ToolChoiceOptionsRequired {
		t.Errorf("ToolChoice mode = %q, want %q", got, responses.ToolChoiceOptionsRequired)
	}
}

// TestToolChoice_CodexProvider_RequiredWithNoTools_Guarded: ToolChoice is
// only set inside the `len(tools) > 0 || enableWebSearch` block, so
// Required with neither must leave the union unset entirely.
func TestToolChoice_CodexProvider_RequiredWithNoTools_Guarded(t *testing.T) {
	options := map[string]any{
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceRequired},
	}
	params := buildCodexParams([]Message{{Role: "user", Content: "hi"}}, nil, "gpt-5", options, false)
	if len(params.Tools) != 0 {
		t.Fatalf("Tools = %v, want empty", params.Tools)
	}
	if params.ToolChoice.OfToolChoiceMode.Valid() {
		t.Errorf("ToolChoice.OfToolChoiceMode = %v, want unset (no tools offered)", params.ToolChoice.OfToolChoiceMode.Value)
	}
}
