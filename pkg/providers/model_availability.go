package providers

import "github.com/elicify-ai/omnipus/pkg/providers/internal/modelavailability"

// modelAvailabilityAssertion uses the pure subject/token parser. Its supported
// grammar and conservative fallback limits are documented at that implementation;
// keeping it independent of SDK adapters permits a small unit/mutation binary.
func modelAvailabilityAssertion(message, model string) bool {
	return modelavailability.Assertion(message, model)
}
