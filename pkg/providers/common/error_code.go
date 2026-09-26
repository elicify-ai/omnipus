package common

// error_code.go — the provider's structured error code, shared by BOTH
// classifiers (routing: pkg/providers C-5 billing; user-side:
// pkg/agent quota_billing and model_retired detectors — provider-messages
// spec §7.3). One extraction, two consumers, one vocabulary.

import "encoding/json"

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
	return s
}
