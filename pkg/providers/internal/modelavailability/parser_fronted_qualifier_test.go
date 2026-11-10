package modelavailability_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/internal/modelavailability"
)

// Round-5 R5-1 (issue #1340): a FRONTED clause that qualifies the request —
// attachments, input type, image/PDF, files, tools or another request-shape
// condition — makes the following availability sentence conditional. A
// conditional "model is not available" is a capability/format verdict, never
// unconditional model absence, so it must not assert bad_model. Only genuinely
// neutral fronted phrases ("Sorry,", "Error:", "Request rejected:") may be
// skipped to reach the model subject.
//
// Expected values come from the R5-1 directive, not from the parser.
func TestFrontedQualifierIsNotModelAbsence(t *testing.T) {
	for _, tc := range []struct{ name, message string }{
		{"with_attachments", "With attachments, model m is not available."},
		{"for_image_input_unquoted", "For image input, model m is not available."},
		{"when_tools_enabled_generic", "When tools are enabled, the model is not available."},
		{"with_pdf_files", "With PDF files, the requested model m is not available."},
		{"for_file_uploads", "For file uploads, model 'm' is not available."},
		{"if_tools_are_used", "If tools are used: model m is not offered by this provider."},
		{"neutral_then_qualifier", "Sorry, with attachments, model m is not available."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if modelavailability.Assertion(tc.message, "m") {
				t.Errorf("fronted request-shape qualifier was discarded; conditional availability became model absence: %q", tc.message)
			}
		})
	}
}

// Preservation controls: neutral fronted phrases are still skipped, so the
// unconditional model-absence verdict is kept.
func TestNeutralFrontedPhraseStillAssertsModelAbsence(t *testing.T) {
	for _, tc := range []struct{ name, message string }{
		{"sorry_comma", "Sorry, model m is not available."},
		{"error_colon", "Error: model m is not available."},
		{"request_rejected_colon", "Request rejected: model m is not available."},
		{"sorry_generic_article", "Sorry, the model is not available."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !modelavailability.Assertion(tc.message, "m") {
				t.Errorf("neutral fronted phrase no longer reaches the model-absence subject: %q", tc.message)
			}
		})
	}
}
