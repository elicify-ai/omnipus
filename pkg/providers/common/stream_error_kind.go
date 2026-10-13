package common

import (
	"strconv"
	"strings"
)

// StreamErrorKind says what kind of failure a provider reported after its 200
// was committed. It decides retry and the words the person reads: a billing or
// auth failure must not be retried (every attempt is billed and fails the same
// way) nor shown as a network fault.
type StreamErrorKind int

const (
	// StreamErrorTransient: a server-side or upstream hiccup (server_error,
	// overloaded, timeout, 5xx, a dropped upstream). Safe to retry.
	StreamErrorTransient StreamErrorKind = iota
	// StreamErrorRateLimit: 429 / rate_limit. Left to the fallback chain.
	StreamErrorRateLimit
	// StreamErrorBilling: 402 / insufficient credit or quota.
	StreamErrorBilling
	// StreamErrorAuth: 401 / 403 / invalid or missing credentials.
	StreamErrorAuth
	// StreamErrorInvalidRequest: 400 / 422 / the provider rejects the request.
	StreamErrorInvalidRequest
	// StreamErrorContentPolicy: moderation / content policy.
	StreamErrorContentPolicy
	// StreamErrorOther: a code this table does not know. Not retried.
	StreamErrorOther
)

// Kind classifies the error by the provider's own code first, its message
// second. An empty or generic code ("", "error", "incomplete_stream") is
// transient: nothing says otherwise.
func (e *StreamProviderError) Kind() StreamErrorKind {
	code := strings.ToLower(strings.TrimSpace(e.Code))
	msg := strings.ToLower(e.Message)
	if n, err := strconv.Atoi(code); err == nil {
		switch {
		case n == 402:
			return StreamErrorBilling
		case n == 401 || n == 403:
			return StreamErrorAuth
		case n == 429:
			return StreamErrorRateLimit
		case n == 400 || n == 404 || n == 413 || n == 422:
			return StreamErrorInvalidRequest
		case n == 408 || n == 409 || n >= 500:
			return StreamErrorTransient
		}
	}
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(code, w) || strings.Contains(msg, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("insufficient_quota", "insufficient_credit", "insufficient credit", "billing", "payment required", "out of credit", "credit balance"):
		return StreamErrorBilling
	case has("invalid_api_key", "authentication", "unauthorized", "permission_denied", "permission_error", "invalid api key", "forbidden"):
		return StreamErrorAuth
	case has("content_policy", "content_filter", "moderation", "safety", "policy violation"):
		return StreamErrorContentPolicy
	case has("rate_limit", "rate limit", "too many requests"):
		return StreamErrorRateLimit
	case has("invalid_request", "invalid request", "unprocessable", "bad request", "context_length", "unsupported"):
		return StreamErrorInvalidRequest
	}
	switch code {
	case "", "error", "incomplete_stream", "server_error", "internal_error", "api_error", "overloaded", "overloaded_error",
		"timeout", "upstream_error", "service_unavailable", "unavailable", "bad_gateway", "gateway_timeout", "provider_error":
		return StreamErrorTransient
	}
	if has("disconnected", "overloaded", "unavailable", "timed out", "timeout", "upstream", "internal server error", "temporarily") {
		return StreamErrorTransient
	}
	return StreamErrorOther
}
