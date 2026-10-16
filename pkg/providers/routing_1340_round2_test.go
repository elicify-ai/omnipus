package providers

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// Oracles: round2 CR R2-1, SFH R2-N2 and PTA F6. Availability must describe
// the model subject itself, not a capability mentioned elsewhere in a message.
func TestRouting1340_Round2AvailabilityAssertions(t *testing.T) {
	cases := []struct {
		name, message string
		wantBadModel  bool
	}{
		{"leading_article_available", "The model 'm' is not available.", true},
		{"leading_article_offered", "The model 'm' is not offered.", true},
		{"offered_by_provider", "Model 'm' is not offered by this provider.", true},
		{"available_on_provider", "Model 'm' is not available on this provider.", true},
		{"quoted_with_prefix", "Request rejected: the model \"m\" is not available.", true},
		{"unquoted_exact_model", "The model m is not available.", true},
		{"generic_model_subject", "The model is not available.", true},
		{"unquoted_other_identifier", "The model other is not available.", false},
		{"colon_image_subject", "Model 'm': image input is not available.", false},
		{"colon_without_punctuation", "Model 'm': image input is not available", false},
		{"separate_capability_subject", "Model 'm' image input is not available.", false},
		{"image_qualifier", "Model 'm' is not available for image input.", false},
		{"pdf_qualifier", "The model 'm' is not offered for PDF input.", false},
		{"request_qualifier", "Model 'm' is not available for this request.", false},
		{"tool_qualifier", "Model 'm' is not offered with tool calls.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{"error": map[string]any{"message": tc.message}})
			if err != nil {
				t.Fatal(err)
			}
			if got := IsBadModelError(400, string(body), "m"); got != tc.wantBadModel {
				t.Errorf("model-availability predicate=%t, want%t for %q", got, tc.wantBadModel, tc.message)
			}
			got := ClassifyError(&common.ProviderError{Status: 400, Body: string(body)}, "openrouter", "m")
			want := FailoverFormat
			if tc.wantBadModel {
				want = FailoverBadModel
			}
			if got == nil || got.Reason != want || got.Status != 400 || got.IsRetriable() {
				t.Errorf("routing=%+v, want non-retriable%s/status400", got, want)
			}
		})
	}
	// A genuine structured model-not-found verdict still outranks capability prose.
	if !IsBadModelError(400, `{"error":{"code":"model_not_found","message":"Model 'm': image input is not available."}}`, "m") {
		t.Error("structured wrong-model evidence lost its precedence")
	}
}
