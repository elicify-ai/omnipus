// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package openai_responses_common

import (
	"errors"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// TestParseResponseBody_ToolCallDecodeFailureCarriesIncompleteStatusAndUsageEvidence
// is the ADR-087 Finding #7 regression for the OpenAI Responses API: the
// function_call decode-failure branch used to `return nil, err` bare, even
// though apiResp.Status/IncompleteDetails.Reason/Usage were all already in
// scope. status:"incomplete" with incomplete_details.reason:"max_output_tokens"
// is the Responses API's raw truncation signal — neither string matches
// AttachToolArgumentsEvidence's normalised-spelling matcher directly, so this
// pins that the site maps it to "length" before attaching, the same
// normalised value ParseResponseBody's own FinishReason already used for a
// successful incomplete response (TestParseResponseBody_IncompleteStatus).
func TestParseResponseBody_ToolCallDecodeFailureCarriesIncompleteStatusAndUsageEvidence(t *testing.T) {
	body := strings.NewReader(`{
		"id": "resp_trunc",
		"object": "response",
		"status": "incomplete",
		"incomplete_details": {"reason": "max_output_tokens"},
		"output": [
			{
				"type": "function_call",
				"call_id": "call_1",
				"name": "write_file",
				"arguments": "{\"path"
			}
		],
		"usage": {"input_tokens": 123, "output_tokens": 45, "total_tokens": 168,
			"input_tokens_details": {"cached_tokens": 0},
			"output_tokens_details": {"reasoning_tokens": 0}}
	}`)

	_, err := ParseResponseBody(body)
	if err == nil {
		t.Fatal("expected a refusal for an undecodable function_call arguments payload")
	}
	if !errors.Is(err, common.ErrToolArgumentsUndecodable) {
		t.Errorf("error %v does not wrap common.ErrToolArgumentsUndecodable", err)
	}

	var tae *common.ToolArgumentsError
	if !errors.As(err, &tae) {
		t.Fatalf("error is not a *common.ToolArgumentsError: %v", err)
	}
	if tae.FinishReason != "length" {
		t.Errorf("FinishReason = %q, want %q (mapped from incomplete_details.reason=max_output_tokens)",
			tae.FinishReason, "length")
	}
	if !tae.Truncated {
		t.Error("Truncated = false, want true (status=incomplete, reason=max_output_tokens)")
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
	if tae.Usage.TotalTokens != 168 {
		t.Errorf("Usage.TotalTokens = %d, want 168", tae.Usage.TotalTokens)
	}
}

// TestParseResponseBody_ToolCallDecodeFailureWithContentFilterIncompleteReason
// pins the deliberate non-mapping: status:"incomplete" with
// incomplete_details.reason:"content_filter" is a refusal, not a token-cap
// cutoff, so it must NOT be reported as truncation evidence.
//
// The arguments payload here is `42` — well-formed JSON of the wrong shape,
// not a syntactically-truncated fragment — deliberately, so that
// DecodeToolCallArguments's own shape-based judgement (which treats an
// EOF-shaped fragment like `{"path` as truncated regardless of finish
// reason, ADR-087 D5) cannot make Truncated true on its own. Only the
// finish-reason mapping under test could set it, and content_filter must not.
func TestParseResponseBody_ToolCallDecodeFailureWithContentFilterIncompleteReason(t *testing.T) {
	body := strings.NewReader(`{
		"id": "resp_cf",
		"object": "response",
		"status": "incomplete",
		"incomplete_details": {"reason": "content_filter"},
		"output": [
			{
				"type": "function_call",
				"call_id": "call_1",
				"name": "write_file",
				"arguments": "42"
			}
		],
		"usage": {"input_tokens": 5, "output_tokens": 5, "total_tokens": 10,
			"input_tokens_details": {"cached_tokens": 0},
			"output_tokens_details": {"reasoning_tokens": 0}}
	}`)

	_, err := ParseResponseBody(body)
	if err == nil {
		t.Fatal("expected a refusal for an undecodable function_call arguments payload")
	}

	var tae *common.ToolArgumentsError
	if !errors.As(err, &tae) {
		t.Fatalf("error is not a *common.ToolArgumentsError: %v", err)
	}
	if tae.FinishReason != "" {
		t.Errorf("FinishReason = %q, want empty — content_filter is not truncation evidence", tae.FinishReason)
	}
	if tae.Truncated {
		t.Error("Truncated = true, want false — a content_filter incomplete reason is a refusal, not a cutoff")
	}
}
