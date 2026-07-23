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
// DowngradeTrigger classifies which path fired a media-downgrade retry.
// Internal-only — does NOT cross the gateway/SPA boundary (Wave 1
// TD-M8). The loop-side relabel reads this value to decide whether to
// stamp the outcome-relabel verdict on success.
type DowngradeTrigger string

const (
	// TriggerClassifierPrimary is set when the classifier returned
	// CodeMediaUnsupported directly (the xAI/Grok "valid JPG, PNG, WebP"
	// incident, capability-absence phrases, etc.).
	TriggerClassifierPrimary DowngradeTrigger = "classifier_primary"

	// TriggerOutcomeFallback is set when the classifier returned
	// CodeUnknown and the outcome-based fallback gate fired (FR-017 —
	// the Gemini 400 "Unsupported MIME type: image/svg+xml" BDD row).
	TriggerOutcomeFallback DowngradeTrigger = "outcome_fallback"

	// TriggerNone means no downgrade fired.
	TriggerNone DowngradeTrigger = "none"
)

// MediaClass is the media class affected by a downgrade. Internal-only
// (TD-M8). Closed enum: PDF, image, or "none" for the no-op case.
type MediaClass string

const (
	MediaClassPDF   MediaClass = "pdf"
	MediaClassImage MediaClass = "image"
	MediaClassNone  MediaClass = "none"
)

// MediaDowngradeResult is the typed return for TryMediaDowngrade
// (Wave 1 TD-M8). It tells the caller which path fired and which
// media class changed, so the loop can build the correct FR-017a
// outcome-relabel and any logging/audit surface can record the
// observed trigger explicitly instead of recomputing the classifier.
type MediaDowngradeResult struct {
	Applied    bool
	Trigger    DowngradeTrigger
	MediaClass MediaClass
}

// TryMediaDowngrade inspects a provider/LLM error from the current LLM
// call and, when it matches CodeMediaUnsupported OR the outcome-based
// fallback gate fires AND the turn has not already performed one
// downgrade-retry for the affected media class, mutates callMessages
// in place to remove the offending media block(s) and returns a
// MediaDowngradeResult describing what fired.
//
// Trigger paths (FR-017 / ADR-051 Rev 4 §4):
//
//  1. Classifier-primary — CodeMediaUnsupported from classifyByProviderError
//     (xAI/Grok 400 with "valid JPG, PNG, WebP, or ICO image" body, etc.).
//     Returns TriggerClassifierPrimary with the affected MediaClass.
//  2. Outcome-based fallback — fires ONLY when ALL of:
//     - classifier returned CodeUnknown,
//     - pe.Status is in 4xx range,
//     - pe.Status is NOT in {401, 403, 413},
//     - pe.Body does NOT match any of the exclusion substrings
//     (context-overflow / content-policy / bad-tool-args / schema),
//     - callMessages carries at least one media block,
//     - the matching per-class guard (mediaRetryDone or imageRetryDone)
//     is not already set.
//     Returns TriggerOutcomeFallback with the affected MediaClass.
//
// Returns the zero-value MediaDowngradeResult (Applied=false,
// Trigger=TriggerNone, MediaClass=MediaClassNone) when:
//
//   - ts is nil,
//   - neither trigger path fires,
//   - both per-class guards are already set,
//   - callMessages carries no media block to downgrade,
//   - classifier returned a specific non-media code (auth/rate/network/
//     policy/context-overflow/tool-args/schema) — those surface verbatim
//     and the loop breaks out of the retry loop normally.
func TryMediaDowngrade(ts *turnState, callMessages []providers.Message, pe *ProviderError) MediaDowngradeResult {
	if ts == nil {
		return MediaDowngradeResult{}
	}

	code := classifyByProviderError(pe, "")
	// Path 1: classifier-primary. CodeMediaUnsupported alone (regardless
	// of whether status/body fall into the outcome-based gate) keeps
	// firing as before — the per-class guards and the strip helpers are
	// unchanged.
	if code == CodeMediaUnsupported {
		result, _ := ts.applyMediaDowngrade(callMessages)
		result.Trigger = TriggerClassifierPrimary
		return result
	}

	// Path 2: outcome-based fallback. Gated on the spec's preconditions
	// (FR-017). Wave 1 TD-M7 narrows the gate to CodeUnknown only;
	// CodeProviderRejected no longer qualifies. The exclusion substrings
	// are re-checked here against pe.Body so a 4xx whose body carries
	// "schema validation" / "invalid tool arguments" / "context length
	// exceeded" / "content policy" still suppresses the fallback even
	// when the classifier returns CodeUnknown for the residual 4xx —
	// the substring check is independent of the classifier verdict and
	// serves as a second line of defense.
	if !outcomeFallbackEligible(pe, code) {
		return MediaDowngradeResult{}
	}
	if !callMessagesCarryMedia(callMessages) {
		return MediaDowngradeResult{}
	}
	result, _ := ts.applyMediaDowngrade(callMessages)
	result.Trigger = TriggerOutcomeFallback
	return result
}

// applyMediaDowngrade is the shared media-strip body used by both the
// classifier-primary and outcome-based paths. It honors the per-class
// per-turn guards (FR-019): PDF first via mediaRetryDone, then image
// via imageRetryDone, each exactly once per turn. Returns the
// MediaClass that was stripped (MediaClassPDF or MediaClassImage) or
// MediaClassNone when both guards are already set or no strippable
// block was found.
func (ts *turnState) applyMediaDowngrade(callMessages []providers.Message) (MediaDowngradeResult, MediaClass) {
	if !ts.mediaRetryDone.Load() {
		if downgradePDFMediaToText(callMessages) {
			ts.mediaRetryDone.Store(true)
			slog.Debug("media_downgrade: downgraded PDF block to extracted text (per-turn retry)",
				"agent_id", ts.agentID, "turn_id", ts.turnID)
			return MediaDowngradeResult{Applied: true}, MediaClassPDF
		}
	}

	if !ts.imageRetryDone.Load() {
		if stripRejectedImageMedia(callMessages) {
			ts.imageRetryDone.Store(true)
			slog.Debug("media_downgrade: stripped rejected image block (per-turn retry)",
				"agent_id", ts.agentID, "turn_id", ts.turnID)
			return MediaDowngradeResult{Applied: true}, MediaClassImage
		}
	}

	// Either both class guards are already set, or callMessages carried
	// no strippable block for the class whose guard was free. Surface
	// as no-downgrade-no-retry: a retry would not change the outcome.
	return MediaDowngradeResult{}, MediaClassNone
}

// outcomeFallbackEligible reports whether pe + code match the
// outcome-based fallback gate from FR-017 (Wave 1 TD-M7 strict
// reading). The gate:
//
//   - pe is non-nil and pe.Status is in 4xx range (status is 4xx),
//   - pe.Status is NOT in {401, 403, 413} (handled deterministically
//     by classifyByHTTPStatus; auth/permission/413 are not media
//     failures and the loop must not strip-retry them),
//   - code is exactly CodeUnknown — the spec is explicit: "ONLY for
//     CodeUnknown" (FR-017). Every other wire code carries a specific
//     classification; firing the fallback for them would mis-label
//     residual non-media failures as media_unsupported,
//   - pe.Body does not match any of the exclusion substrings
//     (context-overflow / content-policy / bad-tool-args / schema) —
//     these are FR-018 specific codes that MUST suppress the fallback.
//     The double-check defends against a regression where
//     classifyByHTTPStatus returns a different code for a 4xx whose
//     body happens to match one of these phrases but the substring
//     path ran in an order that missed it.
//
// The classifier was changed in Wave 1 (TD-M7) to return CodeUnknown
// for residual 4xx-with-non-pinned-body cases (the Gemini 400
// "Unsupported MIME type: image/svg+xml" BDD row is the canonical
// example). The over-broad CodeProviderRejected branch the previous
// version accepted is removed — CodeProviderRejected is a conclusive
// provider rejection (auth, 413, generic 4xx-with-non-pinned-body)
// and the outcome fallback must not fire on it.
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
	if code != CodeUnknown {
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
