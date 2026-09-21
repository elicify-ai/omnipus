// Omnipus - Ultra-lightweight personal AI agent
// License: MIT

package bedrock

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

func undecodableToolInputResponse(stopReason string, inputTokens, outputTokens int) *converseResponse {
	return &converseResponse{
		Output: converseOutput{Message: bedrockResponseMessage{Content: []responseContentBlock{{
			ToolUse: &responseToolUseBlock{
				ToolUseID: "call_1",
				Name:      "write_file",
				Input:     json.RawMessage(`{"unterminated"`),
			},
		}}}},
		StopReason: stopReason,
		Usage:      &tokenUsage{InputTokens: inputTokens, OutputTokens: outputTokens},
	}
}

func TestParseResponse_ToolCallDecodeFailureCarriesStopReasonAndUsageEvidence(t *testing.T) {
	_, err := parseResponse(undecodableToolInputResponse("max_tokens", 123, 45))
	if err == nil {
		t.Fatal("expected a refusal for an undecodable tool_use input")
	}
	if !errors.Is(err, common.ErrToolArgumentsUndecodable) {
		t.Errorf("error %v does not wrap common.ErrToolArgumentsUndecodable", err)
	}
	var tae *common.ToolArgumentsError
	if !errors.As(err, &tae) {
		t.Fatalf("error is not a *common.ToolArgumentsError: %v", err)
	}
	if tae.FinishReason != "max_tokens" || !tae.Truncated {
		t.Errorf("FinishReason=%q Truncated=%v, want max_tokens/true", tae.FinishReason, tae.Truncated)
	}
	if tae.Usage == nil || tae.Usage.PromptTokens != 123 || tae.Usage.CompletionTokens != 45 {
		t.Fatalf("Usage=%#v, want prompt=123 completion=45", tae.Usage)
	}
}

func TestParseResponse_ToolCallDecodeFailureWithoutMaxTokensStopReason(t *testing.T) {
	_, err := parseResponse(undecodableToolInputResponse("tool_use", 5, 5))
	if err == nil {
		t.Fatal("expected a refusal for an undecodable tool_use input")
	}
	var tae *common.ToolArgumentsError
	if !errors.As(err, &tae) {
		t.Fatalf("error is not a *common.ToolArgumentsError: %v", err)
	}
	if tae.FinishReason != "tool_use" || tae.Truncated {
		t.Errorf("FinishReason=%q Truncated=%v, want tool_use/false", tae.FinishReason, tae.Truncated)
	}
}
