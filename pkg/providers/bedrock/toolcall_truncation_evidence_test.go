//go:build bedrock

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package bedrock

import (
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// undecodableSmithyInput builds a document.Interface whose
// UnmarshalSmithyDocument call always fails: the smithy JSON encoder
// (github.com/aws/smithy-go/document/json's Encoder.encodeStruct) refuses
// any struct convertible to time.Time with "unsupported type", so
// MarshalSmithyDocument — which UnmarshalSmithyDocument calls first — errors
// before it ever gets to decode. document.NewLazyDocument(map[string]any{...})
// also fails UnmarshalSmithyDocument in this SDK version, but only through the
// marshaler's own defect (aws/aws-sdk-go-v2#2751 transposes the decode source
// and target), not a genuinely undecodable payload — this helper is what
// actually reaches the UnmarshalSmithyDocument error branch parseResponse
// guards.
func undecodableSmithyInput() document.Interface {
	return document.NewLazyDocument(time.Now())
}

// TestParseResponse_ToolCallDecodeFailureCarriesStopReasonAndUsageEvidence is
// the ADR-087 Finding #7 regression for Bedrock specifically: this decode
// site does not go through common.DecodeToolCallArguments (Bedrock hands
// tool input as a Smithy document, not a JSON string), and before this fix
// it built a plain fmt.Errorf wrapping common.ErrToolArgumentsUndecodable —
// never a *common.ToolArgumentsError. errors.As could therefore never
// classify a Bedrock decode failure as tool_call_truncated, so a genuine
// StopReasonMaxTokens cutoff always read as "filled in arguments
// incorrectly" no matter how clearly the stop reason said otherwise, and the
// refused attempt's billed usage (D3.9) was thrown away entirely.
func TestParseResponse_ToolCallDecodeFailureCarriesStopReasonAndUsageEvidence(t *testing.T) {
	output := &bedrockruntime.ConverseOutput{
		Output: &types.ConverseOutputMemberMessage{
			Value: types.Message{
				Role: types.ConversationRoleAssistant,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberToolUse{
						Value: types.ToolUseBlock{
							ToolUseId: aws.String("call_1"),
							Name:      aws.String("write_file"),
							Input:     undecodableSmithyInput(),
						},
					},
				},
			},
		},
		StopReason: types.StopReasonMaxTokens,
		Usage: &types.TokenUsage{
			InputTokens:  aws.Int32(123),
			OutputTokens: aws.Int32(45),
		},
	}

	_, err := parseResponse(output)
	if err == nil {
		t.Fatal("expected a refusal for an undecodable tool_use input")
	}
	if !errors.Is(err, common.ErrToolArgumentsUndecodable) {
		t.Errorf("error %v does not wrap common.ErrToolArgumentsUndecodable", err)
	}

	var tae *common.ToolArgumentsError
	if !errors.As(err, &tae) {
		t.Fatalf("error is not a *common.ToolArgumentsError (Bedrock must construct one directly, "+
			"not a bare fmt.Errorf): %v", err)
	}
	if tae.FinishReason != "max_tokens" {
		t.Errorf("FinishReason = %q, want %q", tae.FinishReason, "max_tokens")
	}
	if !tae.Truncated {
		t.Error("Truncated = false, want true (stop_reason=max_tokens)")
	}
	if tae.Usage == nil {
		t.Fatal("Usage = nil, want the refused attempt's billed usage")
	}
	if tae.Usage.PromptTokens != 123 {
		t.Errorf("Usage.PromptTokens = %d, want 123", tae.Usage.PromptTokens)
	}
	if tae.Usage.CompletionTokens != 45 {
		t.Errorf("Usage.CompletionTokens = %d, want 45", tae.Usage.CompletionTokens)
	}
}

// TestParseResponse_ToolCallDecodeFailureWithoutMaxTokensStopReason pins the
// other side: a decode failure under a non-max-tokens stop reason must NOT
// be reported Truncated, so a genuine malformed-input fault (as opposed to a
// cutoff) is not misreported.
func TestParseResponse_ToolCallDecodeFailureWithoutMaxTokensStopReason(t *testing.T) {
	output := &bedrockruntime.ConverseOutput{
		Output: &types.ConverseOutputMemberMessage{
			Value: types.Message{
				Role: types.ConversationRoleAssistant,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberToolUse{
						Value: types.ToolUseBlock{
							ToolUseId: aws.String("call_1"),
							Name:      aws.String("write_file"),
							Input:     undecodableSmithyInput(),
						},
					},
				},
			},
		},
		StopReason: types.StopReasonToolUse,
		Usage: &types.TokenUsage{
			InputTokens:  aws.Int32(5),
			OutputTokens: aws.Int32(5),
		},
	}

	_, err := parseResponse(output)
	if err == nil {
		t.Fatal("expected a refusal for an undecodable tool_use input")
	}

	var tae *common.ToolArgumentsError
	if !errors.As(err, &tae) {
		t.Fatalf("error is not a *common.ToolArgumentsError: %v", err)
	}
	if tae.FinishReason != "tool_use" {
		t.Errorf("FinishReason = %q, want %q", tae.FinishReason, "tool_use")
	}
	if tae.Truncated {
		t.Error("Truncated = true, want false — non-truncating stop reason")
	}
}
