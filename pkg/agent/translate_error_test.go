// translate_error_test.go — Wave 1 classifier regression coverage.
//
// Tests the new shared classifier (translate_error.go) covering the
// Wave 1 BLOCK/HIGH review findings:
//
//   - Status precedence: status wins over body for 4xx except where the
//     body matches a pinned media / capability-absence / policy shape
//     (CRIT-002 in ADR-051).
//   - 413 + "request too large" → CodeProviderRejected (NOT
//     CodeContextTooLong) per ADR-051 Rev 3 edge case "HTTP 413 →
//     provider_rejected (not context_too_long)".
//   - 408 / 5xx / timeout → CodeNetwork (retryable).
//   - 429 → CodeRateLimited.
//   - Generic 400 + no body → CodeProviderRejected (NOT CodeMedia).
//   - PDF / image pinned phrases → CodeMediaUnsupported.
//
// Each test is a small, table-driven case so adding a new pinned
// substring or HTTP-status mapping is a one-line addition.

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// TestTranslateLLMError_StatusWinsOverBody is the regression for the
// "status vs body" precedence rule. A 4xx with NO body (or with a body
// that doesn't match a pinned media / capability / policy shape) must
// resolve to CodeUnknown (Wave 1 TD-M7 strict reading — the
// outcome-fallback gate fires only on CodeUnknown per FR-017). A 4xx
// WITH a pinned media shape must resolve to CodeMediaUnsupported (the
// xAI/Grok incident case).
//
// Precedence rule (documented on classifyByHTTPStatus):
//   - Status is consulted first.
//   - Body shape wins only for media / capability / policy / context
//     overflow substrings.
//   - Generic 400 + absent body → CodeUnknown (NOT CodeMedia AND NOT
//     CodeProviderRejected — the spec-mandated "inconclusive" verdict
//     for the outcome-fallback gate).
func TestTranslateLLMError_StatusWinsOverBody(t *testing.T) {
	cases := []struct {
		name string
		pe   *ProviderError
		want LLMErrorCode
	}{
		{
			name: "400 absent body → unknown (residual 4xx, the fallback trigger)",
			pe:   &ProviderError{Status: 400, Body: ""},
			want: CodeUnknown,
		},
		{
			name: "400 whitespace-only body → unknown (residual 4xx, the fallback trigger)",
			pe:   &ProviderError{Status: 400, Body: "   "},
			want: CodeUnknown,
		},
		{
			name: "400 with media-class body → media_unsupported (status+body together)",
			pe: &ProviderError{
				Status: 400,
				Body:   "Downloaded response does not contain a valid JPG, PNG, WebP, or ICO image.",
			},
			want: CodeMediaUnsupported,
		},
		{
			name: "400 with capability-absence body → media_unsupported",
			pe: &ProviderError{
				Status: 400,
				Body:   "'claude-3-5-haiku-20241022' does not support image input.",
			},
			want: CodeMediaUnsupported,
		},
		{
			name: "401 → provider_rejected (auth, retryable=false)",
			pe:   &ProviderError{Status: 401, Body: ""},
			want: CodeProviderRejected,
		},
		{
			name: "403 → provider_rejected (auth, retryable=false)",
			pe:   &ProviderError{Status: 403, Body: ""},
			want: CodeProviderRejected,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			llm := TranslateLLMError(tc.pe, "")
			assert.Equal(t, tc.want, llm.Code,
				"TranslateLLMError(%+v) → code=%s, want=%s",
				tc.pe, llm.Code, tc.want)
		})
	}
}

// TestTranslateLLMError_413WithRequestTooLarge is the regression for the
// ADR-051 Rev 3 edge case "HTTP 413 → provider_rejected (not
// context_too_long)". A 413 with a "request too large" body must NOT be
// classified as context overflow — the model accepted the conversation,
// the request payload is the too-large thing.
func TestTranslateLLMError_413WithRequestTooLarge(t *testing.T) {
	pe := &ProviderError{
		Status: 413,
		Body:   "request too large; payload exceeds the provider limit",
	}
	llm := TranslateLLMError(pe, "")
	assert.Equal(t, CodeProviderRejected, llm.Code,
		"413 + 'request too large' must map to provider_rejected, NOT context_too_long (ADR-051 Rev 3)")
	assert.False(t, llm.Retryable, "provider_rejected is not retryable")

	// 413 with a context-overflow-shaped body is the narrow exception —
	// substring matches for context-overflow shapes still route to
	// CodeContextTooLong (the conversation is genuinely too long).
	peOverflow := &ProviderError{
		Status: 413,
		Body:   "context length exceeded",
	}
	llmOverflow := TranslateLLMError(peOverflow, "")
	assert.Equal(t, CodeContextTooLong, llmOverflow.Code,
		"413 + context-length-exceeded substring must map to context_too_long")
}

// TestTranslateLLMError_Generic400_NoBody locks the "generic 400 + no body
// → CodeUnknown" rule (Wave 1 TD-M7 strict reading). A residual 4xx with
// no body shape is the canonical inconclusive-classification case: the
// outcome-based fallback gate fires only on CodeUnknown per FR-017, and
// surfacing a no-body 400 as CodeProviderRejected would silently drop
// the fallback for genuine residual rejections like the Gemini 400
// "Unsupported MIME type: image/svg+xml" BDD row.
func TestTranslateLLMError_Generic400_NoBody(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty body", ""},
		{"whitespace body", "   "},
		{"unrelated body", "invalid api key: 401 unauthorized"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			llm := TranslateLLMError(&ProviderError{Status: 400, Body: tc.body}, "")
			assert.Equal(t, CodeUnknown, llm.Code,
				"generic 400 (no media/policy shape) must map to CodeUnknown so the outcome-fallback gate can fire")
		})
	}
}

// TestTranslateLLMError_PDFRejectionPhrase verifies the xAI-style PDF
// pinned phrases route to CodeMediaUnsupported (RD2 — media-class
// downgrade-retry applies).
func TestTranslateLLMError_PDFRejectionPhrase(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"pdf input not supported", "this model does not support pdf input"},
		{"pdf not supported", "pdf not supported by upstream"},
		{"mixed case", "PDF INPUT NOT SUPPORTED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			llm := TranslateLLMError(&ProviderError{Status: 400, Body: tc.body}, "")
			assert.Equal(t, CodeMediaUnsupported, llm.Code,
				"PDF-rejection body shape must map to media_unsupported")
		})
	}
}

// TestTranslateLLMError_NetworkAndRateLimit covers the
// status-driven mappings (408/5xx/timeout → CodeNetwork; 429 →
// CodeRateLimited) and the substring fallback for non-HTTP errors.
func TestTranslateLLMError_NetworkAndRateLimit(t *testing.T) {
	cases := []struct {
		name      string
		pe        *ProviderError
		wantCode  LLMErrorCode
		wantRetry bool
	}{
		{"408 request timeout", &ProviderError{Status: 408}, CodeNetwork, true},
		{"500 internal server error", &ProviderError{Status: 500}, CodeNetwork, true},
		{"502 bad gateway", &ProviderError{Status: 502}, CodeNetwork, true},
		{"503 service unavailable", &ProviderError{Status: 503}, CodeNetwork, true},
		{"504 gateway timeout", &ProviderError{Status: 504}, CodeNetwork, true},
		{"429 too many requests", &ProviderError{Status: 429}, CodeRateLimited, true},
		{"429 with quota body", &ProviderError{Status: 429, Body: "quota exceeded"}, CodeRateLimited, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			llm := TranslateLLMError(tc.pe, "")
			assert.Equal(t, tc.wantCode, llm.Code)
			assert.Equal(t, tc.wantRetry, llm.Retryable,
				"%s retryable bit must be %v", tc.name, tc.wantRetry)
		})
	}
}

// TestTranslateLLMError_SubstringFallbackNoStatus covers the path where
// no HTTP status is available (pe.Status==0 or pe nil) and the
// classifier falls back to substring matching on the message / body.
func TestTranslateLLMError_SubstringFallbackNoStatus(t *testing.T) {
	cases := []struct {
		name string
		pe   *ProviderError
		want LLMErrorCode
	}{
		{
			name: "image capability absence",
			pe:   &ProviderError{Body: "model does not support image input"},
			want: CodeMediaUnsupported,
		},
		{
			name: "image format rejection (incident string)",
			pe:   &ProviderError{Body: "Downloaded response does not contain a valid JPG, PNG, WebP, or ICO image."},
			want: CodeMediaUnsupported,
		},
		{
			name: "content policy",
			pe:   &ProviderError{Body: "content policy violation"},
			want: CodeContentPolicy,
		},
		{
			name: "context overflow",
			pe:   &ProviderError{Body: "context length exceeded"},
			want: CodeContextTooLong,
		},
		{
			name: "unknown",
			pe:   &ProviderError{Body: "something completely unrelated"},
			want: CodeUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			llm := TranslateLLMError(tc.pe, "")
			assert.Equal(t, tc.want, llm.Code)
		})
	}
}

// TestTranslateLLMError_MessageGenericOverServerText locks the
// ADR-051 §RD5 invariant: the user-facing Message is generic over server
// text. None of the entries in the userMessages map may echo the body /
// raw provider text — a regression here would re-leak raw provider
// content to the SPA.
func TestTranslateLLMError_MessageGenericOverServerText(t *testing.T) {
	pe := &ProviderError{
		Status: 400,
		Body:   "Downloaded response does not contain a valid JPG, PNG, WebP, or ICO image. Model: xai/grok-2-vision. Trace: abc-123",
	}
	llm := TranslateLLMError(pe, "")
	assert.NotContains(t, llm.Message, "xai/grok-2-vision",
		"message must not echo provider/model identity")
	assert.NotContains(t, llm.Message, "Trace:",
		"message must not echo provider trace IDs")
	assert.NotContains(t, llm.Message, "JPG, PNG",
		"message must not echo provider body shape")
}

// TestTranslateLLMError_NilSafety locks the "pe==nil, message==\"\""
// path: must NOT panic and must return CodeUnknown with a non-empty
// generic message.
func TestTranslateLLMError_NilSafety(t *testing.T) {
	llm := TranslateLLMError(nil, "")
	assert.NotEmpty(t, llm.Message, "even unknown-classified errors get a generic message")
	assert.Equal(t, CodeUnknown, llm.Code)
	assert.False(t, llm.Retryable)
}

// TestClassifyByProviderError_UnknownSubstring verifies that an
// unrecognized error shape returns CodeUnknown (and the user message is
// the generic "encountered an error" copy).
//
// The classification path: when pe.Status == 0 (no HTTP status) the
// classifier falls through to classifyByMessage, which returns
// CodeUnknown when no pinned substring matches. We pass nil status to
// exercise that fallback path — when status is 4xx with no body, the
// classifier maps to CodeProviderRejected (NOT CodeUnknown); that case
// is locked by TestTranslateLLMError_Generic400_NoBody.
func TestClassifyByProviderError_UnknownSubstring(t *testing.T) {
	pe := &ProviderError{
		Status: 0,
		Body:   "this is a totally novel provider error nobody has seen before xyz123",
	}
	code := classifyByProviderError(pe, pe.Body)
	assert.Equal(t, CodeUnknown, code)
	llm := TranslateLLMError(pe, pe.Body)
	assert.NotEmpty(t, llm.Message)
	assert.Equal(t, CodeUnknown, llm.Code)
	assert.Equal(t, "The AI service encountered an error.", llm.Message,
		"CodeUnknown must map to the generic 'encountered an error' copy")
}

// TestIsPDFRejectionMessage / TestIsImageRejectionMessage /
// TestIsCapabilityAbsenceMessage / TestIsContentPolicyMessage /
// TestIsContextOverflowMessage — small focused tests for the substring
// helpers. A regression here (e.g. case sensitivity loss) would silently
// miss the incident string and re-route a media-class rejection to
// CodeProviderRejected.
func TestIsPDFRejectionMessage(t *testing.T) {
	hits := []string{
		"PDF not supported",
		"pdf input not supported by this model",
		"PDF INPUT NOT SUPPORTED", // case-insensitive
	}
	misses := []string{
		"",
		"image not supported",
		"invalid request",
		"this model does not support pdf input", // "does not support pdf" is NOT a pinned shape (capability-absence uses "does not support image")
	}
	for _, s := range hits {
		assert.True(t, isPDFRejectionMessage(s), "want hit for %q", s)
	}
	for _, s := range misses {
		assert.False(t, isPDFRejectionMessage(s), "want miss for %q", s)
	}
}

func TestIsImageRejectionMessage(t *testing.T) {
	hits := []string{
		// xAI/Grok incident string (verbatim)
		"Downloaded response does not contain a valid JPG, PNG, WebP, or ICO image.",
		// Generic phrases
		"unsupported image format",
		"image input not supported",
		"does not support image",
		"model does not support image input",
	}
	misses := []string{
		"",
		"pdf not supported",
		"image too large",
		"corrupt image data",
	}
	for _, s := range hits {
		assert.True(t, isImageRejectionMessage(s), "want hit for %q", s)
	}
	for _, s := range misses {
		assert.False(t, isImageRejectionMessage(s), "want miss for %q", s)
	}
}

// TestSanitizeRunnerError_AlwaysGeneric is the fail-closed invariant:
// regardless of the raw stderr shape, the AssistantText returned is
// always one of the generic userMessages entries. A regression here
// would re-leak raw CLI stderr to the assistant transcript.
func TestSanitizeRunnerError_AlwaysGeneric(t *testing.T) {
	rawCases := []struct {
		name string
		raw  string
	}{
		{"non-empty novel raw", "exit code 1: failed to download file xyz from disk"},
		{"non-empty auth-shaped raw", "claude-code: error: invalid API key"},
		{"non-empty novel raw 2", "some genuinely novel raw stderr text 12345"},
		{"empty raw", ""},
	}
	for _, tc := range rawCases {
		t.Run(tc.name, func(t *testing.T) {
			s := SanitizeRunnerError(tc.raw)
			assert.NotEmpty(t, s.AssistantText)
			if tc.raw != "" {
				// A non-empty raw must NOT appear in the sanitized
				// AssistantText. An empty raw trivially satisfies this
				// (every string contains the empty string), so we skip
				// the assertion for the empty case.
				assert.NotContains(t, s.AssistantText, tc.raw,
					"AssistantText must NEVER echo raw stderr verbatim (raw=%q)", tc.raw)
			}
			// Either the classifier emitted one of the userMessages entries,
			// or the fail-closed fallback "The external CLI failed..." — both
			// are acceptable; both are generic.
			isKnown := false
			for _, code := range []LLMErrorCode{
				CodeMediaUnsupported, CodeProviderRejected, CodeRateLimited,
				CodeNetwork, CodeContentPolicy, CodeContextTooLong, CodeUnknown,
			} {
				if s.AssistantText == userMessages[code] {
					isKnown = true
					break
				}
			}
			assert.True(t, isKnown || strings.HasPrefix(s.AssistantText, "The external CLI failed"),
				"AssistantText must be a generic user-message copy or the fail-closed fallback; got=%q", s.AssistantText)
		})
	}
}

// xAIMediaBody is the verbatim incident body from the xAI/Grok case
// that motivates the BLOCK 1+3 fix in ADR-051 pass-2. The body alone
// (without status=400) is enough for the classifier to map it to
// CodeMediaUnsupported via the pinned substring; we keep it as a
// constant so the BLOCK 1+3 tests stay readable.
const xAIMediaBody = "Downloaded response does not contain a valid JPG, PNG, WebP, or ICO image. Model: xai/grok-2-vision."

// makeProviderErr builds a *common.ProviderError for use in chain
// fixtures. Mirrors what HandleErrorResponse emits in real traffic.
func makeProviderErr(status int, body string) *common.ProviderError {
	return &common.ProviderError{
		Status:        status,
		Body:          body,
		BodyTruncated: false,
		BodyPreview:   body,
		ContentType:   "application/json",
		Err:           nil,
	}
}

// makeFailoverError wraps an inner error in a *providers.FailoverError
// the way ClassifyError would. Status is best-effort from the inner
// error's status; Wrapped is the inner error itself.
func makeFailoverError(reason providers.FailoverReason, provider, model string, status int, wrapped error) *providers.FailoverError {
	return &providers.FailoverError{
		Reason:   reason,
		Provider: provider,
		Model:    model,
		Status:   status,
		Wrapped:  wrapped,
	}
}

// TestProviderErrorFromFailover_PreservesBody is the BLOCK 3
// regression: when fe.Wrapped is a *common.ProviderError carrying the
// body bytes, ProviderErrorFromFailover MUST surface the body so the
// classifier substring-matches on it (the xAI 400 incident). Prior
// pass dropped the body, leaving pe.Body empty and forcing the
// classifier to fall back to pe.Err.Error() — which, for a wrapped
// *ProviderError, had been replaced with a status-only stringified
// form.
func TestProviderErrorFromFailover_PreservesBody(t *testing.T) {
	pe := makeProviderErr(400, xAIMediaBody)
	fe := makeFailoverError(providers.FailoverFormat, "openai_compat", "grok-2-vision", 400, pe)

	got := ProviderErrorFromFailover(fe)
	if assert.NotNil(t, got) {
		assert.Equal(t, 400, got.Status, "Status copied from fe.Status")
		assert.Equal(t, xAIMediaBody, got.Body,
			"Body preserved from *common.ProviderError wrapper (BLOCK 3)")
		assert.NotNil(t, got.Err, "Err preserved for Verbose-Chat Detail")
	}
}

// TestProviderErrorFromFailover_NoInnerProviderError covers the case
// where fe.Wrapped is a non-*ProviderError (e.g. a context error).
// The Body stays empty and the classifier falls through to substring
// matching on Err.
func TestProviderErrorFromFailover_NoInnerProviderError(t *testing.T) {
	fe := &providers.FailoverError{
		Reason:   providers.FailoverTimeout,
		Provider: "openai_compat",
		Model:    "gpt-4o",
		Status:   0,
		Wrapped:  context.DeadlineExceeded,
	}

	got := ProviderErrorFromFailover(fe)
	if assert.NotNil(t, got) {
		assert.Equal(t, 0, got.Status)
		assert.Empty(t, got.Body, "no body when no inner ProviderError")
		assert.ErrorIs(t, got.Err, context.DeadlineExceeded,
			"Wrapping preserved on Err")
	}
}

// TestTranslateLLMError_ProviderErrorFromFallbackExhausted_Media is
// the BLOCK 1+3 integration regression (ADR-051 pass-2): a direct
// *common.ProviderError carrying the xAI 400 body inside a
// *providers.FallbackExhaustedError MUST classify as
// CodeMediaUnsupported via TranslateLLMError — the body bytes must
// survive errorToProviderError's chain walk.
//
// This mirrors the aggregate's Unwrap() []error contract directly:
// providerErrorFromChain sees the structured attempt without requiring a
// synthetic *providers.FailoverError wrapper.
func TestTranslateLLMError_ProviderErrorFromFallbackExhausted_Media(t *testing.T) {
	pp := makeProviderErr(400, xAIMediaBody)
	exhausted := &providers.FallbackExhaustedError{
		Attempts: []providers.FallbackAttempt{
			{
				Provider: "openai_compat",
				Model:    "grok-2-vision",
				Error:    pp,
				Reason:   providers.FailoverFormat,
			},
		},
	}

	// Sanity-check the chain: LastStructuredError returns the inner pe
	// directly so the test asserts the body survives both paths.
	lastPe := exhausted.LastStructuredError()
	if assert.NotNil(t, lastPe) {
		assert.Equal(t, xAIMediaBody, lastPe.Body,
			"LastStructuredError surfaces the body to the classifier's reach")
	}

	agentPe := errorToProviderError(exhausted)
	if assert.NotNil(t, agentPe) {
		assert.Equal(t, 400, agentPe.Status)
		assert.Equal(t, xAIMediaBody, agentPe.Body,
			"body bytes must survive errorToProviderError's multi-error walk")
	}

	llm := TranslateLLMError(agentPe, "")
	assert.Equal(t, CodeMediaUnsupported, llm.Code,
		"400 + xAI media body must classify as CodeMediaUnsupported, "+
			"not CodeProviderRejected (BLOCK 1+3 regression)")
}

// TestTranslateLLMError_FallbackChain_MediaBeatsAuth is the BLOCK 4
// regression: a multi-error fallback chain with attempts
// [401, 400-media] must classify as media (the most-recent attempt's
// 400 body shape) rather than 401 (errors.As would otherwise pick
// the first-attempt FailoverError with Status=401). Without the slice
// walker, the classifier would mis-route the user's experience to
// "AI service rejected" instead of "couldn't process an attachment".
func TestTranslateLLMError_FallbackChain_MediaBeatsAuth(t *testing.T) {
	// Attempt 0: 401 unauthorized — earlier primary.
	authPe := makeProviderErr(401, `{"error":"invalid api key"}`)

	// Attempt 1: 400 xAI media shape — fallback grok-2-vision.
	mediaPe := makeProviderErr(400, xAIMediaBody)

	exhausted := &providers.FallbackExhaustedError{
		Attempts: []providers.FallbackAttempt{
			{Provider: "openrouter", Model: "gpt-4o", Error: authPe, Reason: providers.FailoverFormat},
			{Provider: "openrouter", Model: "grok-2-vision", Error: mediaPe, Reason: providers.FailoverFormat},
		},
	}

	// Walk the chain in reverse — most-recent attempt wins.
	agentPe := errorToProviderError(exhausted)
	if assert.NotNil(t, agentPe) {
		assert.Equal(t, 400, agentPe.Status,
			"most-recent attempt's status wins over earlier attempts (BLOCK 4)")
		assert.Equal(t, xAIMediaBody, agentPe.Body,
			"most-recent attempt's body wins (xAI media-shape, not the first 401's empty body)")
	}

	llm := TranslateLLMError(agentPe, "")
	assert.Equal(t, CodeMediaUnsupported, llm.Code,
		"multi-error chain with later 400-media must classify as media, "+
			"not provider_rejected from the earlier 401 (BLOCK 4 regression)")
}

// TestErrorToProviderError_DirectProviderError locks the single-error path:
// providerErrorFromChain must prefer a structured provider error directly
// reachable from the error rather than requiring a FailoverError wrapper.
// Both status and body are load-bearing for the ADR-051 classifier.
func TestErrorToProviderError_DirectProviderError(t *testing.T) {
	want := makeProviderErr(413, "request too large")
	got := errorToProviderError(want)
	if assert.NotNil(t, got) {
		assert.Equal(t, want.Status, got.Status)
		assert.Equal(t, want.Body, got.Body)
		assert.NotNil(t, got.Err, "the original structured provider error must remain available for detail")
	}
}

// TestErrorToProviderError_EmptyMultiError ensures we degrade safely
// when a FallbackExhaustedError's Unwrap exposes an empty slice (e.g.
// every attempt was a cooldown skip — no underlying error). The
// classifier must NOT panic, MUST return a synthetic pe carrying the
// original error so callers still get a usable pe chain.
func TestErrorToProviderError_EmptyMultiError(t *testing.T) {
	exhausted := &providers.FallbackExhaustedError{
		Attempts: nil,
	}
	pe := errorToProviderError(exhausted)
	if assert.NotNil(t, pe) {
		// Empty slice → falls through to the single-error path →
		// synthetic pe carrying the original error on Err.
		assert.Equal(t, 0, pe.Status)
		assert.Empty(t, pe.Body)
		assert.NotNil(t, pe.Err, "synthetic pe must surface the original err chain")
	}
}

// TestErrorToProviderError_FallbackExhaustedErrorPathOK covers the
// legacy *FallbackExhaustedError without inner *ProviderError — the
// chain is just wrappers around plain errors. The classifier should
// still get a pe so the choke point doesn't render an empty status.
func TestErrorToProviderError_FallbackExhaustedErrorPathOK(t *testing.T) {
	fe := &providers.FailoverError{
		Reason:   providers.FailoverTimeout,
		Provider: "openai_compat",
		Model:    "gpt-4o",
		Status:   0,
		Wrapped:  context.DeadlineExceeded,
	}
	exhausted := &providers.FallbackExhaustedError{
		Attempts: []providers.FallbackAttempt{
			{Provider: "openai_compat", Model: "gpt-4o", Error: fe, Reason: providers.FailoverTimeout},
		},
	}

	pe := errorToProviderError(exhausted)
	if assert.NotNil(t, pe) {
		assert.NotNil(t, pe.Err, "Err preserved even without inner ProviderError")
		// Status may be 0 (timeout didn't carry a status); Body empty
		// because no *ProviderError wrapped the fe. The classifier then
		// falls through to classifyByMessage on pe.Body (empty) and Err.
		assert.Equal(t, 0, pe.Status)
		assert.Empty(t, pe.Body)
	}
}

// TestProviderError_FormatCompatForClassifyError is the BLOCK 1
// regression at the provider layer: common.ProviderError.Error() must retain
// its structured `status=NNN` field so diagnostics and status extraction can
// recover the HTTP status from a stringified provider error. The HTML-wrapper
// path uses human-facing `Status: NNN` prose, but this test covers the common
// provider error type used by the normal JSON response path.
func TestProviderError_FormatCompatForClassifyError(t *testing.T) {
	pe := makeProviderErr(400, xAIMediaBody)
	msg := pe.Error()

	assert.Contains(t, msg, "status=400",
		"ProviderError.Error() must include the structured status so providers.extractHTTPStatus can recover it")

	// The common.ProviderError formatter uses the stable lowercase
	// `status=NNN` shape. Keep this assertion aligned with the provider type's
	// actual Error implementation rather than the HTML-wrapper's human-facing
	// `Status: NNN` prose.
	assert.Regexp(t, `status=400`, msg,
		"the status field must be parseable by providers.extractHTTPStatus")

	// nil receiver doesn't panic (regression — non-pointer receiver).
	var nilPE *common.ProviderError
	assert.NotPanics(t, func() {
		_ = nilPE.Error()
	}, "nil ProviderError receiver must not panic")
}
