package providers

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

var badModelPatterns = []errorPattern{
	rxp("\\bmodel(?:\\s+[\"'`][-a-z0-9_./:@~+]+[\"'`])?\\s+(?:not found\\b|does not exist\\b|is not (?:available|offered)\\b)"),
	rxp(`\bnot a valid model (?:id|identifier)\b`),
}

// IsBadModelError requires positive wrong-model evidence on HTTP400. A bare
// Bad Request, unsupported model capability, or another not-found resource is
// not evidence the model is unavailable. In particular404 keeps its established
// fallback/retirement handling. Shared by routing and user-facing classifiers.
func IsBadModelError(status int, body, model string) bool {
	if status != 400 {
		return false
	}
	if common.StructuredErrorCode(body) == "model_not_found" || matchesAny(body, badModelPatterns) {
		return true
	}
	// An unquoted word after "model" may be a resource, e.g. "model input
	// not found". Only accept an unquoted id when it names this request's model.
	if model != "" {
		lower := strings.ToLower(body)
		prefix := "model " + strings.ToLower(model) + " "
		for _, suffix := range []string{"not found", "does not exist", "is not available", "is not offered"} {
			if strings.Contains(lower, prefix+suffix) {
				return true
			}
		}
	}
	return false
}
