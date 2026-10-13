package common

import "testing"

func TestStreamProviderError_Kind(t *testing.T) {
	for _, tc := range []struct {
		code, msg string
		want      StreamErrorKind
	}{
		{"server_error", "Provider disconnected unexpectedly", StreamErrorTransient},
		{"", "", StreamErrorTransient},
		{"null", "Provider returned error", StreamErrorTransient}, // R3-SRV-SFH-04: a JSON null that reached Kind as text
		{"0", "Provider returned error", StreamErrorTransient},    // R4-SRV-SFH-01: a numeric 0 is an absent code, like null
		{"0", "Insufficient credits", StreamErrorBilling},         // control: a zero code does not hide what the message says
		{"overloaded_error", "Overloaded", StreamErrorTransient},
		{"incomplete_stream", "the stream ended before message_stop", StreamErrorTransient},
		{"503", "unavailable", StreamErrorTransient},
		{"402", "Insufficient credits", StreamErrorBilling},
		{"insufficient_quota", "You exceeded your current quota", StreamErrorBilling},
		{"401", "invalid key", StreamErrorAuth},
		{"authentication_error", "invalid x-api-key", StreamErrorAuth},
		{"403", "Forbidden", StreamErrorAuth},
		{"429", "slow down", StreamErrorRateLimit},
		{"rate_limit_exceeded", "", StreamErrorRateLimit},
		{"400", "bad", StreamErrorInvalidRequest},
		{"invalid_request_error", "messages: field required", StreamErrorInvalidRequest},
		{"content_policy_violation", "blocked", StreamErrorContentPolicy},
		{"moderation", "flagged", StreamErrorContentPolicy},
		{"some_new_code", "something odd", StreamErrorOther},
	} {
		got := (&StreamProviderError{Code: tc.code, Message: tc.msg}).Kind()
		if got != tc.want {
			t.Errorf("Kind(%q, %q) = %d, want %d", tc.code, tc.msg, got, tc.want)
		}
	}
}
