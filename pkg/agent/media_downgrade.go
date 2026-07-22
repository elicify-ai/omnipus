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

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// TryMediaDowngrade inspects a provider/LLM error from the current LLM
// call and, when it matches CodeMediaUnsupported AND the turn has not
// already performed one downgrade-retry, mutates callMessages in place to
// remove the offending media block(s) and returns true. The caller should
// retry the LLM call once with the mutated messages; subsequent calls
// (even if they return the same media-rejection shape) return false
// because the per-turn guard has flipped.
//
// The downgrade path:
//
//   - Any data:application/pdf;base64,… block → downgradePDFMediaToText
//     (existing RD2 helper) — extracts text via docextract and injects it
//     into Content; the native PDF block is removed from Media.
//   - Any data:image/… block → simply removed from Media and replaced with
//     an "[attachment unavailable: <name> (provider rejected this format,
//     retrying without it)]" marker in Content. The image-strip path used
//     to live inline in loop.go's retry loop and only handled the
//     "provider rejected image input" substring; the classifier
//     (translate_error.go) now owns the media-class decision, and this
//     helper consumes that decision uniformly across image and PDF.
//
// Returns false (no downgrade, no retry) when:
//
//   - err is nil or pe's code is not CodeMediaUnsupported
//   - ts.mediaRetryDone is already true (per-turn guard, hoisted in this
//     pass — previous per-iteration reset allowed retry to fire multiple
//     times in one turn, which the spec disallows).
//
// On a successful downgrade, the function stamps ts.mediaRetryDone=true
// and emits a debug-level slog entry so an operator can trace the retry
// in the gateway log.
func TryMediaDowngrade(ts *turnState, callMessages []providers.Message, pe *ProviderError) bool {
	if ts == nil {
		return false
	}

	code := classifyByProviderError(pe, "")
	if code != CodeMediaUnsupported {
		return false
	}

	// Per-class retry guard (H2 fix): each media class (PDF, image) has its
	// own per-turn atomic so a turn with mixed media can downgrade one class
	// without consuming the other's retry budget. The general mediaRetryDone
	// guards PDF; imageRetryDone guards image. Both are hoisted onto turnState
	// (not per-iteration) so each fires at most once per turn.
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

	// Classifier said media-unsupported, but no media block was present to
	// downgrade (or both class guards were already set). Surface as
	// no-downgrade-no-retry: the upstream rejection was real, but a retry
	// would not change the outcome.
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
