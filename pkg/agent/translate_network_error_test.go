package agent

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// The founder's preview (2026-10-12) showed "This turn didn't finish, and we
// can't tell why" five times, parent and helper sessions. The gateway log shows
// the real cause each time: a transport failure to the provider with no HTTP
// status ("http2: client connection lost" after a PROTOCOL_ERROR; "dial tcp:
// lookup openrouter.ai: no such host"). classifyByMessage knew rate limits and
// context overflow but no network vocabulary, so these fell to CodeUnknown. The
// strings below are the real log lines.
func TestTranslateTurnError_TransportFailuresAreNamedNotUnknown(t *testing.T) {
	const prefix = `LLM call failed after retries: fallback: unclassified error from openrouter/deepseek/deepseek-v4.1-flash: failed to send request: Post "https://openrouter.ai/api/v1/chat/completions": `
	for _, tail := range []string{
		"http2: client connection lost",
		"dial tcp: lookup openrouter.ai: no such host",
		"dial tcp: lookup openrouter.ai: i/o timeout",
		"stream error: stream ID 89; PROTOCOL_ERROR; received from peer",
		"dial tcp 104.18.2.115:443: connect: connection refused",
		"dial tcp: network is unreachable",
	} {
		t.Run(tail, func(t *testing.T) {
			got := TranslateTurnError(errors.New(prefix + tail))
			assert.Equal(t, CodeNetwork, got.Code, "a transport failure must be named, not 'we can't tell why'")
			assert.Contains(t, got.Message, "couldn’t reach the model provider")
			assert.NotContains(t, got.Message, "can’t tell why")
			for _, internal := range []string{"openrouter.ai", "lookup", "dial", "http2", "deepseek", "stream ID"} {
				assert.NotContains(t, got.Message, internal, "the user-facing text must carry no internals")
			}
			assert.True(t, got.Retryable)
		})
	}
}

// A provider that ended its response with an error after the 200 (#1278) is
// the same family for the person: retry.
func TestTranslateTurnError_MidStreamProviderErrorIsNamed(t *testing.T) {
	err := fmt.Errorf("LLM call failed: %w", &common.StreamProviderError{Code: "server_error", Message: "Provider disconnected unexpectedly"})
	got := TranslateTurnError(err)
	assert.Equal(t, CodeNetwork, got.Code)
	assert.NotContains(t, got.Message, "can’t tell why")
	assert.False(t, strings.Contains(got.Message, "Provider disconnected"), "raw provider text must not reach the person")
	assert.True(t, got.Retryable)
}

// Errors that are not transport failures still fall to the generic text.
func TestTranslateTurnError_UnrelatedErrorStaysUnknown(t *testing.T) {
	got := TranslateTurnError(errors.New("something nobody anticipated"))
	assert.Equal(t, CodeUnknown, got.Code)
}

// R2-S1-03: the words follow the provider's own code, not one network text.
func TestTranslateTurnError_MidStreamErrorFollowsItsCode(t *testing.T) {
	for _, tc := range []struct {
		code, msg string
		want      LLMErrorCode
		retryable bool
	}{
		{"server_error", "RAWPROVIDERTEXT", CodeNetwork, true},
		{"402", "RAWPROVIDERTEXT", CodeQuotaBilling, false},
		{"401", "RAWPROVIDERTEXT", CodeProviderAuthFailed, false},
		{"429", "RAWPROVIDERTEXT", CodeRateLimited, true},
		{"invalid_request_error", "RAWPROVIDERTEXT", CodeProviderRejected, false},
		{"content_policy_violation", "RAWPROVIDERTEXT", CodeContentPolicy, false},
		{"some_new_code", "RAWPROVIDERTEXT", CodeUnknown, false},
	} {
		got := TranslateTurnError(fmt.Errorf("x: %w", &common.StreamProviderError{Code: tc.code, Message: tc.msg}))
		assert.Equal(t, tc.want, got.Code, "code %q", tc.code)
		assert.NotContains(t, got.Message, "RAWPROVIDERTEXT", "raw provider text must not reach the person")
	}
}
