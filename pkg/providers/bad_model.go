package providers

import (
	"encoding/json"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

var badModelPatterns = []errorPattern{
	rxp("\\bmodel(?:\\s+[\"'`][-a-z0-9_./:@~+]+[\"'`])?\\s+(?:not found\\b|does not exist\\b)"),
	rxp(`\bnot a valid model (?:id|identifier)\b`),
}

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
	if modelAvailabilityAssertion(body, model) {
		return true
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
