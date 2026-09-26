package common

// error_code.go — the provider's structured error code, shared by BOTH
// classifiers (routing: pkg/providers C-5 billing; user-side:
// pkg/agent quota_billing and model_retired detectors — provider-messages
// spec §7.3). One extraction, two consumers, one vocabulary.

import (
	"encoding/json"
	"strings"
)

// StructuredErrorCode extracts the provider's structured error code from an
// error body: the "code" member of the top-level error object
// ({"error":{"code":"insufficient_quota", ...}}). Returns "" when the body
// is not JSON, the envelope is a different shape, or the code member is not
// a string (some providers send numbers — ignored by design).
func StructuredErrorCode(body string) string {
	var envelope struct {
		Error struct {
			Code any `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		return ""
	}
	s, _ := envelope.Error.Code.(string)
	// Gate finding F5: the routing classifier lowercases the body before
	// this call (pkg/providers/error_classifier.go::ClassifyError), the
	// user-side one (pkg/agent/translate_error_provider_messages.go::
	// isBillingEvidence) did not — a mixed-case structured code
	// ("Insufficient_Quota") split the two classifiers on the same body.
	// Normalize here, at the single point both classifiers share.
	return strings.ToLower(s)
}
