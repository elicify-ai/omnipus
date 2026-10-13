// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// H5 (founder decision 2026-10-13): the empty-answer warning carries what
// actually came back — provider, model, stream event types with counts,
// output item types, status/incomplete reason, finish reason and token
// usage — and nothing of the content.
func TestEmptyAnswerLogFields_CarriesShapeNotContent(t *testing.T) {
	candidates := []providers.FallbackCandidate{
		{Provider: "openai-chatgpt", Model: "gpt-6.1-sol"},
		{Provider: "openrouter", Model: "deepseek/deepseek-flash-latest"},
	}
	resp := &providers.LLMResponse{
		Content:      "",
		FinishReason: "length",
		Usage:        &providers.UsageInfo{PromptTokens: 14925, CompletionTokens: 7, TotalTokens: 14932},
		Diagnostics: &providers.ResponseDiagnostics{
			EventCounts:      map[string]int{"response.created": 1, "response.completed": 1},
			OutputItemTypes:  []string{"reasoning"},
			OutputSource:     "completed",
			Status:           "incomplete",
			IncompleteReason: "max_output_tokens",
		},
	}

	f := emptyAnswerLogFields("omnipus", 1, candidates, "gpt-6.1-sol", "gpt-6.1-sol", resp)

	assert.Equal(t, map[string]any{
		"agent_id":          "omnipus",
		"iteration":         1,
		"provider":          "openai-chatgpt",
		"model":             "gpt-6.1-sol",
		"finish_reason":     "length",
		"tool_calls":        0,
		"tokens_in":         14925,
		"tokens_out":        7,
		"tokens_total":      14932,
		"stream_events":     map[string]int{"response.created": 1, "response.completed": 1},
		"output_item_types": []string{"reasoning"},
		"output_source":     "completed",
		"status":            "incomplete",
		"incomplete_reason": "max_output_tokens",
	}, f)
}

func TestEmptyAnswerLogFields_NamesTheAnsweringCandidate(t *testing.T) {
	candidates := []providers.FallbackCandidate{
		{Provider: "openai-chatgpt", Model: "gpt-6.1-sol"},
		{Provider: "openrouter", Model: "deepseek/deepseek-flash-latest"},
	}
	f := emptyAnswerLogFields("a", 2, candidates, "gpt-6.1-sol", "deepseek/deepseek-flash-latest",
		&providers.LLMResponse{FinishReason: "stop"})

	assert.Equal(t, "openrouter", f["provider"])
	assert.Equal(t, "deepseek/deepseek-flash-latest", f["model"])
	assert.Equal(t, "unreported", f["usage"])
	assert.Equal(t, "unavailable for this provider", f["stream_diagnostics"])
	_, hasContent := f["content"]
	assert.False(t, hasContent)
}
