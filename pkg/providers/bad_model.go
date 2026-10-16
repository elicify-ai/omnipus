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
	lower := strings.ToLower(strings.TrimSpace(body))
	// Availability with a capability qualifier is not an existence assertion.
	// Anchor the allowed endings instead of RE2-unsupported negative lookahead.
	for _, ending := range []string{" is not available", " is not offered"} {
		for _, suffix := range []string{"", ".", "!", " on this provider", " on this provider."} {
			if strings.HasSuffix(lower, ending+suffix) && modelMention(lower, model) {
				return true
			}
		}
	}
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

func modelMention(body, model string) bool {
	if strings.HasPrefix(body, "model '") || strings.HasPrefix(body, "model \"") || strings.HasPrefix(body, "model `") || strings.HasPrefix(body, "model is ") {
		return true
	}
	return model != "" && strings.HasPrefix(body, "model "+strings.ToLower(model)+" ")
}
