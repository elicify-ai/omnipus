package providers

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

// Common patterns in Go HTTP error messages
var httpStatusPatterns = []*regexp.Regexp{
	regexp.MustCompile(`status[:\s]+(\d{3})`),
	regexp.MustCompile(`http[/\s]+\d*\.?\d*\s+(\d{3})`),
	// Third pattern requires a preceding error-context word (status, code, http, error, returned)
	// to avoid false positives on numbers like "context length 512" or "512 tokens remaining".
	regexp.MustCompile(`(?i)(?:status|code|http|error|returned)\s*:?\s*([3-5]\d{2})\b`),
}

// errorPattern defines a single pattern (string or regex) for error classification.
type errorPattern struct {
	substring string
	regex     *regexp.Regexp
}

func substr(s string) errorPattern { return errorPattern{substring: s} }
func rxp(r string) errorPattern    { return errorPattern{regex: regexp.MustCompile("(?i)" + r)} }

// Error patterns organized by FailoverReason, matching OpenClaw production (~40 patterns).
var (
	rateLimitPatterns = []errorPattern{
		rxp(`rate[_ ]limit`),
		substr("too many requests"),
		substr("429"),
		substr("exceeded your current quota"),
		rxp(`exceeded.*quota`),
		rxp(`resource has been exhausted`),
		rxp(`resource.*exhausted`),
		substr("resource_exhausted"),
		substr("quota exceeded"),
		substr("usage limit"),
	}

	overloadedPatterns = []errorPattern{
		rxp(`overloaded_error`),
		rxp(`"type"\s*:\s*"overloaded_error"`),
		substr("overloaded"),
	}

	timeoutPatterns = []errorPattern{
		substr("timeout"),
		substr("timed out"),
		substr("deadline exceeded"),
		substr("context deadline exceeded"),
	}

	// connectionDropPatterns match transient transport/connection failures —
	// the upstream provider hung up or the socket died mid-stream. These are
	// NOT application-level rejections (4xx) and are NOT a clean end-of-stream
	// (io.EOF after a complete response). They are equivalent to a 5xx in that
	// the safe action is to retry the same request, so they are classified as
	// FailoverTimeout (the only reason the agent loop retries inline with
	// exponential backoff — pkg/agent/loop.go:4494).
	//
	// These are matched LAST among the message patterns (see classifyByMessage)
	// so that a genuinely-fatal classification (auth/format/context-overflow)
	// always wins when a message contains both a fatal phrase and a transport
	// phrase.
	//
	// IMPORTANT: only "unexpected EOF" is matched, never a bare "EOF". A clean
	// io.EOF marks normal stream completion and must not be treated as an error.
	//
	// HTTP/2 GOAWAY frames: Go's net/http emits two GOAWAY error strings.
	//   - "http2: server sent GOAWAY and closed the connection; ..." (GoAwayError.Error())
	//   - "http2: Transport received Server's graceful shutdown GOAWAY"
	// Neither is matched by the other substrings in this list, so they are
	// listed explicitly. Both indicate the upstream server reset the connection
	// and the request can be safely retried.
	connectionDropPatterns = []errorPattern{
		substr("http2: response body closed"),
		substr("http2: server sent goaway"),
		substr("http2: transport received server's graceful shutdown goaway"),
		substr("unexpected eof"),
		substr("connection reset by peer"),
		substr("broken pipe"),
		substr("stream error:"),
		substr("connection closed"),
		substr("use of closed network connection"),
		substr("server closed idle connection"),
		substr("read: connection timed out"),
	}

	billingPatterns = []errorPattern{
		rxp(`\b402\b`),
		substr("payment required"),
		substr("insufficient credits"),
		substr("credit balance"),
		substr("plans & billing"),
		substr("insufficient balance"),
	}

	authPatterns = []errorPattern{
		rxp(`invalid[_ ]?api[_ ]?key`),
		substr("incorrect api key"),
		substr("invalid token"),
		substr("authentication"),
		substr("re-authenticate"),
		substr("oauth token refresh failed"),
		substr("unauthorized"),
		substr("forbidden"),
		substr("access denied"),
		substr("expired"),
		substr("token has expired"),
		rxp(`\b401\b`),
		rxp(`\b403\b`),
		substr("no credentials found"),
		substr("no api key found"),
	}

	formatPatterns = []errorPattern{
		substr("string should match pattern"),
		substr("tool_use.id"),
		substr("tool_use_id"),
		substr("messages.1.content.1.tool_use.id"),
		substr("invalid request format"),
	}
	contextOverflowPatterns = []errorPattern{
		rxp(`context[_ ]?length[_ ]?exceeded`),
		rxp(`context[_ ]?window[_ ]?exceeded`),
		substr("maximum context length"),
		substr("token limit"),
		substr("too many tokens"),
		substr("prompt is too long"),
		substr("request too large"),
		// Vendor-specific patterns matching OpenClaw production classification.
		substr("invalidparameter"),                      // Azure/Qwen: total tokens exceed max
		substr("total tokens of image and text exceed"), // Azure multi-modal context limit
	}

	imageDimensionPatterns = []errorPattern{
		rxp(`image dimensions exceed max`),
	}

	imageSizePatterns = []errorPattern{
		rxp(`image exceeds.*mb`),
	}

	// Transient HTTP status codes that map to timeout (server-side failures).
	transientStatusCodes = map[int]bool{
		500: true, 502: true, 503: true,
		521: true, 522: true, 523: true, 524: true,
		529: true,
	}
)

// ClassifyError classifies an error into a FailoverError with reason.
// Returns nil if the error is not classifiable (unknown errors should not trigger fallback).
func ClassifyError(err error, provider, model string) *FailoverError {
	if err == nil {
		return nil
	}

	// Context cancellation: user abort, never fallback.
	if errors.Is(err, context.Canceled) {
		return nil
	}

	// Context deadline exceeded: treat as timeout, always fallback.
	if errors.Is(err, context.DeadlineExceeded) {
		return &FailoverError{
			Reason:   FailoverTimeout,
			Provider: provider,
			Model:    model,
			Wrapped:  err,
		}
	}

	msg := strings.ToLower(err.Error())

	// Image dimension/size errors: non-retriable, non-fallback.
	if IsImageDimensionError(msg) || IsImageSizeError(msg) {
		return &FailoverError{
			Reason:   FailoverFormat,
			Provider: provider,
			Model:    model,
			Wrapped:  err,
		}
	}

	// Try HTTP status code extraction first.
	// For status 400, message patterns take priority because 400 can represent
	// both "bad request format" and "context overflow" depending on the provider.
	if status := extractHTTPStatus(msg); status > 0 {
		if status == 400 {
			// Check message patterns before defaulting to FailoverFormat for 400.
			if reason := classifyByMessage(msg); reason != "" {
				return &FailoverError{
					Reason:   reason,
					Provider: provider,
					Model:    model,
					Status:   status,
					Wrapped:  err,
				}
			}
		}
		if reason := classifyByStatus(status); reason != "" {
			return &FailoverError{
				Reason:   reason,
				Provider: provider,
				Model:    model,
				Status:   status,
				Wrapped:  err,
			}
		}
	}

	// Message pattern matching (priority order from OpenClaw).
	if reason := classifyByMessage(msg); reason != "" {
		return &FailoverError{
			Reason:   reason,
			Provider: provider,
			Model:    model,
			Wrapped:  err,
		}
	}

	return nil
}

// classifyByStatus maps HTTP status codes to FailoverReason.
func classifyByStatus(status int) FailoverReason {
	switch {
	case status == 401 || status == 403:
		return FailoverAuth
	case status == 402:
		return FailoverBilling
	case status == 408:
		return FailoverTimeout
	case status == 429:
		return FailoverRateLimit
	case status == 400:
		return FailoverFormat
	case transientStatusCodes[status]:
		return FailoverTimeout
	}
	return ""
}

// classifyByMessage matches error messages against patterns.
// Priority order matters (from OpenClaw classifyFailoverReason).
func classifyByMessage(msg string) FailoverReason {
	if matchesAny(msg, rateLimitPatterns) {
		return FailoverRateLimit
	}
	if matchesAny(msg, overloadedPatterns) {
		return FailoverRateLimit // Overloaded treated as rate_limit
	}
	if matchesAny(msg, billingPatterns) {
		return FailoverBilling
	}
	if matchesAny(msg, timeoutPatterns) {
		return FailoverTimeout
	}
	if matchesAny(msg, authPatterns) {
		return FailoverAuth
	}
	if matchesAny(msg, formatPatterns) {
		return FailoverFormat
	}
	if matchesAny(msg, contextOverflowPatterns) {
		return FailoverContextOverflow
	}
	// Transient connection drops (mid-stream disconnects) are retried inline like
	// timeouts. Checked LAST among the message patterns so that any genuinely-fatal
	// classification (auth, bad-request format, context overflow) wins when a message
	// happens to contain both a fatal phrase and a transport-drop phrase
	// (e.g. "context_length_exceeded ... connection closed"). It remains after
	// rate-limit/overloaded/billing/timeout, which is correct — those are also
	// retriable or more specific. 4xx status codes are already resolved by
	// extractHTTPStatus before this function runs.
	if matchesAny(msg, connectionDropPatterns) {
		return FailoverTimeout
	}
	return ""
}

// extractHTTPStatus extracts an HTTP status code from an error message.
// Looks for patterns like "status: 429", "status 429", "http/1.1 429", "http 429", or standalone "429".
func extractHTTPStatus(msg string) int {
	for _, p := range httpStatusPatterns {
		if m := p.FindStringSubmatch(msg); len(m) > 1 {
			return parseDigits(m[1])
		}
	}
	return 0
}

// IsImageDimensionError returns true if the message indicates an image dimension error.
func IsImageDimensionError(msg string) bool {
	return matchesAny(msg, imageDimensionPatterns)
}

// IsImageSizeError returns true if the message indicates an image file size error.
func IsImageSizeError(msg string) bool {
	return matchesAny(msg, imageSizePatterns)
}

// matchesAny checks if msg matches any of the patterns.
func matchesAny(msg string, patterns []errorPattern) bool {
	for _, p := range patterns {
		if p.regex != nil {
			if p.regex.MatchString(msg) {
				return true
			}
		} else if p.substring != "" {
			if strings.Contains(msg, p.substring) {
				return true
			}
		}
	}
	return false
}

// parseDigits converts a string of digits to an int.
func parseDigits(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}
