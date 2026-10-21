package modelavailability_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/internal/modelavailability"
)

// A configured identifier is evidence at the identifier slot, even when its
// punctuation is also valid clause syntax. The subject remains the model;
// punctuation must not turn the ID into an explanatory heading.
func TestConfiguredIdentifierTakesPrecedenceOverClauseSyntax(t *testing.T) {
	for _, model := range []string{":m", ".m", "vendor/model.v1:tag", "is"} {
		t.Run(model, func(t *testing.T) {
			message := "Model " + model + " is not available."
			if !modelavailability.Assertion(message, model) {
				t.Errorf("configured model ID %q was reinterpreted as clause syntax in %q", model, message)
			}
		})
	}
}
