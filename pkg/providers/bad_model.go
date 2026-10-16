package providers

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

var badModelPatterns = []errorPattern{
	rxp("\\bmodel(?:\\s+[\"'`][-a-z0-9_./:@~+]+[\"'`])?\\s+(?:not found\\b|does not exist\\b)"),
	rxp(`\bnot a valid model (?:id|identifier)\b`),
}

// Require a model-led clause, optionally after a delimited explanation. Mere
// whitespace cannot start it: "input for this model" is an input restriction,
// not a model subject. Keep the predicate/end binding and optional article.
// The only capture is an unquoted identifier, which must equal the selected id.
var modelAvailabilityAssertion = regexp.MustCompile(
	"(?i)(?:^|[:;.!?][[:space:]]+)[[:space:]]*(?:the[[:space:]]+)?model(?:[[:space:]]+(?:[\"'`][-a-z0-9_./:@~+]+[\"'`]|([-a-z0-9_./:@~+]+)))?" +
		`[[:space:]]+is[[:space:]]+not[[:space:]]+(?:available|offered)(?:[[:space:]]+(?:on|by)[[:space:]]+this[[:space:]]+provider)?[.!]?[[:space:]]*$`,
)

// IsBadModelError requires positive wrong-model evidence on HTTP400. Input
// capability restrictions, missing model inputs/responses and404 retain their
// existing behavior. Both routing and terminal presentation use this predicate.
func IsBadModelError(status int, body, model string) bool {
	if status != 400 {
		return false
	}
	if common.StructuredErrorCode(body) == "model_not_found" {
		return true
	}
	// This is an upstream response body, not a gateway/SPA wire format.
	var envelope struct { // not-wire-format: vendor error inspection
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(body), &envelope) == nil && envelope.Error.Message != "" {
		body = envelope.Error.Message
	}
	if matchesAny(body, badModelPatterns) {
		return true
	}
	if assertion := modelAvailabilityAssertion.FindStringSubmatch(body); assertion != nil {
		if assertion[1] == "" || (model != "" && strings.EqualFold(assertion[1], strings.TrimSpace(model))) {
			return true
		}
	}
	lower := strings.ToLower(strings.TrimSpace(body))
	if model != "" {
		prefix := "model " + strings.ToLower(model) + " "
		for _, suffix := range []string{"not found", "does not exist"} {
			if strings.Contains(lower, prefix+suffix) {
				return true
			}
		}
	}
	return false
}
