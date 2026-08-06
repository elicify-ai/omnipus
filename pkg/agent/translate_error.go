// translate_error.go — Provider-capability-aware LLM error translation (ADR-051).
//
// Wave 1 fix pass: classifier gaps, retry-guard hoist, error-provenance
// hardening, rate-limit dedup correctness. Every site that surfaces a
// provider/LLM error to the user (or persists one to the transcript) MUST
// route through translateLLMError so raw provider text never reaches the
// wire and never lands on disk.
//
// The classifier is pure (no I/O, no logging) and consumed at TWO choke
// points: appendErrorTranscript (write) and the WS-forwarder EventKindError
// case (live). Both choke points share the same input (pe *ProviderError, plus
// an already-friendly message when pe==nil) and emit the same LLMError shape.

package agent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// LLMErrorCode is the canonical, machine-readable code for a translated
// LLM error. These values are wire-stable (AsyncAPI contracts/components/
// schemas/LLMError.yaml) — changing one is an API break.
type LLMErrorCode string

const (
	// CodeMediaUnsupported: provider rejected a media block (image or PDF).
	// Either capability-absence (model has no vision) or format-rejection
	// (model has vision, but the supplied format is not acceptable).
	CodeMediaUnsupported LLMErrorCode = "media_unsupported"

	// CodeProviderRejected: non-media, non-policy rejection (incl. HTTP 413
	// with "request too large"). The provider refused the request for a
	// generic reason.
	CodeProviderRejected LLMErrorCode = "provider_rejected"

	// CodeRateLimited: 429 / quota / overloaded. RateLimitFrame is the
	// authoritative live frame; the error frame is suppressed at the WS
	// forwarder.
	CodeRateLimited LLMErrorCode = "rate_limited"

	// CodeNetwork: 408 / 5xx / timeout / connection drop. Retryable.
	CodeNetwork LLMErrorCode = "network"

	// CodeContentPolicy: provider flagged content moderation / safety.
	CodeContentPolicy LLMErrorCode = "content_policy"

	// CodeContextTooLong: body indicates window exceeded (substring match;
	// not HTTP 413 — that maps to provider_rejected).
	CodeContextTooLong LLMErrorCode = "context_too_long"

	// CodeToolArgs: tool-call argument format error (FR-018 / ADR-051
	// Rev 4). Pinned body substring: "invalid tool arguments". Excluded
	// from the outcome-based strip-retry fallback so a malformed
	// tool-call is not mis-labeled as media_unsupported. Status
	// codes that already map to a specific code (401/403/413) still
	// win over the body substring — the body substring is a SECONDARY
	// detector, the status-code path is the PRIMARY gate.
	CodeToolArgs LLMErrorCode = "tool_args"

	// CodeSchema: JSON-schema validation error (FR-018 / ADR-051 Rev 4).
	// Pinned body substring: "schema validation". Excluded from the
	// outcome-based strip-retry fallback for the same reason as
	// CodeToolArgs. Status-code backstop applies identically.
	CodeSchema LLMErrorCode = "schema"

	// CodeUnknown: unclassified — fallback bucket.
	CodeUnknown LLMErrorCode = "unknown"
)

// FromModelPrefix is the attribution marker prepended to userMessages for
// upstream-caused error codes. The bubble can render this with a muted
// style to make the attribution skim-friendly without changing the copy
// itself; the string is the single source of truth for both the bubble and
// the persisted transcript. See the comment on userMessages for rationale.
const FromModelPrefix = "From the model:"

// withFromModelPrefix prefixes msg with FromModelPrefix when cond is true.
func withFromModelPrefix(cond bool, msg string) string {
	if cond {
		return FromModelPrefix + " " + msg
	}
	return msg
}

// userMessage* hold the user-facing copy that backs the userMessages map.
// The strings are kept as package constants so the literal stays under the
// 120-char line-length cap (golangci lll), and so a future test or renderer
// can reference the same source of truth. Names are pinned to their code
// for grep-ability.
const (
	userMessageMediaUnsupported = "it can’t use that attachment. Try another format, or switch to a vision-capable model."
	userMessageProviderRejected = "it refused this request. Retry once, or pick a different model."
	userMessageRateLimited      = "it’s rate-limited right now. Wait a moment, then retry."
	userMessageNetwork          = "Couldn’t reach the model. Check the network, then retry."
	userMessageContentPolicy    = "it blocked this under its safety policy. Rephrase, or remove the flagged content."
	userMessageContextTooLong   = "this chat is too long for its context window. Start a new session, or drop older turns."
	userMessageToolArgs         = "a tool call had invalid arguments. Retry the turn — Verbose chat shows which tool."
	userMessageSchema           = "the request didn’t match what it expects. Retry; if it keeps failing, switch models or check the tool setup."
	userMessageUnknown          = "it didn’t complete this turn. Retry. If it keeps failing, open Technical details (Verbose chat) or switch models."
)

// LLMError is the structured, wire-stable shape produced by the classifier.
// All four fields are required for the wire shape; Code/Message/Retryable
// must always be populated. Detail is computed live at the forwarder
// (never persisted); only the WS path surfaces it, behind Verbose Chat.
//
// Field-by-field contract:
//   - Code: one of the constants above. Use translateLLMError; never hand-pick.
//   - Message: user-facing copy. Generic over server text — no provider
//     identity, no raw body substring, no model name.
//   - Retryable: true when the operator can safely retry. False for
//     capability, content, and auth rejections.
//   - Detail: opaque diagnostic for Verbose Chat. May include provider
//     identity / model / status / body preview; NEVER persisted; NEVER
//     rendered outside Verbose Chat.
type LLMError struct {
	Code      LLMErrorCode
	Message   string
	Retryable bool
	Detail    string
}

// userMessages maps each code to its generic user-facing message. Generic
// over server text by design — the message must NOT carry provider
// identity, model names, or raw body substrings. Operators see the same
// copy regardless of which upstream rejected the request.
//
// Attribution: errors whose cause is upstream of Omnipus (model/provider
// rejections, capability, rate limits, content policy, shape, unclassified)
// are prefixed with "From the model:" so the user knows the failure is
// not a product bug — they should retry or switch models. Failures we own
// outright (5xx, fallback) carry no prefix; we take the blame. `network`
// is ambiguous and carries no prefix; the wording ("check the network")
// already disambiguates. This copy mirrors the SPA catalog in
// src/lib/llm-error.ts so the chat bubble and the persisted transcript
// stay in one voice.
//
// See docs/internal/uat/ADR-051-rev4-error-copy-review.md for the full
// rationale, including the four attribution options considered.
var userMessages = map[LLMErrorCode]string{
	CodeMediaUnsupported: withFromModelPrefix(true, userMessageMediaUnsupported),
	CodeProviderRejected: withFromModelPrefix(true, userMessageProviderRejected),
	CodeRateLimited:      withFromModelPrefix(true, userMessageRateLimited),
	CodeNetwork:          withFromModelPrefix(false, userMessageNetwork),
	CodeContentPolicy:    withFromModelPrefix(true, userMessageContentPolicy),
	CodeContextTooLong:   withFromModelPrefix(true, userMessageContextTooLong),
	CodeToolArgs:         withFromModelPrefix(true, userMessageToolArgs),
	CodeSchema:           withFromModelPrefix(true, userMessageSchema),
	CodeUnknown:          withFromModelPrefix(true, userMessageUnknown),
}

// defaultUserMessage returns the canonical user-facing message for code.
// Falls back to CodeUnknown when an unrecognized code slips through (e.g. a
// future code added in another package without an entry here) so callers
// always have a non-empty, generic message to emit.
func defaultUserMessage(code LLMErrorCode) string {
	if msg, ok := userMessages[code]; ok && msg != "" {
		return msg
	}
	// Unrecognized code — log a warning so the operator knows a code is
	// slipping through without a user-facing message (SFH-W1-02). This is
	// NOT a silent fallback; the warning provides observability for any
	// code added in the future without a matching userMessages entry.
	//
	// logger.WarnCF (not stdlib log.Printf): nothing in this project
	// redirects stdlib log output, so a bare log.Printf call writes to raw
	// stderr and never reaches gateway.log — every other WARN in this
	// package goes through the project's structured logger.
	logger.WarnCF("agent", "unrecognized LLM error code — using generic fallback",
		map[string]any{"code": string(code)})
	return userMessages[CodeUnknown]
}

// UserMessageForCode returns the canonical user-facing message for code.
// Exported for tests (and any external caller that needs to look up the
// generic copy without running the full classifier). Falls back to the
// CodeUnknown copy when code is unrecognized.
func UserMessageForCode(code LLMErrorCode) string {
	return defaultUserMessage(code)
}

// IsRetryableCode reports whether code's user-facing condition is one the
// caller can safely retry without changing inputs. Thin exported wrapper
// around isRetryable (mirrors the UserMessageForCode/defaultUserMessage
// pattern above) for callers outside this package that already have an
// LLMErrorCode in hand — e.g. the WS forwarder (pkg/gateway/websocket.go)
// deriving Retryable from an ErrorPayload.Code it is using verbatim,
// without re-running the full TranslateLLMError classification.
func IsRetryableCode(code LLMErrorCode) bool {
	return isRetryable(code)
}

// BuildDetail composes the Verbose-Chat-only detail string for a (possibly
// curated) ProviderError/message pair. Thin exported wrapper around
// buildDetail (mirrors IsRetryableCode's existing pattern) for callers
// outside this package — specifically the WS forwarder
// (pkg/gateway/websocket.go), which must recompute Detail alongside
// Code/Message/Retryable when a curated ErrorPayload.Code overrides a fresh
// TranslateLLMError classification (FIX 2, re-review), rather than reusing
// the Detail from that fresh classification's own (pe, p.Message) pair by
// coincidence.
func BuildDetail(pe *ProviderError, message string) string {
	return buildDetail(pe, message)
}

// pdfRejectionSubstrings are the body substrings that identify a 4xx
// response as a PDF-format or PDF-input rejection. Match is case-insensitive.
// Pinned by ADR-051 Rev 3 (dataset row #3 — the xAI/Grok incident string).
var pdfRejectionSubstrings = []string{
	"pdf input not supported",
	"pdf not supported",
}

// isPDFRejectionMessage reports whether body (case-insensitive) carries any
// pinned PDF-rejection substring. Used by classifyByHTTPStatus when status
// is a 4xx and the body matches the PDF shape — these are not generic
// provider rejections, they are media-class rejections, and MUST map to
// CodeMediaUnsupported (RD2 in ADR-051).
func isPDFRejectionMessage(body string) bool {
	lower := strings.ToLower(body)
	for _, s := range pdfRejectionSubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// imageRejectionSubstrings are the body substrings that identify a 4xx
// response as an image-format rejection. Match is case-insensitive.
// Pinned by ADR-051 Rev 3 (dataset row #3 — the xAI incident string).
var imageRejectionSubstrings = []string{
	"valid jpg, png, webp, or ico image", // xAI/Grok incident (verbatim)
	"unsupported image format",           // generic
	"image input not supported",          // capability-absence phrase
	"does not support image",             // capability-absence phrase
	"not support image",                  // variant
	"image not supported",                // variant
	"no image support",                   // variant
}

// isImageRejectionMessage reports whether body carries any pinned
// image-rejection substring. The xAI/Grok incident string
// ("valid JPG, PNG, WebP, or ICO image") is the canonical row; the rest
// cover capability-absence and format-rejection variants.
func isImageRejectionMessage(body string) bool {
	lower := strings.ToLower(body)
	for _, s := range imageRejectionSubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// capabilityAbsenceSubstrings match the "model has no vision / no PDF
// capability" family. These phrases are NOT errors — they are the model
// telling the operator what it can do. Mapped to CodeMediaUnsupported
// (retryable=false: switching models is the only fix).
var capabilityAbsenceSubstrings = []string{
	"does not support image",
	"image input not supported",
	"image not supported",
	"no image support",
	"not support image",
	"does not support pdf",
	"pdf input not supported",
}

// isCapabilityAbsenceMessage reports whether body carries any
// capability-absence phrase.
func isCapabilityAbsenceMessage(body string) bool {
	lower := strings.ToLower(body)
	for _, s := range capabilityAbsenceSubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// contentPolicySubstrings mark a provider's content-moderation / safety
// rejection. These phrases go to CodeContentPolicy — they typically CANNOT
// be retried (the same content will be rejected again).
var contentPolicySubstrings = []string{
	"content policy",
	"content_policy",
	"safety",
	"moderation",
	"harmful",
	"refused to",
	"refusal",
}

// isContentPolicyMessage reports whether body carries any content-policy
// substring.
func isContentPolicyMessage(body string) bool {
	lower := strings.ToLower(body)
	for _, s := range contentPolicySubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// contextOverflowSubstrings match "the conversation is too long for the
// model" rejections. NOT HTTP 413 (413 is provider_rejected per ADR-051
// Rev 3) — only substring matches here.
var contextOverflowSubstrings = []string{
	"context length exceeded",
	"context window exceeded",
	"context_length_exceeded",
	"context_window_exceeded",
	"maximum context length",
	"too many tokens",
	"prompt is too long",
}

// isContextOverflowMessage reports whether body carries any
// context-overflow substring.
func isContextOverflowMessage(body string) bool {
	lower := strings.ToLower(body)
	for _, s := range contextOverflowSubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// rateLimitSubstrings cover rate-limit denials that do not carry an HTTP
// status, including Omnipus' own in-process policy limiter. HTTP providers use
// status 429 and are classified before this fallback path.
var rateLimitSubstrings = []string{
	"rate limit",
	"rate_limit",
	"rate limited",
	"rate_limited",
	"too many requests",
	"quota exceeded",
}

func isRateLimitMessage(body string) bool {
	lower := strings.ToLower(body)
	for _, s := range rateLimitSubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// toolArgsSubstrings are the body substrings that identify a tool-call
// argument format error. Pinned by ADR-051 Rev 4 (FR-018). Match is
// case-insensitive. Excluded from the outcome-based strip-retry
// fallback so a malformed tool-call is never mis-labeled as
// media_unsupported.
var toolArgsSubstrings = []string{
	"invalid tool arguments",
}

// isToolArgsMessage reports whether body (case-insensitive) carries
// any pinned tool-args substring.
func isToolArgsMessage(body string) bool {
	lower := strings.ToLower(body)
	for _, s := range toolArgsSubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// schemaSubstrings are the body substrings that identify a JSON-schema
// validation error. Pinned by ADR-051 Rev 4 (FR-018). Match is
// case-insensitive. Excluded from the outcome-based strip-retry
// fallback for the same reason as CodeToolArgs.
var schemaSubstrings = []string{
	"schema validation",
}

// isSchemaMessage reports whether body (case-insensitive) carries
// any pinned schema substring.
func isSchemaMessage(body string) bool {
	lower := strings.ToLower(body)
	for _, s := range schemaSubstrings {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// classifyByHTTPStatus decides the LLMErrorCode for a non-nil pe with a
// real HTTP status code.
//
// PRECEDENCE — DOCUMENTED. Status code is consulted FIRST. No body
// substring wins against an explicit status code except in two narrow cases:
//   - 400 with no body (truly empty) → unknown (NOT provider_rejected, and
//     NOT rate_limited — rate_limited requires an explicit 429). CodeUnknown
//     is deliberate here: it is the residual/inconclusive verdict the
//     outcome-based strip-retry fallback gate needs (FR-017 strict
//     reading) — see the "Generic 4xx + body absent → CodeUnknown" branch
//     below and TestTranslateLLMError_Generic400_NoBody.
//   - 4xx with body matching a pinned media or capability-absence phrase →
//     CodeMediaUnsupported, regardless of the 4xx status code (the xAI
//     incident: 400 with "valid JPG, PNG, WebP, or ICO image" body).
//
// 408 / 5xx / timeout / transient → CodeNetwork.
// 429 / quota → CodeRateLimited.
// 413 + "request too large" body → CodeProviderRejected (NOT
// context_too_long — the model accepted the conversation, the request
// payload itself is too large).
// 413 with no body / generic body → CodeProviderRejected.
func classifyByHTTPStatus(pe *ProviderError) LLMErrorCode {
	if pe == nil {
		return CodeUnknown
	}

	status := pe.Status
	body := pe.Body

	switch {
	case status == 429:
		return CodeRateLimited
	case status == 408:
		return CodeNetwork
	case status >= 500 && status <= 599:
		return CodeNetwork
	case status == 413:
		// 413 + "request too large" body → provider_rejected (NOT
		// context_too_long). The model accepted the conversation; the
		// request payload itself is too large. Per ADR-051 Rev 3, this is
		// explicitly NOT context_too_long. Bodies mentioning context-window
		// overflow on a 413 are exceedingly rare; if one ever appears, the
		// context_overflow substring check below still maps it to
		// CodeContextTooLong (the user's request is fundamentally about the
		// conversation size, not the request shape).
		if isContextOverflowMessage(body) {
			return CodeContextTooLong
		}
		return CodeProviderRejected
	case status == 401, status == 403:
		// Auth — surface as provider_rejected. Distinct from rate_limit;
		// retryable=false (the operator must fix credentials). The
		// outcome-based fallback gate excludes these statuses explicitly,
		// so surfacing as ProviderRejected here does not load the fallback
		// path; it preserves the user-facing copy ("provider rejected the
		// request") for credential-related failures.
		return CodeProviderRejected
	case status >= 400 && status <= 499:
		// Generic 4xx + body absent → CodeUnknown (NOT CodeProviderRejected).
		// The spec's outcome-based fallback gate fires only on CodeUnknown
		// (FR-017 strict reading). A 4xx with no body cannot be a specific
		// rejection — every body-shape detector below needs text to match
		// against. Surfacing CodeUnknown here gives the fallback a chance
		// to fire for residual shape-unknown 4xx responses like the
		// Gemini 400 "Unsupported MIME type: image/svg+xml" BDD row.
		body = strings.TrimSpace(body)
		if body == "" {
			return CodeUnknown
		}
		// 4xx with body: check media / capability / policy substrings
		// BEFORE falling through. This is the documented exception to
		// "status wins over body" — the xAI incident is 400 with a body
		// that identifies it as media-class, and we must map it to
		// CodeMediaUnsupported, not CodeProviderRejected.
		if isPDFRejectionMessage(body) {
			return CodeMediaUnsupported
		}
		if isImageRejectionMessage(body) {
			return CodeMediaUnsupported
		}
		if isCapabilityAbsenceMessage(body) {
			return CodeMediaUnsupported
		}
		if isContentPolicyMessage(body) {
			return CodeContentPolicy
		}
		if isContextOverflowMessage(body) {
			return CodeContextTooLong
		}
		if isToolArgsMessage(body) {
			return CodeToolArgs
		}
		if isSchemaMessage(body) {
			return CodeSchema
		}
		// Residual 4xx with non-pinned body — the classifier was
		// inconclusive. Per FR-017, surface as CodeUnknown so the
		// outcome-based fallback gate can fire (Gemini 400 "Unsupported
		// MIME type: image/svg+xml" — body doesn't match any pinned
		// substring, status is 4xx, status not in {401,403,413} → fallback).
		return CodeUnknown
	}

	// Non-HTTP / status unknown → fall through to the substring path.
	return classifyByMessage(body)
}

// classifyByMessage decides the LLMErrorCode when no HTTP status is
// available (non-HTTP error, pe nil, or status==0). The substring order
// matters: capability / policy / media shape the user's mental model and
// must surface first; rate_limit (often mentioned in the same breath) must
// NOT mask a content-policy rejection.
func classifyByMessage(body string) LLMErrorCode {
	body = strings.TrimSpace(body)
	if body == "" {
		return CodeUnknown
	}
	if isCapabilityAbsenceMessage(body) {
		return CodeMediaUnsupported
	}
	if isPDFRejectionMessage(body) {
		return CodeMediaUnsupported
	}
	if isImageRejectionMessage(body) {
		return CodeMediaUnsupported
	}
	if isContentPolicyMessage(body) {
		return CodeContentPolicy
	}
	if isContextOverflowMessage(body) {
		return CodeContextTooLong
	}
	if isToolArgsMessage(body) {
		return CodeToolArgs
	}
	if isSchemaMessage(body) {
		return CodeSchema
	}
	if isRateLimitMessage(body) {
		return CodeRateLimited
	}
	return CodeUnknown
}

// classifyByProviderError is the top-level dispatch: prefer
// classifyByHTTPStatus when pe carries a real status code, else fall back
// to substring matching on the body. Both pe and message can be nil/empty;
// classifyByMessage handles that case (returns CodeUnknown).
func classifyByProviderError(pe *ProviderError, message string) LLMErrorCode {
	if pe != nil && pe.Status > 0 {
		return classifyByHTTPStatus(pe)
	}
	// No status — fall back to the message (could be the raw error string
	// at emit sites that didn't go through HandleErrorResponse).
	if pe != nil {
		return classifyByMessage(pe.Body)
	}
	return classifyByMessage(message)
}

// TranslateLLMError classifies a provider/LLM error into an LLMError.
// Called at both choke points (appendErrorTranscript write + WS-forwarder
// EventKindError live). Input is the *ProviderError threaded via ErrorPayload
// (CRIT-001); falls back to substring matching on message when status is
// unavailable. detail is derived from pe.Err; NEVER persisted; rendered
// only under Verbose Chat.
//
// Precedence is documented in classifyByHTTPStatus's doc comment. The
// returned Message is generic over server text (per ADR-051 Rev 3 D5).
func TranslateLLMError(pe *ProviderError, message string) LLMError {
	code := classifyByProviderError(pe, message)
	llm := LLMError{
		Code:      code,
		Message:   defaultUserMessage(code),
		Retryable: isRetryable(code),
		Detail:    buildDetail(pe, message),
	}
	return llm
}

// isRetryable reports whether code's user-facing condition is one the
// operator can safely retry without changing inputs.
func isRetryable(code LLMErrorCode) bool {
	switch code {
	case CodeRateLimited, CodeNetwork:
		return true
	}
	return false
}

// buildDetail composes the Verbose-Chat-only detail string from pe and
// message. detail is NEVER persisted and NEVER rendered outside Verbose
// Chat (settings toggle, src/store/chatPreferences.ts::verboseChatEnabled).
// May contain provider identity, model, status code, and a body preview —
// the operator-facing diagnostic that the user-facing Message is generic
// over.
func buildDetail(pe *ProviderError, message string) string {
	var parts []string
	if pe != nil {
		if pe.Status > 0 {
			parts = append(parts, "status="+itoa(pe.Status))
		}
		if len(pe.Body) > 0 {
			preview := strings.TrimSpace(pe.Body)
			if len(preview) > 512 {
				preview = preview[:512] + "..."
			}
			parts = append(parts, "body="+preview)
		}
	}
	if len(parts) == 0 && message != "" {
		parts = append(parts, message)
	}
	return strings.Join(parts, " ")
}

// itoa avoids pulling in strconv for a single digit-path use. Avoids an
// import cycle with packages that would import us.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ProviderError carries structured provider info up the stack so the
// classifier at the choke points can see status/body, not a stringified
// message. Created by HandleErrorResponse (provider error path) and
// WrapHTMLResponseError (HTML-shaped provider error). Converted from
// providers.FailoverError at the agent-loop boundary so every emit site
// can populate ErrorPayload.ProviderError uniformly.
//
// Defined here (pkg/agent) so the agent package owns the wire-shape seam
// the classifier consumes; providers.FallbackExhaustedError unwraps to
// this type via errors.As. Implements error (pointer receiver) so it
// can be wrapped by the chain.
type ProviderError struct {
	Status int
	Body   string
	Err    error
}

// Error implements error so *ProviderError can participate in the
// errors.As / errors.Is chain walks (CRIT-001 wiring — the classifier
// at the choke point must be reachable through the wrapped chain).
func (e *ProviderError) Error() string {
	if e == nil {
		return "<nil ProviderError>"
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("provider error: status=%d body=%s", e.Status, e.Body)
}

// Unwrap exposes the wrapped error to errors.Is / errors.As traversal.
func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ProviderErrorFromFailover converts a *providers.FailoverError (which the
// FallbackChain already populated with the last attempt's status + wrapped
// error) into a *ProviderError the classifier can read. Returns nil when
// fe is nil.
//
// Wave 2 (ADR-051 pass-2 BLOCK 3): the prior implementation copied
// Status and Err but discarded Body. With Body empty, the classifier
// fell back to substring matching on pe.Err.Error(), losing the body's
// capability/policy/context-overflow phrases. For chain-level errors
// where fe.Wrapped is nil (timeout, network drop), Body stays empty and
// the classifier uses Err's message. For wrapped *providers.ProviderError
// (the HandleErrorResponse path), the FULL body is available — we
// surface it via Body so the classifier's substring path matches media
// shapes (the xAI 400 incident).
//
// pe carries:
//   - Status: from fe.Status (the FailoverError's classified status).
//   - Body:   from the wrapped *providers.ProviderError when present;
//     empty otherwise.
//   - Err:    the wrapped error chain (preserved for Verbose-Chat Detail
//     and for downstream debugging).
func ProviderErrorFromFailover(fe *providers.FailoverError) *ProviderError {
	if fe == nil {
		return nil
	}
	pe := &ProviderError{
		Status: fe.Status,
		Err:    fe.Wrapped,
	}
	// Walk fe.Wrapped for the structured provider error. Use errors.As so
	// wrapping (e.g. an outer fmt.Errorf("...: %w", fe)) doesn't break
	// extraction — any *providers.ProviderError reachable from fe.Wrapped
	// is the body carrier.
	if fe.Wrapped != nil {
		var pp *common.ProviderError
		if errors.As(fe.Wrapped, &pp) {
			pe.Body = pp.Body
			// Preserve the original error chain on Err for Verbose-Chat
			// Detail — buildDetail joins Status + body-preview; not
			// replacing with pp keeps the wrapping context visible.
		}
	}
	return pe
}

// multiUnwrap is the contract the runtime uses to expose a chain of
// errors (Go 1.20+). The only producer of multi-errors in this code
// path is providers.FallbackExhaustedError, but the agent package does
// not import it directly to keep the dependency surface narrow — the
// interface is structurally typed, so any error implementing Unwrap()
// []error is handled correctly without a hard import.
type multiUnwrap interface{ Unwrap() []error }

// errorToProviderError walks an error chain looking for the deepest
// *providers.ProviderError (most specific, carries Status+Body), then
// falls through to *providers.FailoverError (carries Status only), and
// finally returns a synthetic *ProviderError carrying just the message
// so the classifier can still substring-match. Returns nil only when
// err is nil.
//
// Wave 2 (ADR-051 pass-2 BLOCK 3+4): the prior implementation reached
// for *providers.FailoverError FIRST and stopped — FailoverError's
// Wrapped indirection was lost, so the body the HandleErrorResponse
// path captured never reached the classifier. The xAI 400-media
// incident then misclassified as CodeProviderRejected instead of
// CodeMediaUnsupported. We now:
//
//  1. For multi-error chains (FallbackExhaustedError exposes Unwrap()
//     []error), walk the slice in REVERSE — most-recent attempt first.
//     errors.As iterates the slice from index 0, which is the EARLIEST
//     attempt — wrong order for fallback chains where the chain
//     exhausted because the LAST attempt produced the dominant error
//     class (rate-limit on the third candidate, media-shape on the
//     second after the first was 401).
//  2. Prefer the deepest *providers.ProviderError (carries Body) over
//     the *providers.FailoverError (Status only). When the wrapped pe
//     is reachable from a FailoverError.Wrapped, extract Body via
//     ProviderErrorFromFailover.
//
// Used by the runTurn error block (and any other site that surfaces a
// LLM-call failure to the assistant transcript) to thread structured
// provider data into the ErrorPayload.ProviderError field.
func errorToProviderError(err error) *ProviderError {
	if err == nil {
		return nil
	}

	// 1. Multi-error path: walk attempts (most recent first). Each attempt
	//    may be either a raw *providers.ProviderError (the
	//    HandleErrorResponse path) or wrapped in a FailoverError. We try
	//    ProviderError first (more specific — has Body); fall through to
	//    FailoverError (has Status only) for attempts that didn't go
	//    through HandleErrorResponse (e.g. plain network drop).
	if mu, ok := err.(multiUnwrap); ok {
		attempts := mu.Unwrap()
		// Defensive copy — the underlying slice is supplied by the
		// FallbackExhaustedError and is not mutated, but copying avoids
		// any accidental aliasing of caller-owned storage.
		recent := make([]error, len(attempts))
		copy(recent, attempts)
		for i := len(recent) - 1; i >= 0; i-- {
			if pe := providerErrorFromChain(recent[i]); pe != nil {
				return pe
			}
		}
		// None of the attempts produced a classifiable pe — fall through
		// to the single-error path. errors.As on a multi-unwrap err will
		// iterate from index 0 (wrong), but at this point we know no pe
		// was reachable, so the choice of direction is moot.
	}

	// 2. Single-error path: same two-step preference — pe first, then fe.
	if pe := providerErrorFromChain(err); pe != nil {
		return pe
	}

	// 3. Synthetic fallback: pe carrying only the message. The classifier
	//    uses this when no structured provider error is reachable.
	return &ProviderError{
		Status: 0,
		Body:   "",
		Err:    err,
	}
}

// providerErrorFromChain extracts the deepest structured provider error
// reachable from err. Preference order:
//
//  1. *providers.ProviderError — has Status+Body+ContentType. The body
//     is what feeds the media-shape detector at the classifier.
//  2. *providers.FailoverError — has Status only. Body is best-effort
//     via ProviderErrorFromFailover (extracted from fe.Wrapped when it
//     carries a *providers.ProviderError).
//
// Returns nil only when neither type is reachable.
func providerErrorFromChain(err error) *ProviderError {
	if err == nil {
		return nil
	}
	// Prefer the most-specific pe — it carries the Body bytes that the
	// classifier substring-matches on. errors.As will walk the Unwrap
	// chain (including Unwrap() []error) so a pe wrapped inside a
	// FailoverError wrapped inside a FallbackExhaustedError is reachable.
	var pp *common.ProviderError
	if errors.As(err, &pp) {
		// Translate the provider-side ProviderError into the agent-side
		// ProviderError. The two types share the Status field shape, but
		// the field names differ — keep an explicit copy so the agent
		// package owns the wire-shape seam.
		return &ProviderError{
			Status: pp.Status,
			Body:   pp.Body,
			Err:    pp,
		}
	}
	// Fallthrough: a FailoverError without an inner pe (timeout, context
	// canceled). Body stays empty; classifier falls back to Err.
	var fe *providers.FailoverError
	if errors.As(err, &fe) {
		return ProviderErrorFromFailover(fe)
	}
	return nil
}
