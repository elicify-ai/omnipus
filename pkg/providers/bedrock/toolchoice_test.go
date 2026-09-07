//go:build bedrock

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package bedrock

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

func bedrockToolsFixture() []ToolDefinition {
	return []ToolDefinition{
		{
			Function: protocoltypes.ToolFunctionDefinition{
				Name:        "get_weather",
				Description: "Get the current weather",
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
	}
}

// TestToolChoice_Bedrock_DefaultUnchanged proves a caller that never touches
// tool_choice leaves ToolConfig.ToolChoice unset — this provider's exact
// pre-ADR-081 behavior (S-13); a nil/omitted ToolChoice already meant "auto"
// to the Converse API.
func TestToolChoice_Bedrock_DefaultUnchanged(t *testing.T) {
	toolConfig := buildToolConfig(bedrockToolsFixture(), map[string]any{})
	require.NotNil(t, toolConfig)
	assert.Nil(t, toolConfig.ToolChoice)
}

// TestToolChoice_Bedrock_ForcedRequired maps ADR-081's Required onto
// Bedrock's types.ToolChoiceMemberAny (S-13, [G-B1] "bedrock — NEW
// tool-choice code").
func TestToolChoice_Bedrock_ForcedRequired(t *testing.T) {
	options := map[string]any{
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceRequired},
	}
	toolConfig := buildToolConfig(bedrockToolsFixture(), options)
	require.NotNil(t, toolConfig)
	_, ok := toolConfig.ToolChoice.(*types.ToolChoiceMemberAny)
	assert.True(t, ok, "ToolChoice = %#v, want *types.ToolChoiceMemberAny", toolConfig.ToolChoice)
}

// TestToolChoice_Bedrock_ForcedAutoExplicit proves an explicit Auto request
// sets types.ToolChoiceMemberAuto, distinct from the "no option" nil default.
func TestToolChoice_Bedrock_ForcedAutoExplicit(t *testing.T) {
	options := map[string]any{
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceAuto},
	}
	toolConfig := buildToolConfig(bedrockToolsFixture(), options)
	require.NotNil(t, toolConfig)
	_, ok := toolConfig.ToolChoice.(*types.ToolChoiceMemberAuto)
	assert.True(t, ok, "ToolChoice = %#v, want *types.ToolChoiceMemberAuto", toolConfig.ToolChoice)
}

// TestToolChoice_Bedrock_RequiredWithNoTools_Guarded: buildToolConfig
// returns nil (no ToolConfig at all) when there are no tools to offer, so
// Converse never sees ToolChoice=Any alongside an empty Tools list.
func TestToolChoice_Bedrock_RequiredWithNoTools_Guarded(t *testing.T) {
	options := map[string]any{
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceRequired},
	}
	toolConfig := buildToolConfig(nil, options)
	assert.Nil(t, toolConfig, "buildToolConfig() = %#v, want nil (no tools offered)", toolConfig)
}

// TestToolChoice_Bedrock_RequiredWithOnlyInvalidTools_Guarded covers the same
// guard when tools is non-empty but every entry is filtered out by
// convertTools (empty name) — toolConfig.Tools ends up empty too.
func TestToolChoice_Bedrock_RequiredWithOnlyInvalidTools_Guarded(t *testing.T) {
	options := map[string]any{
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceRequired},
	}
	tools := []ToolDefinition{{Function: protocoltypes.ToolFunctionDefinition{Name: "   "}}}
	toolConfig := buildToolConfig(tools, options)
	assert.Nil(t, toolConfig, "buildToolConfig() = %#v, want nil (all tools filtered out)", toolConfig)
}
