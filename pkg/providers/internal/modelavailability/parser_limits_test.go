package modelavailability_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/internal/modelavailability"
)

// The documented grammar declines evidence-bearing/ambiguous explanations;
// an unsupported first clause must not be skipped to find a later model phrase.
func TestUnsupportedExplanationsDoNotAssertModelAbsence(t *testing.T) {
	for _, message := range []string{
		"Request could not be completed: model m is not available.",
		"Image input is not available: model m is not available.",
		"Model m is not available. Image input is not available.",
	} {
		t.Run(message, func(t *testing.T) {
			if modelavailability.Assertion(message, "m") {
				t.Errorf("unsupported/ambiguous diagnostic became model-absence evidence: %q", message)
			}
		})
	}
}
