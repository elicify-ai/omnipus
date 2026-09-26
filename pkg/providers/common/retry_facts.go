package common

// retry_facts.go — provider-messages spec §7.2 capture: retry-after and
// request-id facts parsed from the error response's headers at the HTTP
// boundary (HandleErrorResponse / WrapHTMLResponseError).
//
// Capture rules (spec §7.2 + dataset D1):
//   - ONLY retry-after and retry-after-ms are parsed. The x-ratelimit-reset*
//     family is deliberately OUT of scope (C-22/MAJ-008): its values are not
//     seconds-since-now and parsing them would manufacture a retry fact the
//     provider never stated.
//   - retry-after parses integer seconds or an HTTP-date. A past date or a
//     malformed value yields 0 — indistinguishable from an absent header
//     (MIN-101: 0 means "fact absent, take the backoff schedule"). A future
//     date yields the delta seconds AT PARSE TIME.
//   - retry-after-ms rounds UP to whole seconds and is ≥ 1 when
//     present-and-nonzero; 0ms is fact absent.
//   - Duplicate headers: the FIRST value wins.
//   - When both headers are present, an explicit positive retry-after (in
//     seconds) wins; retry-after-ms only backs it up when seconds yielded no
//     usable fact.
//   - The ≤120 s auto-retry ceiling is a DOWNSTREAM decision (the fallback
//     chain's), never a capture decision: an over-ceiling value (3600) is
//     still captured as a fact.
//
// Request-id candidates, in priority order (OBS-003): x-request-id,
// request-id, x-amzn-requestid, cf-ray (last resort — a Cloudflare edge id,
// not a provider request id).

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// parseRetryAfterSeconds reads the retry-after / retry-after-ms facts from h
// per the rules in this file's header. Returns 0 when no usable fact exists.
func parseRetryAfterSeconds(h http.Header) int {
	if v := h.Get("Retry-After"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			if n > 0 {
				return n
			}
		} else if t, dateErr := http.ParseTime(v); dateErr == nil {
			// HTTP-date: delta seconds at parse time, rounded up so a
			// sub-second remainder never under-reports the wait.
			if d := time.Until(t); d > 0 {
				return int((d + time.Second - 1) / time.Second)
			}
		}
	}
	if v := h.Get("Retry-After-Ms"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			// Round up; any nonzero millisecond value is at least 1 second.
			return (n + 999) / 1000
		}
	}
	return 0
}

// parseRequestID reads the first non-empty request-id candidate header
// (priority order in this file's header). Returns "" when none is present.
func parseRequestID(h http.Header) string {
	for _, key := range []string{"X-Request-Id", "Request-Id", "X-Amzn-Requestid", "Cf-Ray"} {
		if v := strings.TrimSpace(h.Get(key)); v != "" {
			return v
		}
	}
	return ""
}

// WrapHTMLResponseErrorWithFacts is WrapHTMLResponseError with the §7.2
// header facts attached. WrapHTMLResponseError's own signature takes no
// headers (one of its callers — ReadAndParseResponse — has only a body
// prefix available), so HandleErrorResponse — the caller that holds the
// headers — parses them and hands the parsed facts here. This variant keeps
// the HTML error path a full §7.2 parse site without breaking the
// prefix-only caller.
func WrapHTMLResponseErrorWithFacts(
	statusCode int, body []byte, contentType, apiBase string,
	retryAfterSeconds int, requestID string,
) error {
	err := WrapHTMLResponseError(statusCode, body, contentType, apiBase)
	if pe, ok := err.(*ProviderError); ok {
		pe.RetryAfterSeconds = retryAfterSeconds
		pe.RequestID = requestID
		return pe
	}
	return err
}
