// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package anthropicprovider

import (
	"errors"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// TestParseResponse_ToolCallDecodeFailureCarriesStopReasonAndUsageEvidence is
// the ADR-087 Finding #7 regression: parseResponse's tool_use decode-failure
// branch used to `return nil, err` bare, throwing away resp.StopReason and
// resp.Usage even though both were already in scope. That made the refused,
// billed generation invisible to the per-turn usage debit (D3.9) and left the
// classifier unable to tell a max_tokens cutoff from a malformed call.
//
// The tool_use "input" here is a well-formed JSON number (`42`), not a
// syntactically-truncated fragment — Anthropic's SDK re-derives the tool_use
// block from the surrounding content-block JSON literal
// (ContentBlockUnion.AsToolUse), which must itself be valid JSON, so a
// genuinely unterminated fragment cannot be embedded in the outer array.
// That is fine: the point of this test is the EVIDENCE wiring, and ADR-087
// D5 says the finish reason wins even over a well-formed-shaped fragment —
// exactly what StopReasonMaxTokens is here to prove.
func TestParseResponse_ToolCallDecodeFailureCarriesStopReasonAndUsageEvidence(t *testing.T) {
	resp := &anthropic.Message{
		Content: unmarshalBlocks(t, `[
			{"type":"tool_use","id":"toolu_1","name":"write_file","input":42}
		]`),
		StopReason: anthropic.StopReasonMaxTokens,
		Usage: anthropic.Usage{
			InputTokens:  123,
			OutputTokens: 45,
		},
	}

	_, err := parseResponse(resp)
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
	if tae.FinishReason != "max_tokens" {
		t.Errorf("FinishReason = %q, want %q", tae.FinishReason, "max_tokens")
	}
	if !tae.Truncated {
		t.Error("Truncated = false, want true (stop_reason=max_tokens is truncation evidence, ADR-087 D5)")
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

// TestParseResponse_ToolCallDecodeFailureWithoutTruncatingStopReason pins the
// other side: a non-max-tokens stop reason on a well-formed-but-wrong-shape
// payload must NOT be marked Truncated, so a genuine malformed-call fault
// isn't misreported as a cutoff.
func TestParseResponse_ToolCallDecodeFailureWithoutTruncatingStopReason(t *testing.T) {
	resp := &anthropic.Message{
		Content: unmarshalBlocks(t, `[
			{"type":"tool_use","id":"toolu_1","name":"write_file","input":42}
		]`),
		StopReason: anthropic.StopReasonToolUse,
		Usage: anthropic.Usage{
			InputTokens:  5,
			OutputTokens: 5,
		},
	}

	_, err := parseResponse(resp)
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
		t.Error("Truncated = true, want false — well-formed non-object shape with a non-truncating stop reason")
	}
}
