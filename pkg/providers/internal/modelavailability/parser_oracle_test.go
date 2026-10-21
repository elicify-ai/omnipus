package modelavailability_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/internal/modelavailability"
)

// TestAvailabilitySubjectOracle copies the independent QA-authored prose table
// without changing expected values. The two structured-code rows remain in the
// original provider/agent tests: code-envelope precedence belongs to their caller,
// not this pure prose parser. All original QA and prior regression files remain intact.
func TestAvailabilitySubjectOracle(t *testing.T) {
	cases := []struct {
		name, message, model string
		want                 bool
	}{
		{"bare_unquoted", "Model m is not available.", "m", true},
		{"bare_unquoted_no_period", "Model m is not available", "m", true},
		{"article_generic", "The model is not available.", "m", true},
		{"this_determiner", "This model is not available.", "m", true},
		{"requested_unquoted", "The requested model m is not available.", "m", true},
		{"a_determiner_quoted", "A model 'm' is not available.", "m", true},
		{"requested_quoted", "The requested model 'm' is not available.", "m", true},
		{"offered_by_provider_unquoted", "Model m is not offered by this provider.", "m", true},
		{"article_quoted_offered_by_provider", "The model 'm' is not offered by this provider.", "m", true},
		{"prefix_colon_no_space", "Request rejected:model m is not available.", "m", true},
		{"prefix_colon_spaced", "Request rejected: model m is not available.", "m", true},
		{"prefix_colon_quoted", "Request rejected: the model \"m\" is not available.", "m", true},
		{"prefix_semicolon", "Request rejected; model 'm' is not available.", "m", true},
		{"prefix_sentence", "Request rejected. The model 'm' is not available.", "m", true},
		{"prefix_exclamation", "Request rejected! The model 'm' is not available.", "m", true},
		{"prefix_question", "Request rejected? The model 'm' is not available.", "m", true},
		{"newline_after_colon", "Request rejected:\nThe model 'm' is not offered by this provider.", "m", true},
		{"leading_trailing_whitespace", "\tThe model 'm' is not available.\n", "m", true},
		{"opaque_id_punctuation", "Model vendor/model.v1:tag is not available.", "vendor/model.v1:tag", true},
		{"unquoted_identity_hit", "Model input is not available.", "input", true},
		{"quoted_id_opaque", "Model 'image-input.v1:tag' is not available.", "image-input.v1:tag", true},
		{"image_subject_bare", "Image input is not available.", "m", false},
		{"image_colon_spaced", "Model m: image input is not available.", "m", false},
		{"image_colon_no_space", "Model m:image input is not available.", "m", false},
		{"image_prefix_no_space", "Request rejected:image input is not available.", "m", false},
		{"image_input_first_unquoted", "Image input for model m is not available.", "m", false},
		{"image_input_first_quoted", "Image input for model 'm' is not available.", "m", false},
		{"image_input_first_this_model", "Image input for this model is not available.", "m", false},
		{"pdf_input_first_this_model", "PDF input for this model is not available.", "m", false},
		{"pdf_input_first_quoted", "PDF input for model 'm' is not available.", "m", false},
		{"pdf_colon_no_space", "Model m:PDF input is not available.", "m", false},
		{"image_model_qualifier", "Model m is not available for image input.", "m", false},
		{"pdf_article_qualifier", "The model 'm' is not offered for PDF input.", "m", false},
		{"image_fronted_qualifier", "For image input, model 'm' is not available.", "m", false},
		{"request_subject", "This request is not available.", "m", false},
		{"requested_input_subject", "The requested input is not available.", "m", false},
		{"model_response_not_found", "Model response not found.", "m", false},
		{"model_input_not_found", "Model input not found.", "m", false},
		{"unquoted_identity_miss", "Model input is not available.", "m", false},
		{"unquoted_other_identifier", "The model other is not available.", "m", false},
		{"available_without_negation", "Model m is available.", "m", false},
		{"typo_predicate", "Model m is not availble.", "m", false},
		{"different_verb", "Model m did not respond.", "m", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := modelavailability.Assertion(tc.message, tc.model); got != tc.want {
				t.Errorf("subject assertion=%t, want%t for selected model %q and message %q", got, tc.want, tc.model, tc.message)
			}
		})
	}
}
