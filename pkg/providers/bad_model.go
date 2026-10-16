package providers

import "github.com/elicify-ai/omnipus/pkg/providers/common"

var badModelPatterns = []errorPattern{
	rxp("\\bmodel(?:\\s+[\"'`]?[-a-z0-9_./:@~+]+[\"'`]?)?\\s+(?:not found\\b|does not exist\\b|is not (?:available|offered)\\b)"),
	rxp(`\bnot a valid model (?:id|identifier)\b`),
}

// IsBadModelError requires positive wrong-model evidence on HTTP400. A bare
// Bad Request, unsupported model capability, or another not-found resource is
// not evidence the model is unavailable. In particular404 keeps its established
// fallback/retirement handling. Shared by routing and user-facing classifiers.
func IsBadModelError(status int, body string) bool {
	return status == 400 && (common.StructuredErrorCode(body) == "model_not_found" || matchesAny(body, badModelPatterns))
}
