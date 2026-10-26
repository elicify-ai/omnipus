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
		// Round3 controls: a real model-subject clause may follow an explicit
		// explanation/sentence boundary, never mere whitespace in input prose.
		{"article_offered_by_provider", "The model 'm' is not offered by this provider.", true},
		{"semicolon_model_clause", "Request rejected; model 'm' is not available.", true},
		{"sentence_model_clause", "Request rejected. The model 'm' is not available.", true},
		{"exclamation_model_clause", "Request rejected! The model 'm' is not available.", true},
		{"question_model_clause", "Request rejected? The model 'm' is not available.", true},
		{"newline_after_colon", "Request rejected:\nThe model 'm' is not offered by this provider.", true},
		{"leading_whitespace", "\tThe model 'm' is not available.\n", true},
		{"unquoted_other_identifier", "The model other is not available.", false},
		{"input_first_image_quoted", "Image input for model 'm' is not available.", false},
		{"input_first_image_this_model", "Image input for this model is not available.", false},
		{"input_first_image_unquoted", "Image input for model m is not available.", false},
		{"input_first_pdf_quoted", "PDF input for model 'm' is not available.", false},
		{"input_first_pdf_this_model", "PDF input for this model is not available.", false},
		{"input_first_pdf_unquoted", "PDF input for model m is not available.", false},
		{"prefixed_input_first_image", "Request rejected: image input for model 'm' is not available.", false},
		{"prefixed_input_first_pdf", "Request rejected: PDF input for this model is not offered by this provider.", false},
		{"fronted_input_qualifier", "For image input, model 'm' is not available.", false},
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
			if got == nil || got.Reason != want || got.Status != 400 {
				t.Errorf("routing=%+v, want %s/status400", got, want)
			}
			// Spec change (founder Q70, 2026-10-24): bad_model is now
			// retriable (falls back to the backup); format stays terminal.
			// The classification subject oracle above is unchanged.
			if wantRetriable := tc.wantBadModel; got != nil && got.IsRetriable() != wantRetriable {
				t.Errorf("retriability=%t, want %t for %q", got.IsRetriable(), wantRetriable, tc.message)
			}
		})
	}
	// A genuine structured model-not-found verdict still outranks capability prose.
	if !IsBadModelError(400, `{"error":{"code":"model_not_found","message":"Model 'm': image input is not available."}}`, "m") {
		t.Error("structured wrong-model evidence lost its precedence")
	}
	t.Run("structured_input_first_remains_authoritative", func(t *testing.T) {
		body := `{"error":{"code":"model_not_found","message":"Image input for this model is not available."}}`
		got := ClassifyError(&common.ProviderError{Status: 400, Body: body}, "openrouter", "m")
		// Spec change (founder Q70, 2026-10-24): the verdict is still
		// bad_model, and it is now retriable; OLD: non-retriable.
		if !IsBadModelError(400, body, "m") || got == nil || got.Reason != FailoverBadModel || got.Status != 400 || !got.IsRetriable() {
			t.Errorf("structured model absence lost to input-first prose: %+v", got)
		}
	})
}
