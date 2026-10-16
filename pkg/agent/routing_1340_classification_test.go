package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// R3 plus the existing ADR-051 media-recovery contract: classify positive
// wrong-model evidence, never replace inconclusive400 during recovery, and
// preserve all existing auth/quota/rate/timeout/capability overrides.
func TestRouting1340_UserClassification(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   LLMErrorCode
	}{
		// Round1 architect D2: recovery remains inconclusive; the shared
		// permanent-failure gate blocks retry, and terminal presentation
		// classifies the original evidence as bad_model below.
		{"bad_model_code", 400, `{"error":{"code":"model_not_found"}}`, CodeUnknown},
		{"bad_model_message", 400, `{"error":{"message":"Model 'missing' not found"}}`, CodeUnknown},
		{"empty400_recovery_unchanged", 400, "", CodeUnknown},
		{"arbitrary400_recovery_unchanged", 400, `{"error":{"message":"account header missing"}}`, CodeUnknown},
		{"not_found_other_resource", 400, `{"error":{"message":"file not found for model input"}}`, CodeUnknown},
		{"model_response_missing", 400, `{"error":{"message":"model response not found"}}`, CodeUnknown},
		{"model_input_missing", 400, `{"error":{"message":"model input not found"}}`, CodeUnknown},
		{"billing_override", 400, `{"error":{"message":"credit balance is too low"}}`, CodeQuotaBilling},
		{"quota429", 429, `{"error":{"code":"insufficient_quota"}}`, CodeQuotaBilling},
		{"auth401", 401, `{"error":{"code":"model_not_found"}}`, CodeProviderAuthFailed},
		{"auth403", 403, "", CodeProviderAuthFailed},
		{"billing402", 402, "", CodeQuotaBilling},
		{"rate429", 429, `{"error":{"code":"model_not_found"}}`, CodeRateLimited},
		{"timeout408", 408, "", CodeNetwork},
		{"upstream500", 500, `{"error":{"code":"model_not_found"}}`, CodeNetwork},
		{"media_override", 400, `{"error":{"message":"image input not supported"}}`, CodeMediaUnsupported},
		{"policy_override", 400, `{"error":{"message":"content policy violation"}}`, CodeContentPolicy},
		{"context_override", 400, `{"error":{"message":"context length exceeded"}}`, CodeContextTooLong},
		{"ordinary404_unchanged", 404, `{"error":{"code":"model_not_found"}}`, CodeUnknown},
		{"retired404_unchanged", 404, `{"error":{"message":"model has been decommissioned"}}`, CodeModelRetired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TranslateLLMError(&ProviderError{Status: tc.status, Body: tc.body}, "")
			if got.Code != tc.want {
				t.Fatalf("user classification status%d=%q, want%q", tc.status, got.Code, tc.want)
			}
			if tc.name == "bad_model_code" || tc.name == "bad_model_message" {
				terminal := TranslateTurnError(&common.ProviderError{Status: tc.status, Body: tc.body})
				if terminal.Code != CodeBadModel || terminal.Retryable {
					t.Fatalf("terminal model evidence lost: %+v", terminal)
				}
				if outcomeFallbackEligible(&ProviderError{Status: tc.status, Body: tc.body}, got.Code) {
					t.Fatal("wrong-model evidence must still refuse the media fallback gate")
				}
			}
		})
	}
}
