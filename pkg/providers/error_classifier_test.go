package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
)

func TestClassifyError_Nil(t *testing.T) {
	result := ClassifyError(nil, "openai", "gpt-4")
	if result != nil {
		t.Errorf("expected nil for nil error, got %+v", result)
	}
}

func TestClassifyError_ContextCanceled(t *testing.T) {
	result := ClassifyError(context.Canceled, "openai", "gpt-4")
	if result != nil {
		t.Errorf("expected nil for context.Canceled (user abort), got %+v", result)
	}
}

func TestClassifyError_ContextDeadlineExceeded(t *testing.T) {
	result := ClassifyError(context.DeadlineExceeded, "openai", "gpt-4")
	if result == nil {
		t.Fatal("expected non-nil for deadline exceeded")
	}
	if result.Reason != FailoverTimeout {
		t.Errorf("reason = %q, want timeout", result.Reason)
	}
}

func TestClassifyError_StatusCodes(t *testing.T) {
	tests := []struct {
		status int
		reason FailoverReason
	}{
		{401, FailoverAuth},
		{403, FailoverAuth},
		{402, FailoverBilling},
		{408, FailoverTimeout},
		{429, FailoverRateLimit},
		{400, FailoverFormat},
		{500, FailoverTimeout},
		{502, FailoverTimeout},
		{503, FailoverTimeout},
		{521, FailoverTimeout},
		{522, FailoverTimeout},
		{523, FailoverTimeout},
		{524, FailoverTimeout},
		{529, FailoverTimeout},
	}

	for _, tt := range tests {
		err := fmt.Errorf("API error: status: %d something went wrong", tt.status)
		result := ClassifyError(err, "test", "model")
		if result == nil {
			t.Errorf("status %d: expected non-nil", tt.status)
			continue
		}
		if result.Reason != tt.reason {
			t.Errorf("status %d: reason = %q, want %q", tt.status, result.Reason, tt.reason)
		}
	}
}

func TestClassifyError_RateLimitPatterns(t *testing.T) {
	patterns := []string{
		"rate limit exceeded",
		"rate_limit reached",
		"too many requests",
		"exceeded your current quota",
		"resource has been exhausted",
		"resource_exhausted",
		"quota exceeded",
		"usage limit reached",
	}

	for _, msg := range patterns {
		err := errors.New(msg)
		result := ClassifyError(err, "openai", "gpt-4")
		if result == nil {
			t.Errorf("pattern %q: expected non-nil", msg)
			continue
		}
		if result.Reason != FailoverRateLimit {
			t.Errorf("pattern %q: reason = %q, want rate_limit", msg, result.Reason)
		}
	}
}

func TestClassifyError_OverloadedPatterns(t *testing.T) {
	patterns := []string{
		"overloaded_error",
		`{"type": "overloaded_error"}`,
		"server is overloaded",
	}

	for _, msg := range patterns {
		err := errors.New(msg)
		result := ClassifyError(err, "anthropic", "claude")
		if result == nil {
			t.Errorf("pattern %q: expected non-nil", msg)
			continue
		}
		// Overloaded is treated as rate_limit
		if result.Reason != FailoverRateLimit {
			t.Errorf("pattern %q: reason = %q, want rate_limit", msg, result.Reason)
		}
	}
}

func TestClassifyError_BillingPatterns(t *testing.T) {
	patterns := []string{
		"payment required",
		"insufficient credits",
		"credit balance too low",
		"plans & billing page",
		"insufficient balance",
	}

	for _, msg := range patterns {
		err := errors.New(msg)
		result := ClassifyError(err, "openai", "gpt-4")
		if result == nil {
			t.Errorf("pattern %q: expected non-nil", msg)
			continue
		}
		if result.Reason != FailoverBilling {
			t.Errorf("pattern %q: reason = %q, want billing", msg, result.Reason)
		}
	}
}

func TestClassifyError_TimeoutPatterns(t *testing.T) {
	patterns := []string{
		"request timeout",
		"connection timed out",
		"deadline exceeded",
		"context deadline exceeded",
	}

	for _, msg := range patterns {
		err := errors.New(msg)
		result := ClassifyError(err, "openai", "gpt-4")
		if result == nil {
			t.Errorf("pattern %q: expected non-nil", msg)
			continue
		}
		if result.Reason != FailoverTimeout {
			t.Errorf("pattern %q: reason = %q, want timeout", msg, result.Reason)
		}
	}
}

func TestClassifyError_AuthPatterns(t *testing.T) {
	patterns := []string{
		"invalid api key",
		"invalid_api_key",
		"incorrect api key",
		"invalid token",
		"authentication failed",
		"re-authenticate",
		"oauth token refresh failed",
		"unauthorized access",
		"forbidden",
		"access denied",
		"expired",
		"token has expired",
		"no credentials found",
		"no api key found",
	}

	for _, msg := range patterns {
		err := errors.New(msg)
		result := ClassifyError(err, "openai", "gpt-4")
		if result == nil {
			t.Errorf("pattern %q: expected non-nil", msg)
			continue
		}
		if result.Reason != FailoverAuth {
			t.Errorf("pattern %q: reason = %q, want auth", msg, result.Reason)
		}
	}
}

func TestClassifyError_FormatPatterns(t *testing.T) {
	patterns := []string{
		"string should match pattern",
		"tool_use.id is required",
		"invalid tool_use_id",
		"messages.1.content.1.tool_use.id must be valid",
		"invalid request format",
	}

	for _, msg := range patterns {
		err := errors.New(msg)
		result := ClassifyError(err, "anthropic", "claude")
		if result == nil {
			t.Errorf("pattern %q: expected non-nil", msg)
			continue
		}
		if result.Reason != FailoverFormat {
			t.Errorf("pattern %q: reason = %q, want format", msg, result.Reason)
		}
	}
}

func TestClassifyError_ImageDimensionError(t *testing.T) {
	err := errors.New("image dimensions exceed max allowed 2048x2048")
	result := ClassifyError(err, "openai", "gpt-4o")
	if result == nil {
		t.Fatal("expected non-nil for image dimension error")
	}
	if result.Reason != FailoverFormat {
		t.Errorf("reason = %q, want format", result.Reason)
	}
	if result.IsRetriable() {
		t.Error("image dimension error should not be retriable")
	}
}

func TestClassifyError_ContextOverflowPatterns(t *testing.T) {
	patterns := []string{
		"context_length_exceeded",
		"context_window_exceeded",
		"maximum context length",
		"token limit",
		"too many tokens",
		"prompt is too long",
		"request too large",
	}

	for _, msg := range patterns {
		err := errors.New(msg)
		result := ClassifyError(err, "openai", "gpt-4")
		if result == nil {
			t.Errorf("pattern %q: expected non-nil", msg)
			continue
		}
		if result.Reason != FailoverContextOverflow {
			t.Errorf("pattern %q: reason = %q, want context_overflow", msg, result.Reason)
		}
	}
}

func TestClassifyError_ImageSizeError(t *testing.T) {
	err := errors.New("image exceeds 20 mb limit")
	result := ClassifyError(err, "openai", "gpt-4o")
	if result == nil {
		t.Fatal("expected non-nil for image size error")
	}
	if result.Reason != FailoverFormat {
		t.Errorf("reason = %q, want format", result.Reason)
	}
}

func TestClassifyError_ConnectionDropPatterns(t *testing.T) {
	// Each of these is a transient mid-stream transport failure and must be
	// retriable via FailoverTimeout (the only reason the agent loop retries
	// inline — pkg/agent/loop.go:4494).
	patterns := []string{
		"streaming read error: http2: response body closed",
		"unexpected EOF",
		"read tcp 10.0.0.1:443: read: connection reset by peer",
		"write tcp: broken pipe",
		"stream error: stream ID 7; INTERNAL_ERROR",
		"connection closed before response was received",
		"use of closed network connection",
		"http: server closed idle connection",
		"read: connection timed out",
	}

	for _, msg := range patterns {
		err := errors.New(msg)
		result := ClassifyError(err, "openai", "gpt-4")
		if result == nil {
			t.Errorf("pattern %q: expected non-nil (transient drop must classify)", msg)
			continue
		}
		if result.Reason != FailoverTimeout {
			t.Errorf("pattern %q: reason = %q, want timeout", msg, result.Reason)
		}
		if !result.IsRetriable() {
			t.Errorf("pattern %q: expected retriable", msg)
		}
	}
}

func TestClassifyError_ConnectionDropTriggersInlineRetry(t *testing.T) {
	// Regression guard: the original CI failure was a mid-stream
	// "http2: response body closed" classified as nil, which the loop treats as
	// non-retriable and breaks on with zero retries. The loop only retries inline
	// when Reason == FailoverTimeout (loop.go:4494), so assert exactly that.
	err := errors.New("streaming read error: http2: response body closed")
	result := ClassifyError(err, "openrouter", "z-ai/glm-5-turbo")
	if result == nil {
		t.Fatal("http2 body-closed drop must not classify as nil")
	}
	if result.Reason != FailoverTimeout {
		t.Errorf("reason = %q, want timeout (only reason the loop retries inline)", result.Reason)
	}
}

func TestClassifyError_NoEndpointsFoundStillFatal(t *testing.T) {
	// "404 No endpoints found" is a genuinely-fatal routing error (model/route
	// missing). It must NOT be reclassified as retriable by the new connection-drop
	// patterns. It carries no error-context word before 404, so extractHTTPStatus
	// returns 0 and no message pattern matches → nil (loop breaks, no retry).
	err := errors.New("404 No endpoints found for z-ai/glm-5-turbo")
	result := ClassifyError(err, "openrouter", "z-ai/glm-5-turbo")
	if result != nil {
		t.Errorf("expected nil (fatal, non-retriable), got %+v", result)
	}

	// The explicit "status: 404" form: 404 is not in classifyByStatus and matches
	// no message pattern, so it classifies as nil (loop breaks — non-retriable).
	// The new connection-drop set must not change that to a retriable timeout.
	// Assert the result is strictly nil (the prior `!= nil && ... == FailoverTimeout`
	// form passed vacuously when result2 was nil).
	err2 := errors.New("status: 404 model not found")
	result2 := ClassifyError(err2, "openrouter", "z-ai/glm-5-turbo")
	if result2 != nil {
		t.Errorf("status:404 must be non-retriable (nil), got %+v", result2)
	}
}

// TestClassifyError_GoAwayPatternsClassifiedAsTimeout verifies that HTTP/2
// GOAWAY error strings are classified as FailoverTimeout (retriable). These
// were previously unmatched by connectionDropPatterns, causing ClassifyError to
// return nil → the agent loop treated them as terminal → 0-token turns.
func TestClassifyError_GoAwayPatternsClassifiedAsTimeout(t *testing.T) {
	goawayPatterns := []string{
		// net/http GoAwayError.Error() non-graceful form.
		`http2: server sent GOAWAY and closed the connection; LastStreamID=5, ErrCode=INTERNAL_ERROR, debug=""`,
		// net/http GoAwayError.Error() graceful-shutdown form (load-balancer recycle).
		"http2: Transport received Server's graceful shutdown GOAWAY",
		// Wrapped in the openai_compat "streaming read error:" prefix.
		`streaming read error: http2: server sent GOAWAY and closed the connection; LastStreamID=0, ErrCode=INTERNAL_ERROR, debug=""`,
	}
	for _, msg := range goawayPatterns {
		err := errors.New(msg)
		result := ClassifyError(err, "openrouter", "z-ai/glm-5-turbo")
		if result == nil {
			t.Errorf("GOAWAY pattern %q: expected non-nil (must classify as FailoverTimeout)", msg)
			continue
		}
		if result.Reason != FailoverTimeout {
			t.Errorf("GOAWAY pattern %q: reason = %q, want %q", msg, result.Reason, FailoverTimeout)
		}
		if !result.IsRetriable() {
			t.Errorf("GOAWAY pattern %q: expected retriable", msg)
		}
	}
}

func TestClassifyError_CleanEOFNotMisclassified(t *testing.T) {
	// A clean io.EOF marks normal stream completion. It must NOT be classified
	// as an error (the bare "EOF" substring is deliberately not in the
	// connection-drop set; only "unexpected EOF" matches).
	if result := ClassifyError(io.EOF, "openai", "gpt-4"); result != nil {
		t.Errorf("clean io.EOF must not classify as an error, got %+v", result)
	}
	if result := ClassifyError(errors.New("EOF"), "openai", "gpt-4"); result != nil {
		t.Errorf("bare \"EOF\" must not classify as an error, got %+v", result)
	}
}

func TestClassifyError_FatalErrorsUnaffectedByDropPatterns(t *testing.T) {
	// Confirm genuinely-fatal classifications are unchanged after adding the
	// connection-drop set.
	tests := []struct {
		msg    string
		reason FailoverReason
	}{
		{"invalid api key", FailoverAuth},
		{"status: 401 unauthorized", FailoverAuth},
		{"status: 403 forbidden", FailoverAuth},
		{"context_length_exceeded", FailoverContextOverflow},
		{"maximum context length", FailoverContextOverflow},
		{"string should match pattern", FailoverFormat},
	}
	for _, tt := range tests {
		result := ClassifyError(errors.New(tt.msg), "openai", "gpt-4")
		if result == nil {
			t.Errorf("pattern %q: expected non-nil", tt.msg)
			continue
		}
		if result.Reason != tt.reason {
			t.Errorf("pattern %q: reason = %q, want %q", tt.msg, result.Reason, tt.reason)
		}
	}
}

func TestClassifyError_UnknownError(t *testing.T) {
	err := errors.New("some completely random error")
	result := ClassifyError(err, "openai", "gpt-4")
	if result != nil {
		t.Errorf("expected nil for unknown error, got %+v", result)
	}
}

func TestClassifyError_ProviderModelPropagation(t *testing.T) {
	err := errors.New("rate limit exceeded")
	result := ClassifyError(err, "my-provider", "my-model")
	if result == nil {
		t.Fatal("expected non-nil")
	}
	if result.Provider != "my-provider" {
		t.Errorf("provider = %q, want my-provider", result.Provider)
	}
	if result.Model != "my-model" {
		t.Errorf("model = %q, want my-model", result.Model)
	}
}

func TestFailoverError_IsRetriable(t *testing.T) {
	tests := []struct {
		reason    FailoverReason
		retriable bool
	}{
		{FailoverAuth, true},
		{FailoverRateLimit, true},
		{FailoverBilling, true},
		{FailoverTimeout, true},
		{FailoverOverloaded, true},
		{FailoverFormat, false},
		{FailoverContextOverflow, false},
		{FailoverUnknown, true},
	}

	for _, tt := range tests {
		fe := &FailoverError{Reason: tt.reason}
		if fe.IsRetriable() != tt.retriable {
			t.Errorf("IsRetriable(%q) = %v, want %v", tt.reason, fe.IsRetriable(), tt.retriable)
		}
	}
}

func TestFailoverError_ErrorString(t *testing.T) {
	fe := &FailoverError{
		Reason:   FailoverRateLimit,
		Provider: "openai",
		Model:    "gpt-4",
		Status:   429,
		Wrapped:  errors.New("too many requests"),
	}
	s := fe.Error()
	if s == "" {
		t.Error("expected non-empty error string")
	}
}

func TestFailoverError_Unwrap(t *testing.T) {
	inner := errors.New("inner error")
	fe := &FailoverError{Reason: FailoverTimeout, Wrapped: inner}
	if fe.Unwrap() != inner {
		t.Error("Unwrap should return wrapped error")
	}
}

func TestExtractHTTPStatus(t *testing.T) {
	tests := []struct {
		msg  string
		want int
	}{
		{"status: 429 rate limited", 429},
		{"status 401 unauthorized", 401},
		{"http/1.1 502 bad gateway", 502},
		{"error 429", 429},
		{"no status code here", 0},
		{"random number 12345", 0},
	}

	for _, tt := range tests {
		got := extractHTTPStatus(tt.msg)
		if got != tt.want {
			t.Errorf("extractHTTPStatus(%q) = %d, want %d", tt.msg, got, tt.want)
		}
	}
}

func TestIsImageDimensionError(t *testing.T) {
	if !IsImageDimensionError("image dimensions exceed max 4096x4096") {
		t.Error("should match image dimensions exceed max")
	}
	if IsImageDimensionError("normal error message") {
		t.Error("should not match normal error")
	}
}

func TestIsImageSizeError(t *testing.T) {
	if !IsImageSizeError("image exceeds 20 mb") {
		t.Error("should match image exceeds mb")
	}
	if IsImageSizeError("normal error message") {
		t.Error("should not match normal error")
	}
}
