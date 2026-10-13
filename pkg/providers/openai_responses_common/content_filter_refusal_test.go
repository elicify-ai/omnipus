package openai_responses_common

import (
	"errors"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// Provider hotfix H5 review (silent-failure F3): status "incomplete" with
// incomplete_details.reason "content_filter" is a content-policy refusal.
// Even when the response carries partial text, it must not be returned as a
// "length" answer — the agent would auto-continue a refused response.
func TestParseResponseBody_ContentFilterIncompleteIsAPolicyRefusal(t *testing.T) {
	body := strings.NewReader(`{
		"id": "resp_cf_text",
		"object": "response",
		"status": "incomplete",
		"incomplete_details": {"reason": "content_filter"},
		"output": [
			{"type": "message", "role": "assistant", "status": "completed",
			 "content": [{"type": "output_text", "text": "partial", "annotations": []}]}
		],
		"usage": {"input_tokens": 5, "output_tokens": 3, "total_tokens": 8,
			"input_tokens_details": {"cached_tokens": 0},
			"output_tokens_details": {"reasoning_tokens": 0}}
	}`)

	resp, err := ParseResponseBody(body)

	if err == nil {
		t.Fatalf("content_filter incomplete returned an answer (FinishReason %q); want a policy refusal", resp.FinishReason)
	}
	var spe *common.StreamProviderError
	if !errors.As(err, &spe) {
		t.Fatalf("error is not a *common.StreamProviderError: %v", err)
	}
	if spe.Kind() != common.StreamErrorContentPolicy {
		t.Errorf("Kind() = %v, want StreamErrorContentPolicy", spe.Kind())
	}
	if spe.Usage == nil || spe.Usage.TotalTokens != 8 {
		t.Errorf("Usage = %+v, want the response's usage (total 8)", spe.Usage)
	}
}
