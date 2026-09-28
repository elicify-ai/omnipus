// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// translate_error_provider_messages.go — the user-side half of spec §7.3:
// C-5 quota_billing, the model_retired detector, the §6 sentence assembly
// (providerMessageFor), and WireLLMError — the one assembly point that turns
// an ErrorPayload into the contract's LLMError (MAJ-001: the agent assembles
// sentence + facts + flag; the gateway hub is a thin translator).
package agent

import (
	"regexp"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// billingPhrases is the C-5 user-side billing vocabulary (dataset D2) —
// exactly the five phrases; no 402/429 numerals (the status gate is the
// classifier's job), no bare "credit balance", no "plans & billing" (a
// substring of rate-limit prose). Mirror of
// pkg/providers/error_classifier.go::billingPatterns.
var billingPhrases = []string{
	"payment required",
	"insufficient credits",
	"insufficient balance",
	"credit balance is too low",
	"credit balance too low",
}

// isBillingEvidence reports the C-5 user-side billing verdict on a provider
// response: an explicit 402 status, the structured insufficient_quota error
// code, or a C-5 phrase in the body. Never on a >=500 status. The routing
// classifier (pkg/providers) reaches the same verdict from the same inputs —
// the two classifiers must AGREE on every dataset D2 row (SC-3).
func isBillingEvidence(body string, status int) bool {
	if status >= 500 {
		return false
	}
	if status == 402 {
		return true
	}
	if common.StructuredErrorCode(body) == "insufficient_quota" {
		return true
	}
	for _, phrase := range billingPhrases {
		if strings.Contains(strings.ToLower(body), phrase) {
			return true
		}
	}
	return false
}

// modelRetiredPhrases is the C-24 model-retirement vocabulary — exactly the
// three phrases a provider emits when a model has been withdrawn. The
// detector is 404-gated (see isModelRetiredBody): a phrase without the 404
// must NOT read as retirement (a 410 or a prose-only mention stays
// CodeUnknown, byte-identical to today's copy).
var modelRetiredPhrases = []string{
	"has been decommissioned",
	"no longer supported",
	"deprecated and removed",
}

// isModelRetiredBody reports whether a 404 response body announces model
// retirement: the detector is (status == 404) AND (a retirement phrase in
// the body). Near-misses — a 410, or a body with the phrase but a different
// status — are NOT retirement: they stay CodeUnknown with byte-identical
// copy (C-24's negative rows; the media strip-retry gate is not re-pointed).
func isModelRetiredBody(body string, status int) bool {
	if status != 404 {
		return false
	}
	lower := strings.ToLower(body)
	for _, phrase := range modelRetiredPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// providerMessageFor assembles the §6 sentence for code with the provider's
// display name substituted into the {provider} slot. Returns "" when the
// code has no provider-message template (generated.LLMErrorProviderMessages),
// or the provider id is empty — the caller keeps the catalogue copy then.
func providerMessageFor(code LLMErrorCode, providerID string) string {
	if providerID == "" {
		return ""
	}
	tmpl, ok := generated.LLMErrorProviderMessages[string(code)]
	if !ok {
		return ""
	}
	return strings.ReplaceAll(tmpl, "{provider}", providers.DisplayName(providerID))
}

// WireLLMError is the ONE assembly point from an ErrorPayload to the
// contract's LLMError (MAJ-001): sentence, facts, and provider_message flag
// are assembled HERE, in the agent, and the gateway hub forwards them
// untouched. Order of assembly:
//
//  1. Fresh classification: TranslateLLMError(pe, p.Message) — when the
//     boundary error carries provider identity and the classified code has
//     a §6 template, the Message is the assembled sentence.
//  2. Curated override: when the payload carries a Code, the agent's own
//     verdict wins wholesale — code, message (already assembled by the
//     emit site), retryability, and the D1 detail. The hub never re-runs
//     classification over the agent's answer.
//  3. Facts: the failing attempt's provider/model identity and captured
//     request_id, as an anonymous literal matching the generated shape
//     (field-for-field — the compiler verifies type identity). Absent
//     identity → facts absent. NO retry keys under facts (OBS-103).
//  4. provider_message: set when the final code has a §6 provider-message
//     template AND identity is present — the marker that the message on
//     the wire is the assembled sentence (MAJ-103/C-14 write-side).
func WireLLMError(p ErrorPayload) generated.LLMError {
	translated := TranslateLLMError(p.ProviderError, p.Message)
	code := translated.Code
	message := translated.Message
	retryable := translated.Retryable
	detail := translated.Detail
	if p.Code != "" {
		code = LLMErrorCode(p.Code)
		message = p.Message
		retryable = IsRetryableCode(code)
		detail = BuildDetail(p.ProviderError, message)
	}

	providerID, modelID, requestID := "", "", ""
	if pe := p.ProviderError; pe != nil {
		providerID = pe.Provider
		modelID = pe.Model
		requestID = pe.RequestID
	}

	out := generated.LLMError{
		Code:      string(code),
		Message:   message,
		Retryable: retryable,
		Detail:    &detail,
	}
	if providerMessageFor(code, providerID) != "" {
		flag := true
		out.ProviderMessage = &flag
	}
	if providerID != "" || modelID != "" || requestID != "" {
		out.Facts = &struct {
			Model     *string `json:"model,omitempty"`
			Provider  *string `json:"provider,omitempty"`
			RequestId *string `json:"request_id,omitempty"`
		}{
			Model:     stringPtr(modelID),
			Provider:  stringPtr(providerID),
			RequestId: stringPtr(requestID),
		}
	}
	return out
}

// stringPtr returns nil for an empty string (omitted from facts), the
// address of a copy otherwise.
func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// keyShapedToken matches API-key-shaped fragments in diagnostic text
// (C-19, TestKeyFragment_NeverInMessage_C19): "sk-" followed by a key body.
// Broader than any one vendor's format on purpose — a provider body may
// echo the key in a partial or masked form ("sk-proj-****abcd"), and the
// requirement is that NO "sk-" fragment survives into the user-visible
// detail, whatever the masking.
var keyShapedToken = regexp.MustCompile(`sk-[A-Za-z0-9][A-Za-z0-9_-]{4,}`)

// scrubDetailText scrubs every registered credential out of diagnostic text
// and, on top, redacts key-shaped fragments the credential replacer cannot
// know (the provider may echo an unregistered key). The detail crosses the
// WebSocket on every error frame, so neither path may leak it.
func scrubDetailText(s string) string {
	return keyShapedToken.ReplaceAllString(logger.ScrubSensitiveValues(s), "[redacted]")
}
