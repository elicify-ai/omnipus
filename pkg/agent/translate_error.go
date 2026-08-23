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
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
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

	// CodeProviderRejected: a genuine model refusal — the model declined to
	// answer for a reason that is neither media, policy, size, nor auth.
	//
	// ⚠️ NO CLASSIFIER PRODUCES THIS CODE TODAY, AND THAT IS DELIBERATE.
	// It used to be the catch-all for three unrelated fault owners: HTTP 413
	// (ours — we built an oversized request) and HTTP 401/403 (the operator's
	// — bad credentials). Those are now CodeRequestTooLarge and
	// CodeProviderAuthFailed, which leaves this code with no producer, because
	// the "genuine refusal" case it is NAMED for was never routed here in the
	// first place — a residual 4xx we cannot attribute falls to CodeUnknown
	// (see that constant for why it must keep doing so).
	//
	// The code is retained, NOT dead: session.TranscriptEntry.ErrorCode
	// persists it to the session JSONL (pkg/session/daypartition.go), so every
	// transcript written before this split still replays it and both message
	// catalogues must keep rendering copy for it. Re-arming it for a real
	// refusal shape needs evidence of what such a response looks like on the
	// wire — do not point residual 4xx at it to "give it a producer".
	CodeProviderRejected LLMErrorCode = "provider_rejected"

	// CodeRequestTooLarge: HTTP 413 — the request payload exceeded what the
	// model will accept. OUR fault, not the user's and not the provider's: we
	// assembled the oversized request. Distinct from CodeContextTooLong, which
	// is about the conversation exceeding the context window; here the model
	// never got as far as considering the conversation.
	CodeRequestTooLarge LLMErrorCode = "request_too_large"

	// CodeProviderAuthFailed: HTTP 401 / 403 — the provider rejected our
	// credentials. The operator's fault and the operator's fix: an API key in
	// Settings. Not retryable; retrying with the same key fails identically.
	CodeProviderAuthFailed LLMErrorCode = "provider_auth_failed"

	// CodeRateLimited: 429 / quota / overloaded. Two producers share this
	// name and must not be collapsed: Omnipus's own SEC-26 limiter emits
	// EventKindRateLimit (RateLimitFrame); an upstream provider 429 emits
	// EventKindError and MUST be forwarded as an error frame. The forwarder
	// used to drop the latter on the false premise that the former covered
	// it — it does not.
	CodeRateLimited LLMErrorCode = "rate_limited"

	// CodeNetwork: 408 / 5xx / timeout / connection drop. Retryable.
	CodeNetwork LLMErrorCode = "network"

	// CodeContentPolicy: provider flagged content moderation / safety.
	CodeContentPolicy LLMErrorCode = "content_policy"

	// CodeContextTooLong: body indicates window exceeded (substring match).
	// HTTP 413 is NOT this code — that is CodeRequestTooLarge.
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

	// CodeAgentNotConfigured: the acting agent belongs to no workspace, so
	// resolveTurnWorkDirOrRefuse refused the turn before any LLM call
	// (ErrAgentNotWorkspaceMember). Not a provider failure at all — an
	// operator-fixable setup gap, and not retryable: the agent has to be added
	// to a workspace team first.
	CodeAgentNotConfigured LLMErrorCode = "agent_not_configured"

	// CodeWorkspaceUnavailable: the agent IS on a workspace, but its working
	// folder could not be opened (disk, permissions, invalid id, or a
	// system-agent home that could not be created). Distinct from
	// CodeAgentNotConfigured — adding the agent to a team will not help.
	CodeWorkspaceUnavailable LLMErrorCode = "workspace_unavailable"

	// CodeModelUnavailable: the caller asked for a different model and the
	// switch failed. The turn continues on the previous model; this code
	// is how we say so instead of "we can't tell why".
	CodeModelUnavailable LLMErrorCode = "model_unavailable"

	// CodeNeedsProvider (ADR-068 FR-046, T068-32): a device-code provider's
	// (openai-chatgpt, xai) stored sign-in could not be refreshed — nothing
	// is stored, or a refresh attempt failed. Attribution `user` (a routine
	// re-auth prompt, not an operator setup gap — see the x-user-messages
	// catalog entry's note on the still-unreconciled ADR-067 pre-turn-gate
	// producer for this same wire code). Produced by TranslateTurnError
	// recognizing providers.ErrProviderNeedsSignIn in the error chain.
	//
	// CodeNeedsProvider (ADR-067 FR-016/FR-031/FR-038): the agent's PRIMARY
	// provider id is neither a catalog id nor a constructible custom row (an
	// id that differs only by case is exact-compared and therefore unknown —
	// FR-036), so the turn was refused before any provider call and zero
	// upstream requests were made. Attribution `config`: the copy points at
	// Settings → Providers, the one place the operator can fix it. Raised by
	// runTurn's pre-turn gate (FIRST, ahead of model_unassigned and
	// context_window_unknown) via ErrAgentNeedsProvider. When ADR-068's
	// derived needs_model is also true, this copy wins (FR-031) — a provider
	// must exist before a model can.
	CodeNeedsProvider LLMErrorCode = "needs_provider"

	// CodeTurnCanceled (ADR-066 D7, FR-034): the turn's context was cancelled
	// — Stop button, session interrupt, shutdown — before the provider
	// finished. Attribution `user`: the SPA renders it as a neutral notice,
	// not an error toast. Not retryable: the user chose to stop.
	//
	// Produced by runTurn's typed exits (see typedTurnExit in loop.go) and by
	// TranslateTurnError for any error chain carrying context.Canceled or
	// ErrTurnCanceled.
	CodeTurnCanceled LLMErrorCode = "turn_canceled"

	// CodeTurnTimedOut (ADR-066 D7, FR-034): the turn's deadline expired
	// while waiting on the provider. Attribution `provider`; retryable.
	CodeTurnTimedOut LLMErrorCode = "turn_timed_out"

	// CodeContextUnrecoverable (ADR-066 D6/D7, FR-032, FR-034): the mid-turn
	// window guard fired — after emptying every eligible tool result a
	// trigger condition still held, so no further provider call is made.
	// Attribution `product` (our bug, never the user's). Reachable only via
	// an injected fault; the guard itself lands with T066-13, this constant
	// and its attribution/copy land here so the contract round-trip closes.
	CodeContextUnrecoverable LLMErrorCode = "context_unrecoverable"

	// CodeContextWindowUnknown (ADR-066 D3, FR-008): the agent's provider is
	// a `locality: local` endpoint that reported no context window and no
	// operator override exists, so the turn was refused before any provider
	// call — never run on a guessed window. Attribution `config`: the copy
	// names the exact field to set (Settings → Models → Model overrides →
	// Context length) and must not invite a retry. Raised by runTurn's
	// pre-turn gate (third, after needs_provider and model_unassigned) via
	// ErrContextWindowUnknown. D8 is NOT adopted: nothing is ever learned
	// from provider error text — contextOverflowSubstrings only classifies.
	CodeContextWindowUnknown LLMErrorCode = "context_window_unknown"

	// CodeUnknown: unclassified — the residual/inconclusive verdict.
	//
	// ⚠️ LOAD-BEARING BEYOND ITS NAME. This code does double duty: it means
	// "we could not attribute this failure" AND it is the gate value for the
	// FR-017 media strip-retry fallback. pkg/agent/media_downgrade.go's
	// outcomeFallbackEligible fires ONLY when the classifier returns exactly
	// CodeUnknown, and TryMediaDowngrade re-runs classifyByProviderError
	// itself — so this classifier's verdict IS that gate's input.
	//
	// Concretely: a residual 4xx (a 4xx whose body matches no pinned shape,
	// e.g. the Gemini 400 "Unsupported MIME type: image/svg+xml" row) must
	// keep returning CodeUnknown. Re-pointing it at any more specific code —
	// CodeProviderRejected being the tempting one — silently disables media
	// strip-retry for every turn carrying an attachment. Silently: no error,
	// no failing test in the obvious places, just a retry that stops
	// happening. If you change the residual-4xx verdict, change
	// outcomeFallbackEligible in the same commit.
	CodeUnknown LLMErrorCode = "unknown"
)

// LLMErrorAttribution names who owns the fault behind a code — the model, the
// hosting provider, Omnipus itself ("product"), an operator-fixable setting
// ("config"), or neither/unknown. The vocabulary and the per-code tags are
// contract data (contracts/components/schemas/LLMError.yaml's
// x-user-message-attributions / x-user-messages), not a Go-side judgement call.
//
// Aliased rather than redeclared so there is exactly one set of attribution
// constants in the binary.
type LLMErrorAttribution = generated.LLMErrorAttribution

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
// GENERATED CONTENT — the strings live in the contract, not here. Edit the
// x-user-messages block in contracts/components/schemas/LLMError.yaml (and its
// three sibling copies) and re-run `make gen-contracts`. The SPA's catalogue
// (src/lib/api/generated/llm-error-messages.ts, consumed by
// src/lib/llm-error.ts) is emitted from the SAME block, which is what keeps the
// chat bubble and the persisted transcript in one voice — previously two
// hand-written maps that had to be edited in lockstep.
//
// Attribution is written into each sentence rather than carried by a blanket
// "From the model:" prefix. The prefix mechanism was deleted: it mis-attributed
// every fault upstream, including the ones Omnipus causes (an oversized request
// we built) and the ones an operator fixes in Settings (a bad API key). Each
// code now carries a machine-readable attribution tag alongside its copy — see
// AttributionForCode.
var userMessages = buildUserMessages()

// buildUserMessages converts the generated catalogue's string-keyed map into
// the LLMErrorCode-keyed map this package uses. The generator guarantees an
// entry for every code in the contract enum (codegen aborts on a gap), so this
// map is exhaustive over the wire enum by construction.
func buildUserMessages() map[LLMErrorCode]string {
	out := make(map[LLMErrorCode]string, len(generated.LLMErrorUserMessages))
	for code, message := range generated.LLMErrorUserMessages {
		out[LLMErrorCode(code)] = message
	}
	return out
}

// AttributionForCode returns the fault attribution for code — who owns the
// failure the user is looking at. Returns the CodeUnknown attribution for an
// unrecognised code, mirroring UserMessageForCode's fallback.
//
// Consumed by the copy-rule tests (a `product`/`config` message must never tell
// the user to switch models, rephrase their content, or — for `config` — retry)
// and available to any renderer that wants to style the bubble by fault owner
// rather than by code.
func AttributionForCode(code LLMErrorCode) LLMErrorAttribution {
	if attribution, ok := generated.LLMErrorUserAttributions[string(code)]; ok {
		return attribution
	}
	return generated.LLMErrorUserAttributions[string(CodeUnknown)]
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
// 413 → CodeRequestTooLarge (NOT context_too_long — the model never got as
// far as the conversation; the request payload itself is oversized, and we
// built it).
// 401 / 403 → CodeProviderAuthFailed (operator-fixable credentials).
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
		// 413 → request_too_large (NOT context_too_long). The model never
		// weighed the conversation; the request payload itself is oversized,
		// and WE assembled it — the copy for this code says so rather than
		// blaming the model. Per ADR-051 Rev 3 this is explicitly not
		// context_too_long. Bodies mentioning context-window overflow on a 413
		// are exceedingly rare; if one ever appears, the context_overflow
		// substring check still maps it to CodeContextTooLong (the user's
		// problem is genuinely the conversation size, not the request shape).
		if isContextOverflowMessage(body) {
			return CodeContextTooLong
		}
		return CodeRequestTooLarge
	case status == 401, status == 403:
		// Auth — the provider rejected OUR credentials. Distinct from
		// rate_limit and from a model refusal: it is operator-fixable in
		// Settings, and retryable=false because the same key fails
		// identically. The outcome-based fallback gate excludes these statuses
		// explicitly, and this code is not CodeUnknown, so neither path loads
		// the media strip-retry fallback.
		return CodeProviderAuthFailed
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

// TranslateTurnError classifies a turn-level error — one that reached a caller
// as a Go error value rather than as a *ProviderError — into an LLMError.
//
// Use this instead of TranslateLLMError(nil, err.Error()) at any site that
// holds the error VALUE. Stringifying first throws away the error type, and
// with it every sentinel the chain carefully preserved: a turn refused because
// the agent belongs to no workspace arrived at the user as "This turn didn't
// finish, and we can't tell why" when the cause was known exactly and was
// one setting away from being fixed.
//
// Sentinels recognised (checked with errors.Is, so wrapping is fine):
//
//   - ErrAgentNotWorkspaceMember → CodeAgentNotConfigured. Raised by
//     resolveTurnWorkDirOrRefuse (pkg/agent/workspace_reroot.go) and preserved
//     through runTurn → runAgentLoop → processMessage.
//
//   - ErrWorkspaceWorkDirUnavailable / ErrAgentHomeUnavailable →
//     CodeWorkspaceUnavailable. The agent is on a workspace (or is a system
//     agent with a home) but the folder could not be opened.
//
//   - ErrTurnCanceled / context.Canceled → CodeTurnCanceled;
//     ErrTurnTimedOut / context.DeadlineExceeded → CodeTurnTimedOut;
//     ErrContextUnrecoverable → CodeContextUnrecoverable (ADR-066 D7). The
//     typed exits in runTurn wrap both the sentinel and the raw cause, so
//     either match routes here.
//
// Anything else falls through to the substring classifier, preserving the
// previous behaviour for every other error shape.
func TranslateTurnError(err error) LLMError {
	if err == nil {
		return TranslateLLMError(nil, "")
	}
	if code, ok := typedExitCode(err); ok {
		return typedExitError(code, err)
	}
	if errors.Is(err, ErrAgentNotWorkspaceMember) {
		return LLMError{
			Code:      CodeAgentNotConfigured,
			Message:   defaultUserMessage(CodeAgentNotConfigured),
			Retryable: isRetryable(CodeAgentNotConfigured),
			// Our own refusal text, not provider text — safe as a
			// Verbose-Chat detail and never persisted.
			Detail: buildDetail(nil, err.Error()),
		}
	}
	if errors.Is(err, ErrWorkspaceWorkDirUnavailable) || errors.Is(err, ErrAgentHomeUnavailable) {
		return LLMError{
			Code:      CodeWorkspaceUnavailable,
			Message:   defaultUserMessage(CodeWorkspaceUnavailable),
			Retryable: isRetryable(CodeWorkspaceUnavailable),
			Detail:    buildDetail(nil, err.Error()),
		}
	}
	// Two distinct causes map to needs_provider: the agent has no usable
	// provider at all (T067-09, ErrAgentNeedsProvider), and a subscription
	// sign-in that can no longer be refreshed (ADR-068 §8b FR-046,
	// providers.ErrProviderNeedsSignIn). Both are the user's to resolve in
	// Settings -> Providers, so they share the code; keep BOTH checks.
	if errors.Is(err, ErrAgentNeedsProvider) || errors.Is(err, providers.ErrProviderNeedsSignIn) {
		return LLMError{
			Code:      CodeNeedsProvider,
			Message:   defaultUserMessage(CodeNeedsProvider),
			Retryable: isRetryable(CodeNeedsProvider),
			// The token source's own error text may name the provider id;
			// safe as a Verbose-Chat detail (never the refresh/access token
			// itself — the sentinel error never carries either).
			Detail: buildDetail(nil, err.Error()),
		}
	}
	if errors.Is(err, ErrContextWindowUnknown) {
		return LLMError{
			Code:      CodeContextWindowUnknown,
			Message:   defaultUserMessage(CodeContextWindowUnknown),
			Retryable: isRetryable(CodeContextWindowUnknown),
			Detail:    buildDetail(nil, err.Error()),
		}
	}
	return TranslateLLMError(nil, err.Error())
}

// Typed turn-exit sentinels (ADR-066 D7, FR-034). runTurn's formerly silent
// return sites wrap one of these together with the raw cause
// (`fmt.Errorf("%w: %w", ErrTurnCanceled, cause)`), so a caller can errors.Is
// either the sentinel or the underlying context error. TranslateTurnError
// maps each to its typed code.
var (
	// ErrTurnCanceled: the turn context was cancelled before the provider
	// finished (Stop button, interrupt, shutdown).
	ErrTurnCanceled = errors.New("turn canceled")
	// ErrTurnTimedOut: the turn deadline expired while waiting on the provider.
	ErrTurnTimedOut = errors.New("turn timed out")
	// ErrContextUnrecoverable: the mid-turn window guard fired (T066-13
	// raises it); no further provider call is made.
	ErrContextUnrecoverable = errors.New("context unrecoverable after emptying every eligible tool result")
)

// typedExitCode reports the ADR-066 D7 exit code carried by err's chain, if
// any. The sentinel and the raw context error are both accepted so callers
// that see only the unwrapped context error (e.g. a provider returning
// context.Canceled directly) classify identically to callers that see the
// wrapped runTurn return.
func typedExitCode(err error) (LLMErrorCode, bool) {
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, ErrContextUnrecoverable):
		return CodeContextUnrecoverable, true
	case errors.Is(err, ErrTurnCanceled), errors.Is(err, context.Canceled):
		return CodeTurnCanceled, true
	case errors.Is(err, ErrTurnTimedOut), errors.Is(err, context.DeadlineExceeded):
		return CodeTurnTimedOut, true
	}
	return "", false
}

// typedExitError builds the LLMError for a typed exit: catalogue copy for the
// code, contract retryability, and the raw cause confined to the
// Verbose-Chat-only Detail (never the Message, never persisted).
func typedExitError(code LLMErrorCode, cause error) LLMError {
	detail := ""
	if cause != nil {
		detail = cause.Error()
	}
	return LLMError{
		Code:      code,
		Message:   defaultUserMessage(code),
		Retryable: isRetryable(code),
		Detail:    buildDetail(nil, detail),
	}
}

// isRetryable reports whether code's user-facing condition is one the
// operator can safely retry without changing inputs. Everything else —
// capability, content, size, auth, and setup failures — fails identically on
// retry and is reported as not retryable. A timed-out turn is retryable (the
// provider may answer next time); a cancelled one is not (the user stopped
// it); an unrecoverable context is not (retrying re-runs the same overflow).
func isRetryable(code LLMErrorCode) bool {
	switch code {
	case CodeRateLimited, CodeNetwork, CodeTurnTimedOut:
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
