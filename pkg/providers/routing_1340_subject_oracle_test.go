package providers

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// Round-4 subject-kind oracle (issue #1340). Derived from the round-4
// directive BEFORE the structured availability parser exists: classification
// must follow the SUBJECT of the availability sentence —
//
//   - the model itself is the subject        → bad_model (terminal),
//   - an input type (image/PDF) is subject   → capability, never bad_model,
//   - neither is the subject                 → format/unknown as existing,
//   - a structured model_not_found code      → always authoritative.
//
// Never by whether the word "model" occurs. The five baseline-red literals
// (this / requested / a / quoted-requested / no-space colon prefix) are marked
// in the table; the remaining rows are preservation controls that also pin the
// identifier policy: punctuation inside a real id is not a clause boundary,
// unquoted ids must equal the selected id exactly, quoted ids stay opaque.
//
// Assertions cover the public predicate IsBadModelError AND the public router
// ClassifyError (reason/status/identity/non-retriability) — never the private
// expression a future parser replaces.
func TestRouting1340_AvailabilitySubjectOracle(t *testing.T) {
	cases := []struct {
		name, message string
		model         string
		code          string // optional structured error code riding the body
		wantBadModel  bool
	}{
		// ── Model absence: the model is the unavailable subject. ──────────
		{"bare_unquoted", "Model m is not available.", "m", "", true},
		{"bare_unquoted_no_period", "Model m is not available", "m", "", true},
		{"article_generic", "The model is not available.", "m", "", true},
		{"this_determiner", "This model is not available.", "m", "", true},               // RED on baseline regex
		{"requested_unquoted", "The requested model m is not available.", "m", "", true}, // RED on baseline regex
		{"a_determiner_quoted", "A model 'm' is not available.", "m", "", true},          // RED on baseline regex
		{"requested_quoted", "The requested model 'm' is not available.", "m", "", true}, // RED on baseline regex
		{"offered_by_provider_unquoted", "Model m is not offered by this provider.", "m", "", true},
		{"article_quoted_offered_by_provider", "The model 'm' is not offered by this provider.", "m", "", true},
		{"prefix_colon_no_space", "Request rejected:model m is not available.", "m", "", true}, // RED on baseline regex
		{"prefix_colon_spaced", "Request rejected: model m is not available.", "m", "", true},
		{"prefix_colon_quoted", `Request rejected: the model "m" is not available.`, "m", "", true},
		{"prefix_semicolon", "Request rejected; model 'm' is not available.", "m", "", true},
		{"prefix_sentence", "Request rejected. The model 'm' is not available.", "m", "", true},
		{"prefix_exclamation", "Request rejected! The model 'm' is not available.", "m", "", true},
		{"prefix_question", "Request rejected? The model 'm' is not available.", "m", "", true},
		{"newline_after_colon", "Request rejected:\nThe model 'm' is not offered by this provider.", "m", "", true},
		{"leading_trailing_whitespace", "\tThe model 'm' is not available.\n", "m", "", true},
		// Identifier policy (user round-4 addition; provider-only controls).
		// Punctuation inside an actual identifier is opaque id text, never a
		// clause boundary; an unquoted id must EQUAL the selected id; a
		// quoted id is always an opaque model identifier, not an input name.
		{"opaque_id_punctuation", "Model vendor/model.v1:tag is not available.", "vendor/model.v1:tag", "", true},
		{"unquoted_identity_hit", "Model input is not available.", "input", "", true},
		{"quoted_id_opaque", "Model 'image-input.v1:tag' is not available.", "image-input.v1:tag", "", true},

		// ── Input-type capability: an input type is the unavailable subject.
		{"image_subject_bare", "Image input is not available.", "m", "", false},
		{"image_colon_spaced", "Model m: image input is not available.", "m", "", false},
		{"image_colon_no_space", "Model m:image input is not available.", "m", "", false},
		{"image_prefix_no_space", "Request rejected:image input is not available.", "m", "", false},
		{"image_input_first_unquoted", "Image input for model m is not available.", "m", "", false},
		{"image_input_first_quoted", "Image input for model 'm' is not available.", "m", "", false},
		{"image_input_first_this_model", "Image input for this model is not available.", "m", "", false},
		{"pdf_input_first_this_model", "PDF input for this model is not available.", "m", "", false},
		{"pdf_input_first_quoted", "PDF input for model 'm' is not available.", "m", "", false},
		{"pdf_colon_no_space", "Model m:PDF input is not available.", "m", "", false},
		{"image_model_qualifier", "Model m is not available for image input.", "m", "", false},
		{"pdf_article_qualifier", "The model 'm' is not offered for PDF input.", "m", "", false},
		{"image_fronted_qualifier", "For image input, model 'm' is not available.", "m", "", false},

		// ── Everything else: no model-absence evidence at all. A model phrase
		// in the sentence must never make these terminal as bad_model.
		{"request_subject", "This request is not available.", "m", "", false},
		{"requested_input_subject", "The requested input is not available.", "m", "", false},
		{"model_response_not_found", "Model response not found.", "m", "", false},
		{"model_input_not_found", "Model input not found.", "m", "", false},
		{"unquoted_identity_miss", "Model input is not available.", "m", "", false},
		{"unquoted_other_identifier", "The model other is not available.", "m", "", false},
		{"available_without_negation", "Model m is available.", "m", "", false},
		{"typo_predicate", "Model m is not availble.", "m", "", false},
		{"different_verb", "Model m did not respond.", "m", "", false},

		// ── Structured controls: a model_not_found code outranks capability
		// prose riding the same body.
		{"structured_image_prose", "Model m: image input is not available.", "m", "model_not_found", true},
		{"structured_pdf_prose_no_space", "Model m:PDF input is not available.", "m", "model_not_found", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := routing1340SubjectBody(t, tc.code, tc.message)
			if got := IsBadModelError(400, body, tc.model); got != tc.wantBadModel {
				t.Errorf("IsBadModelError=%t, want %t (selected model %q) for %q", got, tc.wantBadModel, tc.model, tc.message)
			}
			got := ClassifyError(&common.ProviderError{Status: 400, Body: body}, "openrouter", tc.model)
			if tc.wantBadModel {
				if got == nil || got.Reason != FailoverBadModel {
					t.Errorf("ClassifyError=%+v, want bad_model for %q", got, tc.message)
				}
			} else if got == nil || got.Reason != FailoverFormat {
				t.Errorf("ClassifyError=%+v, want format (never bad_model) for %q", got, tc.message)
			}
			if got == nil {
				t.Fatal("ClassifyError returned nil for a 400 provider error")
			}
			if got.Status != 400 || got.IsRetriable() {
				t.Errorf("ClassifyError status=%d retriable=%t, want status 400 and non-retriable for %q", got.Status, got.IsRetriable(), tc.message)
			}
			if got.Provider != "openrouter" || got.Model != tc.model {
				t.Errorf("ClassifyError identity=%q/%q, want openrouter/%q (failing attempt must keep its identity)", got.Provider, got.Model, tc.model)
			}
		})
	}
}

// routing1340SubjectBody renders the upstream error envelope the predicate and
// the classifier both read. An empty code renders the plain message envelope;
// a code renders the structured body used by the precedence controls.
func routing1340SubjectBody(t *testing.T, code, message string) string {
	t.Helper()
	errObj := map[string]any{"message": message}
	if code != "" {
		errObj["code"] = code
	}
	body, err := json.Marshal(map[string]any{"error": errObj})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
