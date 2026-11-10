package anthropicprovider

import (
	"encoding/json"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

// unmarshalBlocks constructs []ContentBlockUnion via JSON round-trip so that
// the internal JSON.raw field is populated (required by AsText/AsThinking).
func unmarshalBlocks(t *testing.T, jsonStr string) []anthropic.ContentBlockUnion {
	t.Helper()
	var blocks []anthropic.ContentBlockUnion
	if err := json.Unmarshal([]byte(jsonStr), &blocks); err != nil {
		t.Fatalf("unmarshalBlocks: %v", err)
	}
	return blocks
}

func TestParseResponse_ThinkingBlock(t *testing.T) {
	resp := &anthropic.Message{
		Content: unmarshalBlocks(t, `[
			{"type":"thinking","thinking":"Let me reason step by step...","signature":"sig"},
			{"type":"text","text":"The answer is 42."}
		]`),
		StopReason: anthropic.StopReasonEndTurn,
	}

	result, err := parseResponse(resp)
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}

	if result.Reasoning != "Let me reason step by step..." {
		t.Errorf("Reasoning = %q, want thinking content", result.Reasoning)
	}
	if result.Content != "The answer is 42." {
		t.Errorf("Content = %q, want text content", result.Content)
	}
	if result.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", result.FinishReason)
	}
}

func TestParseResponse_NoThinkingBlock(t *testing.T) {
	resp := &anthropic.Message{
		Content: unmarshalBlocks(t, `[
			{"type":"text","text":"Just a normal response."}
		]`),
		StopReason: anthropic.StopReasonEndTurn,
	}

	result, err := parseResponse(resp)
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}

	if result.Reasoning != "" {
		t.Errorf("Reasoning = %q, want empty", result.Reasoning)
	}
	if result.Content != "Just a normal response." {
		t.Errorf("Content = %q, want text content", result.Content)
	}
}

func TestBuildParams_NoThinkingKeepsTemperature(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "hello"}}
	opts := map[string]any{
		"temperature": 0.8,
	}

	params, err := buildParams(msgs, nil, "claude-sonnet-4-6", opts)
	if err != nil {
		t.Fatal(err)
	}

	if !params.Temperature.Valid() {
		t.Error("temperature should be preserved when thinking is not set")
	}
	if params.Temperature.Value != 0.8 {
		t.Errorf("temperature = %f, want 0.8", params.Temperature.Value)
	}
}
