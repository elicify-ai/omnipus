// media_downgrade.go — Per-turn media-rejection retry guard (ADR-051 RD2).
//
// Wave 1 fix pass: hoist the retry guard from a per-iteration reset (which
// allowed the loop's image-strip / PDF-text fallback paths to fire more
// than once per turn) to a single per-turn boolean on turnState. The retry
// is now gated by the classifier (CodeMediaUnsupported only) instead of
// substrings alone, and only fires once per turn even if multiple LLM
// calls in the same turn return the same media-rejection shape.

package agent

import (
	"log/slog"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// TryMediaDowngrade inspects a provider/LLM error from the current LLM
// call and, when it matches CodeMediaUnsupported OR the outcome-based
// fallback gate fires AND the turn has not already performed one
// downgrade-retry for the affected media class, mutates callMessages
// in place to remove the offending media block(s) and returns true.
// The caller should retry the LLM call once with the mutated messages;
// subsequent calls (even if they return the same shape) return false
// because the per-turn guard has flipped.
//
// Trigger paths (FR-017 / ADR-051 Rev 4 §4):
//
//  1. Classifier-primary — CodeMediaUnsupported from classifyByProviderError
//     (xAI/Grok 400 with "valid JPG, PNG, WebP, or ICO image" body, etc.).
//     Fired as before; no behavior change on this branch.
//
//  2. Outcome-based fallback — fires ONLY when ALL of:
//     - classifier returned CodeUnknown OR CodeProviderRejected
//     (i.e. inconclusive: no pinned phrase matched, and the status is
//     not auth/permission/413 which already map deterministically),
//     - pe.Status is in 4xx range,
//     - pe.Status is NOT in {401, 403, 413},
//     - pe.Body does NOT match any of the exclusion substrings
//     (context-overflow / content-policy / bad-tool-args / schema),
//     - callMessages carries at least one media block,
//     - the matching per-class guard (mediaRetryDone or imageRetryDone)
//     is not already set.
//
//     The exclusion substrings are checked here independently of the
//     classifier verdict because a 4xx with a body matching e.g.
//     "schema validation" still classifies as CodeProviderRejected under
//     the existing 4xx-body rule, and we MUST NOT fire the strip-retry
//     for that case (FR-018 + the round-2 grill F-L8-2 backstop).
//
// The downgrade path:
//
//   - Any data:application/pdf;base64,… block → downgradePDFMediaToText
//     (existing RD2 helper) — extracts text via docextract and injects it
//     into Content; the native PDF block is removed from Media.
//   - Any data:image/… block → simply removed from Media and replaced with
//     an "[attachment unavailable: <name> (provider rejected this format,
//     retrying without it)]" marker in Content.
//
// Returns false (no downgrade, no retry) when:
//
//   - ts is nil,
//   - neither trigger path fires,
//   - both per-class guards are already set,
//   - callMessages carries no media block to downgrade,
//   - classifier returned a specific non-media code (auth/rate/network/
//     policy/context-overflow/tool-args/schema) — those surface verbatim
//     and the loop breaks out of the retry loop normally.
//
// On a successful downgrade, the function stamps the matching
// per-class guard (mediaRetryDone for PDF, imageRetryDone for image)
// and emits a debug-level slog entry. The two guards are independent:
// a PDF downgrade does NOT consume the image-retry budget and vice
// versa (FR-019 per-class independence).
func TryMediaDowngrade(ts *turnState, callMessages []providers.Message, pe *ProviderError) bool {
	if ts == nil {
		return false
	}

	code := classifyByProviderError(pe, "")
	// Path 1: classifier-primary. CodeMediaUnsupported alone (regardless
	// of whether status/body fall into the outcome-based gate) keeps
	// firing as before — the per-class guards and the strip helpers are
	// unchanged.
	if code == CodeMediaUnsupported {
		return ts.applyMediaDowngrade(callMessages)
	}

	// Path 2: outcome-based fallback. Gated on the spec's preconditions
	// (FR-017). The exclusion substrings are re-checked here against
	// pe.Body so a 4xx whose body carries "schema validation" /
	// "invalid tool arguments" / "context length exceeded" / "content
	// policy" still suppresses the fallback even though the
	// classifyByHTTPStatus path returns CodeProviderRejected for those
	// shapes (the 4xx-body rule's pinned-substring lookups run before
	// the substring fallback, but only against the pinned media /
	// context / policy sets — not against the FR-018 tool-args / schema
	// shapes, which classifyByProviderError resolves via their dedicated
	// codes only when classifyByHTTPStatus reaches its 4xx branch in
	// order. The double-check below closes any pre-FR-018 regression
	// where a 4xx with a tool-args body fell through to CodeProviderRejected
	// and would have triggered a wrong-code strip-retry.)
	if !outcomeFallbackEligible(pe, code) {
		return false
	}
	if !callMessagesCarryMedia(callMessages) {
		return false
	}
	return ts.applyMediaDowngrade(callMessages)
}

// applyMediaDowngrade is the shared media-strip body used by both the
// classifier-primary and outcome-based paths. It honors the per-class
// per-turn guards (FR-019): PDF first via mediaRetryDone, then image
// via imageRetryDone, each exactly once per turn. Returns true when
// at least one block was stripped; false when both guards are already
// set or no strippable block was found.
func (ts *turnState) applyMediaDowngrade(callMessages []providers.Message) bool {
	if !ts.mediaRetryDone.Load() {
		if downgradePDFMediaToText(callMessages) {
			ts.mediaRetryDone.Store(true)
			slog.Debug("media_downgrade: downgraded PDF block to extracted text (per-turn retry)",
				"agent_id", ts.agentID, "turn_id", ts.turnID)
			return true
		}
	}

	if !ts.imageRetryDone.Load() {
		if stripRejectedImageMedia(callMessages) {
			ts.imageRetryDone.Store(true)
			slog.Debug("media_downgrade: stripped rejected image block (per-turn retry)",
				"agent_id", ts.agentID, "turn_id", ts.turnID)
			return true
		}
	}

	// Either both class guards are already set, or callMessages carried
	// no strippable block for the class whose guard was free. Surface
	// as no-downgrade-no-retry: a retry would not change the outcome.
	return false
}

// outcomeFallbackEligible reports whether pe + code match the
// outcome-based fallback gate from FR-017. The gate:
//
//   - pe is non-nil and pe.Status is in 4xx range (status is 4xx),
//   - pe.Status is NOT in {401, 403, 413} (handled deterministically
//     by classifyByHTTPStatus; auth/permission/413 are not media
//     failures and the loop must not strip-retry them),
//   - code is CodeUnknown OR (code is CodeProviderRejected AND pe.Status
//     is in 4xx range and not 401/403/413) — both represent the
//     "classifier was inconclusive" state. The CodeProviderRejected
//     branch covers residual 4xx-with-non-pinned-body cases like the
//     Gemini 400 "Unsupported MIME type: image/svg+xml" BDD row (the
//     body doesn't match any pinned substring, so the existing
//     classifier returns CodeProviderRejected, but the fallback still
//     fires per the spec — the spec's parenthetical "CodeUnknown" is
//     the practical verdict, not a strict symbol match),
//   - pe.Body does not match any of the exclusion substrings
//     (context-overflow / content-policy / bad-tool-args / schema) —
//     these are the FR-018 + FR-017 specific codes that MUST suppress
//     the fallback. The double-check defends against a regression
//     where classifyByHTTPStatus returns CodeProviderRejected for a
//     4xx whose body happens to match one of these phrases but the
//     classifier substring path ran in an order that missed it.
func outcomeFallbackEligible(pe *ProviderError, code LLMErrorCode) bool {
	if pe == nil {
		return false
	}
	if pe.Status < 400 || pe.Status > 499 {
		return false
	}
	if pe.Status == 401 || pe.Status == 403 || pe.Status == 413 {
		return false
	}
	switch code {
	case CodeUnknown:
		// ok
	case CodeProviderRejected:
		// ok — residual 4xx with no pinned body match (see doc above)
	default:
		return false
	}
	body := pe.Body
	if isContextOverflowMessage(body) ||
		isContentPolicyMessage(body) ||
		isToolArgsMessage(body) ||
		isSchemaMessage(body) {
		return false
	}
	return true
}

// callMessagesCarryMedia reports whether callMessages has at least
// one data:image/... or data:application/pdf;... block. This is the
// FR-017 "media is present" precondition — without a strippable block
// there is nothing to strip and the retry would be a no-op that
// nonetheless consumed the per-class guard, so the gate fails
// explicitly.
func callMessagesCarryMedia(callMessages []providers.Message) bool {
	const imgPrefix = "data:image/"
	const pdfPrefix = "data:application/pdf;"
	for i := range callMessages {
		for _, ref := range callMessages[i].Media {
			if strings.HasPrefix(ref, imgPrefix) || strings.HasPrefix(ref, pdfPrefix) {
				return true
			}
		}
	}
	return false
}

// stripRejectedImageMedia removes every data:image/… block from the
// selected slice of messages and replaces each with an "[attachment
// unavailable]" marker in Content. Returns true when at least one block
// was removed.
//
// Pass-2 fix: callers now pass an explicit [from, to) range so the
// helper only touches the affected message. The full messages slice is
// accepted for callers that want the original "all messages" behavior.
// Symmetric with downgradePDFMediaToText's mutation shape: the media
// block disappears, the user (and the LLM on the next attempt) sees a
// short text marker naming the file. The difference is that images have
// no extractable text — we cannot downgrade them to text content the way
// PDFs can — so we strip and annotate instead.
func stripRejectedImageMedia(messages []providers.Message, opts ...imageStripRange) bool {
	const imgPrefix = "data:image/"
	const fallbackName = "attachment"

	from, to := 0, len(messages)
	if len(opts) > 0 {
		from, to = opts[0].from, opts[0].to
	}
	from = max(from, 0)
	to = min(to, len(messages))
	if from >= to {
		return false
	}

	anyChanged := false
	for i := from; i < to; i++ {
		if len(messages[i].Media) == 0 {
			continue
		}
		kept := make([]string, 0, len(messages[i].Media))
		var injections []string
		msgChanged := false

		for _, ref := range messages[i].Media {
			if !startsWithCaseInsensitive(ref, imgPrefix) {
				kept = append(kept, ref)
				continue
			}
			msgChanged = true
			injections = append(injections,
				"\n\n[attachment unavailable: "+fallbackName+
					" (provider rejected this image format, retrying without it)]")
		}

		if msgChanged {
			messages[i].Media = kept
			if len(injections) > 0 {
				messages[i].Content = injectDocumentContent(messages[i].Content, injections)
			}
			anyChanged = true
		}
	}
	return anyChanged
}

// imageStripRange is the variadic [from, to) range hint for
// stripRejectedImageMedia. Empty (no opts) means "all messages".
type imageStripRange struct{ from, to int }

// startsWithCaseInsensitive is a tiny helper kept local to avoid pulling
// in strings.ToLower allocation when the prefix is already lowercase.
// (imageRejection prefix is lowercase ASCII; this is a hot path.)
func startsWithCaseInsensitive(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		sc := s[i]
		pc := prefix[i]
		if sc >= 'A' && sc <= 'Z' {
			sc += 32
		}
		if pc != sc {
			return false
		}
	}
	return true
}
