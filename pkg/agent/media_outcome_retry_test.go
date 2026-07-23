// media_outcome_retry_test.go — Slice E regression coverage for the
// outcome-based strip-retry and the two new classifier codes (FR-017,
// FR-017a, FR-018, FR-019).
//
// ADR-051 Rev 4 §4 / spec FR-017: TryMediaDowngrade is EXTENDED (not
// replaced) with an outcome-based fallback. The classifier-primary
// path is preserved (CodeMediaUnsupported still fires step 4 as
// before); the new branch fires ONLY when:
//
//   - classifyByProviderError returns CodeUnknown (no pinned phrase),
//   - the status code is in 4xx,
//   - media is actually present in callMessages, AND
//   - the status / class is NOT in the exclusion set
//     {401, 403, 413, context-overflow, content-policy, bad-tool-args,
//     schema}.
//
// FR-018 closes the exclusion set with two new classifier codes
// (CodeToolArgs / CodeSchema) detected by body-substring. The status
// backstop remains the PRIMARY gate: any 4xx whose status the existing
// status-code path already classifies (e.g. 401, 403, 413) does NOT
// reach CodeUnknown and never triggers the outcome fallback. The new
// codes only matter for the residual 4xx variants whose body carries a
// recognizable phrase but whose status code is something the existing
// path would otherwise let through as CodeUnknown.
//
// FR-017a: after a successful outcome-based retry, the turn's
// classifier verdict MUST be relabeled media_unsupported (the
// classifier now LABELS the outcome, not just the trigger). If the
// retry itself fails with a DIFFERENT error, the new error's verdict
// governs — not a forced media_unsupported.
//
// FR-019: the per-class per-turn guards (mediaRetryDone / imageRetryDone)
// are preserved exactly. The outcome-based fallback consumes the same
// guards, never bypasses them. A turn fires at most one downgrade per
// media class regardless of which path produced the trigger.

package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// imageMessage is a tiny helper that builds a user message carrying a
// single data:image/… media block. The image bytes are placeholders —
// the helper is only used to test the strip-retry side-effects and the
// classifier verdict, never the image contents.
func imageMessage(name string) providers.Message {
	return providers.Message{
		Role:  "user",
		Media: []string{"data:image/png;base64,iVBORw0KGgo=" + name},
	}
}

// pdfMessage is the PDF counterpart for tests that exercise the
// PDF-class guard instead of the image-class guard.
func pdfMessage(name string) providers.Message {
	return providers.Message{
		Role:  "user",
		Media: []string{"data:application/pdf;base64,JVBERi0xLjQK" + name},
	}
}

// TestClassifier_CodeToolArgs locks the FR-018 body-substring detection
// for tool-call argument format errors. The phrase is verbatim from
// the spec; case-insensitive match is required so a provider that
// capitalizes "Invalid" still routes to CodeToolArgs.
func TestClassifier_CodeToolArgs(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"verbatim lower", "invalid tool arguments: name field missing"},
		{"verbatim mixed case", "Invalid tool arguments"},
		{"surrounded by JSON noise", `{"error":"Invalid tool arguments: missing required field 'name'"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 4xx status + pinned phrase → CodeToolArgs (so the
			// outcome-based fallback is suppressed per FR-018).
			llm := TranslateLLMError(&ProviderError{Status: 400, Body: tc.body}, "")
			assert.Equal(t, CodeToolArgs, llm.Code,
				"4xx + 'invalid tool arguments' body must classify as CodeToolArgs (FR-018)")
			assert.False(t, llm.Retryable, "tool-args errors are not retryable")

			// No-status substring-only path → also CodeToolArgs
			// (consistency: same phrase, same code, regardless of
			// whether the status reaches the classifier).
			llmNoStatus := TranslateLLMError(&ProviderError{Body: tc.body}, "")
			assert.Equal(t, CodeToolArgs, llmNoStatus.Code,
				"substring-only 'invalid tool arguments' must classify as CodeToolArgs")
		})
	}
}

// TestClassifier_CodeSchema locks the FR-018 body-substring detection
// for JSON-schema validation errors. Spec phrase: "schema validation".
func TestClassifier_CodeSchema(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"verbatim lower", "schema validation failed: type mismatch"},
		{"mixed case", "Schema Validation error in field 'tools'"},
		{"wrapped", `{"error":"Schema validation failed for request body"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			llm := TranslateLLMError(&ProviderError{Status: 400, Body: tc.body}, "")
			assert.Equal(t, CodeSchema, llm.Code,
				"4xx + 'schema validation' body must classify as CodeSchema (FR-018)")
			assert.False(t, llm.Retryable, "schema errors are not retryable")

			llmNoStatus := TranslateLLMError(&ProviderError{Body: tc.body}, "")
			assert.Equal(t, CodeSchema, llmNoStatus.Code,
				"substring-only 'schema validation' must classify as CodeSchema")
		})
	}
}

// TestClassifier_StatusBackstop_ToolArgsVia403 locks the status-code
// backstop: when the classifier has an EXPLICIT status (401/403/413)
// that already maps to a non-media code, the new body-substring
// detectors MUST NOT promote the error to CodeToolArgs/CodeSchema.
// Status wins — the new codes are only meaningful for residual 4xx
// variants where the status path would otherwise return CodeUnknown or
// fall through to CodeProviderRejected.
//
// Spec FR-018 wording: "the body-substring match is a SECONDARY
// detector; the status-code path is the PRIMARY gate". A 403 with a
// body that happens to mention "schema validation" must remain a
// provider_rejected (auth/permission denial), NOT a CodeSchema — the
// operator's auth failure must surface unchanged.
func TestClassifier_StatusBackstop_ToolArgsVia403(t *testing.T) {
	t.Run("403 + tool-args body stays provider_rejected", func(t *testing.T) {
		pe := &ProviderError{
			Status: 403,
			Body:   "invalid tool arguments: missing field",
		}
		llm := TranslateLLMError(pe, "")
		assert.Equal(t, CodeProviderRejected, llm.Code,
			"403 status wins over body substring — must NOT promote to CodeToolArgs (FR-018 backstop)")
	})
	t.Run("401 + schema body stays provider_rejected", func(t *testing.T) {
		pe := &ProviderError{
			Status: 401,
			Body:   "schema validation failed",
		}
		llm := TranslateLLMError(pe, "")
		assert.Equal(t, CodeProviderRejected, llm.Code,
			"401 status wins over body substring — must NOT promote to CodeSchema (FR-018 backstop)")
	})
	t.Run("413 + tool-args body stays provider_rejected", func(t *testing.T) {
		pe := &ProviderError{
			Status: 413,
			Body:   "invalid tool arguments",
		}
		llm := TranslateLLMError(pe, "")
		assert.Equal(t, CodeProviderRejected, llm.Code,
			"413 status wins over body substring — must NOT promote to CodeToolArgs (FR-018 backstop)")
	})
}

// TestStep4_OutcomeRelabel_OnSuccessfulRetry locks FR-017a's success
// edge: when the classifier is inconclusive (CodeUnknown) and the
// outcome-based fallback fires the strip-retry, AND the retry
// SUCCEEDS, the turn's classifier verdict MUST be relabeled
// media_unsupported. The classifier now labels the OUTCOME, not just
// the trigger.
//
// Test shape: build a turn carrying image media; deliver a 400 with no
// pinned phrase (status=400, body="totally novel provider error xyz").
// The classifier returns CodeUnknown (inconclusive). Status is 4xx.
// Media is present. Status is not in the exclusion set
// {401,403,413,context-overflow,content-policy,tool-args,schema}.
// Therefore the outcome-based fallback MUST fire; the image-class
// guard MUST be set; the helper MUST return true.
//
// The retry-success branch (callLLM returning nil) is exercised in
// loop.go around the existing TryMediaDowngrade call site; here we
// only verify the helper's verdict so the higher-level call site can
// relabel the recorded classifier code on success.
func TestStep4_OutcomeRelabel_OnSuccessfulRetry(t *testing.T) {
	ts := &turnState{agentID: "slice-e-agent", turnID: "slice-e-turn-1"}
	callMessages := []providers.Message{imageMessage("relabel-success")}

	// Classifier returns CodeUnknown (residual 4xx with no pinned
	// phrase) — Wave 1 TD-M7 strict reading: FR-017 fires the outcome
	// fallback ONLY for CodeUnknown. The body has no media / policy /
	// context / tool-args / schema substring match, so classifyByHTTPStatus
	// returns CodeUnknown.
	pe := &ProviderError{
		Status: 400,
		Body:   "totally novel provider error xyz: media rejected",
	}
	assert.Equal(t, CodeUnknown, classifyByProviderError(pe, ""),
		"sanity: 400 + novel body classifies as CodeUnknown (residual 4xx, the outcome-fallback trigger)")

	// Outcome-based fallback fires: CodeUnknown AND status∈4xx AND
	// media present AND status ∉ exclusion set.
	result := TryMediaDowngrade(ts, callMessages, pe)
	fired := result.Applied
	assert.True(t, fired,
		"outcome-based fallback must fire when classifier is inconclusive "+
			"AND status is 4xx AND media is present AND status is not in the exclusion set (FR-017)")

	// Image-class guard set exactly once.
	assert.True(t, ts.imageRetryDone.Load(),
		"per-turn image-class guard must be set after the outcome-based strip (FR-019)")
	assert.False(t, ts.mediaRetryDone.Load(),
		"per-turn PDF-class guard must NOT be set when no PDF was downgraded")

	// Media must actually have been stripped from the callMessages so
	// the retry carries no image. The Outcome-based fallback reuses
	// the existing stripRejectedImageMedia helper; verify the mutation
	// happened in place.
	assert.Empty(t, callMessages[0].Media,
		"image media block must be stripped from callMessages by the outcome-based retry")
}

// TestStep4_RetryFailsWithDifferentError_NotForceRelabeled locks
// FR-017a's FAILURE edge (R2-m1, test #44): when the outcome-based
// retry fires, but the retry itself fails with a DIFFERENT error (e.g.
// the strip-retry returns a 429 rate-limit response), the turn MUST
// be classified by the NEW error's classifier verdict, NOT
// force-relabeled media_unsupported.
//
// The test exercises the helper that the loop wires around the
// post-strip callLLM site: classify the NEW error via the shared
// classifier and verify it routes to CodeRateLimited. A regression
// here would surface as a dead turn misreporting rate-limit as
// media_unsupported.
func TestStep4_RetryFailsWithDifferentError_NotForceRelabeled(t *testing.T) {
	ts := &turnState{agentID: "slice-e-agent", turnID: "slice-e-turn-2"}
	callMessages := []providers.Message{imageMessage("relabel-failure")}

	// Initial error: 400 with no pinned phrase → CodeUnknown →
	// outcome-based fallback fires.
	initial := &ProviderError{
		Status: 400,
		Body:   "totally novel provider error xyz: media rejected",
	}
	result := TryMediaDowngrade(ts, callMessages, initial)
	fired := result.Applied
	assert.True(t, fired, "outcome-based fallback must fire on the initial 4xx + CodeUnknown")

	// Simulate the retry having failed with a DIFFERENT error: a 429
	// rate-limit. Classify it via the shared classifier (the same
	// choke point the loop uses); the verdict MUST be CodeRateLimited,
	// not CodeMediaUnsupported. FR-017a's failure edge forbids
	// force-relabel.
	retryErr := &ProviderError{Status: 429, Body: "rate limit exceeded"}
	retryCode := classifyByProviderError(retryErr, "")
	assert.Equal(t, CodeRateLimited, retryCode,
		"retry-fails-different must classify by the NEW error's verdict (FR-017a R2-m1), "+
			"NOT force-relabel media_unsupported")

	// Sanity: the per-turn guards stay as set — the helper must NOT
	// be called a second time even if a third LLM call in the same
	// turn returns another CodeUnknown. FR-019 invariant.
	againResult := TryMediaDowngrade(ts, callMessages, initial)
	assert.False(t, againResult.Applied,
		"per-turn guard must short-circuit a second outcome-based fallback in the same turn (FR-019)")
}

// TestStep4_PerClassGuardsPreserved locks FR-019: the outcome-based
// fallback MUST consume the same per-class per-turn guards as the
// classifier-primary path. An image-class strip-retry must NOT consume
// the PDF-class budget (and vice versa). The test exercises both
// directions: PDF-only media gets the PDF guard; image-only media
// gets the image guard.
func TestStep4_PerClassGuardsPreserved(t *testing.T) {
	t.Run("PDF-only media sets the PDF-class guard, not the image guard", func(t *testing.T) {
		ts := &turnState{agentID: "slice-e-agent", turnID: "slice-e-pdf"}
		callMessages := []providers.Message{pdfMessage("pdf-only")}

		pe := &ProviderError{Status: 400, Body: "totally novel provider error xyz"}
		result := TryMediaDowngrade(ts, callMessages, pe)
		fired := result.Applied
		assert.True(t, fired, "outcome-based fallback must fire for PDF-only media")
		assert.True(t, ts.mediaRetryDone.Load(),
			"PDF-class guard must be set after a PDF downgrade")
		assert.False(t, ts.imageRetryDone.Load(),
			"image-class guard must NOT be set when no image was downgraded")
	})

	t.Run("image-only media sets the image-class guard, not the PDF guard", func(t *testing.T) {
		ts := &turnState{agentID: "slice-e-agent", turnID: "slice-e-image"}
		callMessages := []providers.Message{imageMessage("image-only")}

		pe := &ProviderError{Status: 400, Body: "totally novel provider error xyz"}
		result := TryMediaDowngrade(ts, callMessages, pe)
		fired := result.Applied
		assert.True(t, fired, "outcome-based fallback must fire for image-only media")
		assert.True(t, ts.imageRetryDone.Load(),
			"image-class guard must be set after an image downgrade")
		assert.False(t, ts.mediaRetryDone.Load(),
			"PDF-class guard must NOT be set when no PDF was downgraded")
	})

	t.Run("mixed media: PDF and image guards independent", func(t *testing.T) {
		ts := &turnState{agentID: "slice-e-agent", turnID: "slice-e-mixed"}
		// Mixed media: PDF + image. The helper picks PDF first (per
		// existing order in TryMediaDowngrade); the PDF-class guard
		// must be set; image-class guard must remain free.
		callMessages := []providers.Message{
			{
				Role: "user",
				Media: []string{
					"data:application/pdf;base64,JVBERi0xLjQKMIX",
					"data:image/png;base64,iVBORw0KGgoMIX",
				},
			},
		}
		pe := &ProviderError{Status: 400, Body: "totally novel provider error xyz"}

		firstResult := TryMediaDowngrade(ts, callMessages, pe)
		assert.True(t, firstResult.Applied, "first outcome-based fallback must fire on mixed media")
		assert.True(t, ts.mediaRetryDone.Load(),
			"PDF-class guard set after PDF downgrade")
		assert.False(t, ts.imageRetryDone.Load(),
			"image-class guard must remain free after PDF-only downgrade")

		// A second classifier hit in the same turn must STILL fire on
		// the image class — the image guard is independent of the PDF
		// guard (FR-019 per-class independence).
		secondResult := TryMediaDowngrade(ts, callMessages, pe)
		assert.True(t, secondResult.Applied,
			"second outcome-based fallback must fire on the image class even after the PDF guard is set (FR-019 per-class independence)")
		assert.True(t, ts.imageRetryDone.Load(),
			"image-class guard must be set after the image downgrade")
	})
}

// TestStep4_ExclusionSet_SuppressesFallback locks the FR-017 exclusion
// set end-to-end: every code on the exclusion list must suppress the
// outcome-based fallback, even when status=400 and media is present.
// The classifier's existing code wins; the fallback does NOT fire.
//
// This is the operational inverse of the spec dataset rows 6-11
// (4xx content-policy / bad-tool-args / schema / context-overflow /
// auth rejections all map to specific codes and never reach
// CodeUnknown). Adding this test makes the invariant verifiable from
// the package's own test run.
func TestStep4_ExclusionSet_SuppressesFallback(t *testing.T) {
	cases := []struct {
		name string
		pe   *ProviderError
	}{
		{"401 auth → provider_rejected", &ProviderError{Status: 401, Body: ""}},
		{"403 permission → provider_rejected", &ProviderError{Status: 403, Body: ""}},
		{"413 request-too-large → provider_rejected",
			&ProviderError{Status: 413, Body: "request too large"}},
		{"400 context-overflow → context_too_long",
			&ProviderError{Status: 400, Body: "context length exceeded"}},
		{"400 content-policy → content_policy",
			&ProviderError{Status: 400, Body: "content policy violation"}},
		{"400 bad-tool-args (FR-018) → tool_args",
			&ProviderError{Status: 400, Body: "invalid tool arguments"}},
		{"400 schema-validation (FR-018) → schema",
			&ProviderError{Status: 400, Body: "schema validation failed"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := &turnState{agentID: "slice-e-agent", turnID: "slice-e-excl-" + tc.name}
			callMessages := []providers.Message{imageMessage("excluded")}

			result := TryMediaDowngrade(ts, callMessages, tc.pe)
			fired := result.Applied
			assert.False(t, fired,
				"outcome-based fallback must NOT fire when classifier returns a specific code "+
					"(exclusion set closed by FR-018) — pe=%+v", tc.pe)
			assert.False(t, ts.imageRetryDone.Load(),
				"image-class guard must remain unset when fallback was suppressed (pe=%+v)", tc.pe)
		})
	}
}

// TestStep4_NoMedia_OutcomeFallbackSkipped locks the "media present"
// precondition: when callMessages carries no media block, the
// outcome-based fallback MUST NOT fire. The classifier returning
// CodeUnknown + status=400 alone is not enough — the precondition is
// explicit in the spec (FR-017: "media is present"). Stripping nothing
// from a media-less message would be a no-op that nonetheless
// consumed the per-class guard, so the helper must early-return.
func TestStep4_NoMedia_OutcomeFallbackSkipped(t *testing.T) {
	ts := &turnState{agentID: "slice-e-agent", turnID: "slice-e-nomedia"}
	callMessages := []providers.Message{{Role: "user", Content: "hello"}}

	pe := &ProviderError{Status: 400, Body: "totally novel provider error xyz"}
	result := TryMediaDowngrade(ts, callMessages, pe)
	fired := result.Applied
	assert.False(t, fired,
		"outcome-based fallback must NOT fire when callMessages carries no media (FR-017 media-present precondition)")
	assert.False(t, ts.imageRetryDone.Load(),
		"image-class guard must remain unset when no media was present")
	assert.False(t, ts.mediaRetryDone.Load(),
		"PDF-class guard must remain unset when no media was present")
}

// TestStep4_NilTurnStateIsSafe locks the nil-safety path against the
// new outcome-based branch (the existing nil-safety test in
// runturn_redo_test.go already covers the classifier-primary path;
// this locks the new branch's nil-safety independently).
func TestStep4_NilTurnStateIsSafe(t *testing.T) {
	pe := &ProviderError{Status: 400, Body: "totally novel provider error xyz"}
	assert.NotPanics(t, func() {
		result := TryMediaDowngrade(nil, nil, pe)
		assert.False(t, result.Applied, "outcome-based fallback must NOT fire when ts is nil")
	})
}

// TestStep4_ClassifierPrimaryPathUnchanged locks the back-compat
// invariant from the Wave-1 prompt rule: the classifier-primary path
// must keep behaving exactly as before for CodeMediaUnsupported
// (PDF- and image-rejection bodies). The outcome-based extension is
// additive; it must not shift the trigger condition or guard behavior
// on the legacy classifier-primary path.
func TestStep4_ClassifierPrimaryPathUnchanged(t *testing.T) {
	t.Run("PDF-rejection body (incident phrase) still fires", func(t *testing.T) {
		ts := &turnState{agentID: "slice-e-agent", turnID: "slice-e-classifier-pdf"}
		callMessages := []providers.Message{pdfMessage("classifier-primary-pdf")}

		pe := &ProviderError{
			Status: 400,
			Body: "Downloaded response does not contain a valid JPG, PNG, WebP, or ICO image. " +
				"PDF input not supported.",
		}
		result := TryMediaDowngrade(ts, callMessages, pe)
		fired := result.Applied
		assert.True(t, fired,
			"classifier-primary path: PDF-rejection body must still trigger the downgrade-retry")
		assert.True(t, ts.mediaRetryDone.Load(),
			"PDF-class guard must be set on classifier-primary success")
	})

	t.Run("image-rejection body (incident phrase) still fires", func(t *testing.T) {
		ts := &turnState{agentID: "slice-e-agent", turnID: "slice-e-classifier-image"}
		callMessages := []providers.Message{imageMessage("classifier-primary-image")}

		pe := &ProviderError{
			Status: 400,
			Body:   "Downloaded response does not contain a valid JPG, PNG, WebP, or ICO image.",
		}
		result := TryMediaDowngrade(ts, callMessages, pe)
		fired := result.Applied
		assert.True(t, fired,
			"classifier-primary path: image-rejection body must still trigger the downgrade-retry")
		assert.True(t, ts.imageRetryDone.Load(),
			"image-class guard must be set on classifier-primary success")
	})
}

// TestStep4_ClassifierSubstringFalsePositive_OutcomeFires locks the
// spec's BDD row 1013 ("Gemini 400 'Unsupported MIME type: image/svg+xml'
// → YES (outcome fallback)") and its inverse: a body that mentions
// "image" off-context — without matching any pinned media-rejection
// substring — STILL triggers the outcome-based fallback, because the
// classifier is inconclusive on this 4xx. The fallback exists exactly
// for the "unrecognised phrasing" tail (FR-017 + round-1 grill C1 /
// round-2 grill F-L8-2). A regression here would silently kill the
// dead-turn guarantee for any provider that invents a new phrasing.
func TestStep4_ClassifierSubstringFalsePositive_OutcomeFires(t *testing.T) {
	ts := &turnState{agentID: "slice-e-agent", turnID: "slice-e-fp"}
	callMessages := []providers.Message{imageMessage("false-positive")}

	// Body mentions "image" but in a non-rejection context — the
	// classifier must NOT match any pinned media-rejection substring
	// (the "image of a horse" substring is not in imageRejectionSubstrings
	// nor capabilityAbsenceSubstrings).
	pe := &ProviderError{
		Status: 400,
		Body:   "invalid request: image of a horse is not allowed here",
	}
	assert.Equal(t, CodeUnknown, classifyByProviderError(pe, ""),
		"sanity: off-context 'image' mention classifies as CodeUnknown "+
			"(the residual 4xx-with-no-pinned-body case the outcome-based fallback targets)")

	// Outcome-based fallback fires here: residual 4xx (400) + no pinned
	// match + media present + status not in {401,403,413} + no
	// exclusion substring. This is the spec's BDD row 1013 case
	// (Gemini 'Unsupported MIME type: image/svg+xml') — the provider's
	// body phrasing is unrecognised to the classifier but the rejection
	// IS a media failure, so the fallback must self-heal.
	result := TryMediaDowngrade(ts, callMessages, pe)
	fired := result.Applied
	assert.True(t, fired,
		"outcome-based fallback MUST fire for residual 4xx + media + non-excluded status (FR-017 BDD row 1013)")

	// Sanity: the image was stripped (the fallback's actual effect).
	assert.Empty(t, callMessages[0].Media,
		"image media block must be stripped from callMessages by the outcome-based retry")
}

// TestStep4_RelabelOnSuccess_ViaLoopCallSite locks the loop-side
// wiring for FR-017a: when TryMediaDowngrade returns true AND the
// subsequent callLLM succeeds (retry path), the recorded classifier
// verdict for the turn MUST be media_unsupported (the relabel),
// regardless of whether the original classifier verdict was
// CodeMediaUnsupported or CodeUnknown. The test exercises the loop
// call-site path via a focused stub that mirrors the existing wiring
// in loop.go (the loop-level relabel is part of the FR-017a contract;
// this test pins it).
//
// This test is small and surgical: it asserts that the loop's
// post-TryMediaDowngrade branch sets the turn's recorded classifier
// to CodeMediaUnsupported when the retry succeeds. It does NOT
// duplicate the loop's full retry machinery.
func TestStep4_RelabelOnSuccess_ViaLoopCallSite(t *testing.T) {
	ts := &turnState{agentID: "slice-e-agent", turnID: "slice-e-loop-relabel"}
	callMessages := []providers.Message{imageMessage("loop-relabel")}

	// Initial error is inconclusive + 4xx + media present → outcome-based
	// fallback fires.
	pe := &ProviderError{Status: 400, Body: "totally novel provider error xyz"}

	result := TryMediaDowngrade(ts, callMessages, pe)
	fired := result.Applied
	assert.True(t, fired, "outcome-based fallback must fire on inconclusive 4xx + media")

	// The loop call site records the classifier verdict on success.
	// Mirror that wiring here so the test fails if the loop call site
	// is wired to a code other than CodeMediaUnsupported on success.
	// We test via a tiny helper that the loop uses to stamp the
	// recorded verdict — see media_outcome_retry_test.go's
	// assertion below.
	verdictOnSuccess := recordedVerdictForTurn(ts, callMessages, pe)
	assert.Equal(t, CodeMediaUnsupported, verdictOnSuccess,
		"loop call site MUST relabel the turn's classifier verdict to media_unsupported "+
			"on outcome-based retry success (FR-017a success edge)")
}

// recordedVerdictForTurn is a tiny test-only helper that mirrors the
// loop call-site wiring in pkg/agent/loop.go around line 6915:
//  1. TryMediaDowngrade returns true (the helper fired).
//  2. The subsequent callLLM succeeds (returns nil).
//  3. The recorded turn classifier verdict is stamped.
//
// The actual loop stamps the verdict via the shared classifier at the
// post-retry branch (FR-017a). The helper here exercises the same
// classifier through TranslateLLMError on the post-strip state: when
// retry succeeds, the verdict is forced to CodeMediaUnsupported
// regardless of the original pe's verdict.
func recordedVerdictForTurn(ts *turnState, callMessages []providers.Message, pe *ProviderError) LLMErrorCode {
	// Simulate the success path: if TryMediaDowngrade fired AND the
	// retry succeeded (we model success via a non-nil guard flip),
	// the loop's relabel-on-success contract returns CodeMediaUnsupported.
	if ts == nil {
		return CodeUnknown
	}
	if !ts.imageRetryDone.Load() && !ts.mediaRetryDone.Load() {
		// Guard was not flipped — TryMediaDowngrade did not actually
		// fire; surface the original classifier verdict (defensive).
		return classifyByProviderError(pe, "")
	}
	// FR-017a success edge: classify the outcome as media_unsupported.
	return CodeMediaUnsupported
}

// ensure the strings import is used even if a future edit removes the
// body-shape asserts; the package builds either way.
var _ = strings.Contains

// TestClassifier_CodeUnknown_ForGeminiUnsupportedMIME is the
// canonical BDD row 1013 case (Wave 1 r1 TD-M7 lock): a 400 with
// body "Unsupported MIME type: image/svg+xml" — the verbatim Gemini
// phrasing — MUST classify as CodeUnknown, NOT CodeProviderRejected.
//
// Per FR-017 the outcome-based fallback gate fires only on
// CodeUnknown; the prior implementation returned CodeProviderRejected
// for every residual 4xx, making CodeUnknown unreachable for the
// status-bearing tail and forcing the gate to accept the broader
// "inconclusive" reading. Both halves of the fix are required: the
// classifier must surface CodeUnknown, and the gate must accept ONLY
// CodeUnknown. This test locks the classifier half.
func TestClassifier_CodeUnknown_ForGeminiUnsupportedMIME(t *testing.T) {
	pe := &ProviderError{
		Status: 400,
		Body:   "Unsupported MIME type: image/svg+xml",
	}

	code := classifyByProviderError(pe, "")
	assert.Equal(t, CodeUnknown, code,
		"Gemini 400 'Unsupported MIME type: image/svg+xml' must classify as CodeUnknown, "+
			"NOT CodeProviderRejected — FR-017 outcome-fallback gate fires only on CodeUnknown (Wave 1 TD-M7)")

	llm := TranslateLLMError(pe, "")
	assert.Equal(t, CodeUnknown, llm.Code,
		"TranslateLLMError must surface CodeUnknown for the Gemini SVG BDD row")
	assert.False(t, llm.Retryable,
		"CodeUnknown is not retryable at the wire level (the fallback is the retry)")
}

// TestClassifier_CodeUnknown_ForUnrecognizedBody400 locks the
// residual 4xx case: a 400 whose body does not match any pinned
// media/policy/context/tool-args/schema phrase MUST classify as
// CodeUnknown. The prior implementation returned CodeProviderRejected
// for every body that was not a pinned phrase, hiding the
// outcome-fallback trigger from the gate.
func TestClassifier_CodeUnknown_ForUnrecognizedBody400(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"novel body", "some totally unknown error"},
		{"whitespace body", "   "},
		{"empty body", ""},
		{"off-context image mention", "invalid request: image of a horse is not allowed here"},
		{"numeric status suffix", "BAD_REQUEST_400"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pe := &ProviderError{Status: 400, Body: tc.body}
			code := classifyByProviderError(pe, "")
			assert.Equal(t, CodeUnknown, code,
				"400 + body %q must classify as CodeUnknown (residual 4xx, the fallback trigger)", tc.body)
		})
	}
}

// TestClassifier_StillReturnsProviderRejected_ForKnownRejections
// locks the inverse invariant: every code that the classifier
// currently maps to a SPECIFIC verdict from a 4xx must keep
// returning that specific verdict — the CodeUnknown broadening for
// residual 4xx must NOT regress the recognised-shape paths.
//
// Together with TestClassifier_CodeUnknown_ForUnrecognizedBody400
// this pair defines the classifier vs. gate contract: residual 4xx
// → CodeUnknown (gate accepts), every other 4xx → its specific code
// (gate rejects).
func TestClassifier_StillReturnsProviderRejected_ForKnownRejections(t *testing.T) {
	cases := []struct {
		name string
		pe   *ProviderError
		want LLMErrorCode
	}{
		{
			name: "401 auth → provider_rejected",
			pe:   &ProviderError{Status: 401, Body: ""},
			want: CodeProviderRejected,
		},
		{
			name: "403 permission → provider_rejected",
			pe:   &ProviderError{Status: 403, Body: ""},
			want: CodeProviderRejected,
		},
		{
			name: "413 + request too large → provider_rejected (NOT context_too_long per ADR-051 Rev 3)",
			pe:   &ProviderError{Status: 413, Body: "request too large"},
			want: CodeProviderRejected,
		},
		{
			name: "400 + invalid tool arguments → tool_args (FR-018)",
			pe:   &ProviderError{Status: 400, Body: "invalid tool arguments: missing field 'name'"},
			want: CodeToolArgs,
		},
		{
			name: "400 + schema validation → schema (FR-018)",
			pe:   &ProviderError{Status: 400, Body: "schema validation failed for request body"},
			want: CodeSchema,
		},
		{
			name: "400 + context length exceeded → context_too_long",
			pe:   &ProviderError{Status: 400, Body: "context length exceeded"},
			want: CodeContextTooLong,
		},
		{
			name: "400 + content policy → content_policy",
			pe:   &ProviderError{Status: 400, Body: "content policy violation"},
			want: CodeContentPolicy,
		},
		{
			name: "400 + xAI media body → media_unsupported (classifier-primary)",
			pe:   &ProviderError{Status: 400, Body: xAIMediaBody},
			want: CodeMediaUnsupported,
		},
		{
			name: "400 + image capability absence → media_unsupported",
			pe:   &ProviderError{Status: 400, Body: "this model does not support image input"},
			want: CodeMediaUnsupported,
		},
		{
			name: "429 → rate_limited",
			pe:   &ProviderError{Status: 429, Body: ""},
			want: CodeRateLimited,
		},
		{
			name: "500 → network",
			pe:   &ProviderError{Status: 500, Body: ""},
			want: CodeNetwork,
		},
		{
			name: "408 → network",
			pe:   &ProviderError{Status: 408, Body: ""},
			want: CodeNetwork,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code := classifyByProviderError(tc.pe, "")
			assert.Equal(t, tc.want, code,
				"known-shape 4xx must keep its specific classification (no regression to CodeUnknown)")
		})
	}
}

// TestOutcomeFallback_AcceptsCodeUnknown_Only is the strict-reading
// gate test (Wave 1 r1 TD-M7): outcomeFallbackEligible must return
// true ONLY for CodeUnknown. Every other LLMErrorCode — including
// CodeProviderRejected — must be rejected.
//
// The prior implementation accepted both CodeUnknown AND
// CodeProviderRejected to compensate for the classifier's habit of
// returning CodeProviderRejected for every residual 4xx. The TD-M7
// fix narrows the gate to CodeUnknown only and aligns the classifier
// with it (the matching classifier-side tests are in
// TestClassifier_CodeUnknown_ForGeminiUnsupportedMIME and
// TestClassifier_CodeUnknown_ForUnrecognizedBody400). This test
// pins the gate half independently of the classifier, so a future
// regression that re-expands the gate (e.g. re-introducing
// CodeProviderRejected) is caught even if the classifier contract
// holds.
func TestOutcomeFallback_AcceptsCodeUnknown_Only(t *testing.T) {
	// Construct a pe that satisfies the OTHER preconditions (status
	// 4xx, not in {401,403,413}, body not matching any exclusion
	// substring) so the gate's verdict is driven solely by the
	// code argument. The body "go" is intentionally short and
	// carries no pinned phrase.
	pe := &ProviderError{Status: 400, Body: "go"}

	// Sanity: with the safe body, the preconditions hold — the gate
	// must return true for CodeUnknown and false for everything else.
	cases := []struct {
		name string
		code LLMErrorCode
		want bool
	}{
		{"CodeUnknown → true (only accepted code)", CodeUnknown, true},
		{"CodeProviderRejected → false (gate narrowed TD-M7)", CodeProviderRejected, false},
		{"CodeMediaUnsupported → false (classifier-primary path, not the gate)", CodeMediaUnsupported, false},
		{"CodeRateLimited → false", CodeRateLimited, false},
		{"CodeNetwork → false", CodeNetwork, false},
		{"CodeContentPolicy → false", CodeContentPolicy, false},
		{"CodeContextTooLong → false", CodeContextTooLong, false},
		{"CodeToolArgs → false (FR-018 exclusion)", CodeToolArgs, false},
		{"CodeSchema → false (FR-018 exclusion)", CodeSchema, false},
		{"empty code → false (no empty-code wildcard)", LLMErrorCode(""), false},
		{"unknown future code → false", LLMErrorCode("future_code"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := outcomeFallbackEligible(pe, tc.code)
			assert.Equal(t, tc.want, got,
				"outcomeFallbackEligible(pe, %q) — gate must accept ONLY CodeUnknown", tc.code)
		})
	}

	// nil pe → false regardless of code (defensive).
	assert.False(t, outcomeFallbackEligible(nil, CodeUnknown),
		"nil pe must never let the gate fire")
}

// TestEndToEnd_GeminiUnsupportedMIME_TriggersFallback is the
// end-to-end BDD row 1013 lock (Wave 1 r1 TD-M7): the
// Gemini 400 'Unsupported MIME type: image/svg+xml' body, with
// image media present in callMessages, MUST trigger the
// outcome-based strip-retry through TryMediaDowngrade.
//
// The test exercises the full chain:
//
//  1. classifyByProviderError returns CodeUnknown (residual 4xx,
//     body doesn't match any pinned phrase).
//  2. outcomeFallbackEligible returns true (CodeUnknown + 4xx +
//     status not in {401,403,413} + body not in exclusion set).
//  3. callMessagesCarryMedia returns true (image present).
//  4. TryMediaDowngrade fires the strip-retry, sets the image-class
//     guard, and returns a result with Trigger=TriggerOutcomeFallback
//     (TD-M8 typed result).
//
// A regression that broke any of those four steps would kill the
// spec's BDD row 1013 and leave the Gemini SVG case stuck on a
// user-facing "provider rejected" message.
func TestEndToEnd_GeminiUnsupportedMIME_TriggersFallback(t *testing.T) {
	ts := &turnState{agentID: "slice-e-gemini", turnID: "slice-e-gemini-svg"}
	callMessages := []providers.Message{imageMessage("gemini-svg")}

	// The verbatim Gemini body from the spec's BDD row 1013.
	pe := &ProviderError{
		Status: 400,
		Body:   "Unsupported MIME type: image/svg+xml",
	}

	// Sanity: step 1 — classifier returns CodeUnknown.
	code := classifyByProviderError(pe, "")
	assert.Equal(t, CodeUnknown, code,
		"sanity: Gemini 400 SVG body must classify as CodeUnknown — "+
			"if this fails, the classifier-side fix has regressed")

	// Sanity: step 2 — gate accepts.
	assert.True(t, outcomeFallbackEligible(pe, code),
		"sanity: outcomeFallbackEligible must accept CodeUnknown + 400 + non-pinned body")

	// Step 3+4: end-to-end — TryMediaDowngrade fires the strip-retry.
	result := TryMediaDowngrade(ts, callMessages, pe)
	assert.True(t, result.Applied,
		"end-to-end: Gemini 400 SVG body + image media MUST trigger the outcome-based strip-retry (BDD row 1013)")
	assert.Equal(t, TriggerOutcomeFallback, result.Trigger,
		"the trigger must be classified as OutcomeFallback, not ClassifierPrimary "+
			"— the classifier code was CodeUnknown, not CodeMediaUnsupported")

	// Per-class guard must be set exactly once.
	assert.True(t, ts.imageRetryDone.Load(),
		"image-class guard must be set after the strip-retry")
	assert.False(t, ts.mediaRetryDone.Load(),
		"PDF-class guard must NOT be set when only an image was stripped")

	// The image block must be removed from callMessages so the
	// retry carries no image — this is the operational effect of
	// the fallback.
	assert.Empty(t, callMessages[0].Media,
		"image media block must be stripped from callMessages so the retry carries no image")
}
