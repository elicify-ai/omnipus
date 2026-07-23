// runturn_redo_test.go — Wave 1 retry-guard hoist regression coverage.
//
// Two classifier matches in a single turn must NOT trigger the
// downgrade-retry twice. The per-turn guard (ts.mediaRetryDone, hoisted
// onto turnState in this pass) is the single authority that decides
// whether a retry may fire — replacing the previous per-iteration reset
// that allowed retries to fire on every loop iteration.

package agent

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// TestRunTurn_MediaRetry_FiresOncePerTurn is the regression for the
// retry-guard hoist. TryMediaDowngrade must return true on the FIRST
// classifier match in a turn, set ts.mediaRetryDone, and return false
// on every subsequent call in the same turn — even when the same
// classifier shape (CodeMediaUnsupported) fires again.
func TestRunTurn_MediaRetry_FiresOncePerTurn(t *testing.T) {
	ts := &turnState{agentID: "test-agent", turnID: "test-turn-1"}

	// Build a Media-unsupported provider error. Any status+body that
	// classifies as CodeMediaUnsupported will do — using the incident
	// shape (xAI/Grok) for realism.
	pe := &ProviderError{
		Status: 400,
		Body:   "Downloaded response does not contain a valid JPG, PNG, WebP, or ICO image.",
	}

	// Build a callMessages slice with one image data URL. TryMediaDowngrade
	// mutates messages in place (strips the offending media block) — the
	// second call sees the mutated slice but the per-turn guard still
	// blocks the retry regardless of message state.
	callMessages := []providers.Message{
		{
			Role:  "user",
			Media: []string{"data:image/png;base64,iVBORw0KGgo="},
		},
	}

	// First classifier match — retry fires.
	firstResult := TryMediaDowngrade(ts, callMessages, pe)
	assert.True(t, firstResult.Applied,
		"first classifier match in the turn must fire the downgrade-retry")
	// The test data has only image media (no PDF), so the image-class guard
	// (imageRetryDone) is the one that fires — NOT the general mediaRetryDone.
	assert.True(t, ts.imageRetryDone.Load(),
		"per-turn image-class guard must be set after an image-only downgrade")
	assert.False(t, ts.mediaRetryDone.Load(),
		"per-turn PDF guard must NOT be set when no PDF was downgraded")

	// Second classifier match in the same turn — guard blocks.
	secondResult := TryMediaDowngrade(ts, callMessages, pe)
	assert.False(t, secondResult.Applied,
		"second classifier match in the same turn must NOT fire the downgrade-retry — the per-turn guard has been set")

	// Third classifier match in the same turn — guard still blocks.
	thirdResult := TryMediaDowngrade(ts, callMessages, pe)
	assert.False(t, thirdResult.Applied,
		"third classifier match in the same turn must NOT fire the downgrade-retry")
}

// TestTryMediaDowngrade_NilTurnStateIsSafe locks the nil-safety path:
// turnState nil must NOT panic and must return false (no retry).
func TestTryMediaDowngrade_NilTurnStateIsSafe(t *testing.T) {
	pe := &ProviderError{Status: 400, Body: "media not supported"}
	assert.NotPanics(t, func() {
		result := TryMediaDowngrade(nil, nil, pe)
		assert.False(t, result.Applied)
	})
}

// TestTryMediaDowngrade_NonMediaCodeDoesNotRetry locks the
// classifier-gated behavior: only CodeMediaUnsupported triggers the
// retry. Auth / rate-limit / context-overflow / content-policy must
// NOT fire the downgrade.
func TestTryMediaDowngrade_NonMediaCodeDoesNotRetry(t *testing.T) {
	ts := &turnState{agentID: "test-agent", turnID: "test-turn-2"}
	callMessages := []providers.Message{
		{Role: "user", Media: []string{"data:image/png;base64,iVBORw0KGgo="}},
	}

	cases := []struct {
		name string
		pe   *ProviderError
	}{
		{
			name: "auth rejection",
			pe:   &ProviderError{Status: 401, Body: "invalid api key"},
		},
		{
			name: "rate-limit",
			pe:   &ProviderError{Status: 429, Body: "too many requests"},
		},
		{
			name: "context overflow",
			pe:   &ProviderError{Status: 400, Body: "context length exceeded"},
		},
		{
			name: "content policy",
			pe:   &ProviderError{Status: 400, Body: "content policy violation"},
		},
		{
			name: "5xx (network)",
			pe:   &ProviderError{Status: 500, Body: "internal server error"},
		},
		{
			name: "unknown body, no status",
			pe:   &ProviderError{Body: "totally novel provider error xyz"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := TryMediaDowngrade(ts, callMessages, tc.pe)
			assert.False(t, result.Applied,
				"non-media classifier code must NOT fire the media downgrade-retry (pe=%+v)", tc.pe)
			assert.False(t, ts.mediaRetryDone.Load(),
				"per-turn guard must remain unset when no downgrade fired")
		})
	}
}

// TestTryMediaDowngrade_AlreadyBlockedByGuard — explicitly set the
// matching guard and verify even a perfect classifier match does NOT
// retry. This is the explicit hoist invariant: once the matching-class
// guard is true, the retry is dead for the rest of the turn.
//
// Wave 2 (ADR-051 §RD2 per-class-guards): the guard is split per
// rejection class. An image-rejection retry is gated by
// ts.imageRetryDone; a PDF-rejection retry is gated by
// ts.mediaRetryDone. The test sets BOTH guards and confirms the
// image-class retry is blocked (the body here is image-rejection).
func TestTryMediaDowngrade_AlreadyBlockedByGuard(t *testing.T) {
	ts := &turnState{agentID: "test-agent", turnID: "test-turn-3"}
	ts.mediaRetryDone.Store(true)
	ts.imageRetryDone.Store(true)

	pe := &ProviderError{
		Status: 400,
		Body:   "Downloaded response does not contain a valid JPG, PNG, WebP, or ICO image.",
	}
	callMessages := []providers.Message{
		{Role: "user", Media: []string{"data:image/png;base64,iVBORw0KGgo="}},
	}

	result := TryMediaDowngrade(ts, callMessages, pe)
	assert.False(t, result.Applied,
		"the per-turn image-class guard must short-circuit even a perfect classifier match")
}

// TestStripRejectedImageMedia is a focused unit test for the image-strip
// helper. The classifier-gated TryMediaDowngrade routes to it for image
// media when downgradePDFMediaToText found no PDF block.
//
// Wave 2: the helper now takes an explicit [from, to) message-index range
// so a per-turn image rejection strips only the affected message's image
// (not every image in every message). Each subtest passes the full range
// to exercise the in-bounds mutation; the wave-2 affectedImageRange logic
// is exercised separately by the test in loop_media_normalization_test.go.
func TestStripRejectedImageMedia(t *testing.T) {
	t.Run("removes image and annotates", func(t *testing.T) {
		msgs := []providers.Message{
			{
				Role:  "user",
				Media: []string{"data:image/png;base64,iVBORw0KGgo="},
			},
		}
		changed := stripRejectedImageMedia(msgs, imageStripRange{from: 0, to: len(msgs)})
		assert.True(t, changed)
		assert.Empty(t, msgs[0].Media, "image data URL must be removed from Media")
		assert.Contains(t, msgs[0].Content, "[attachment unavailable",
			"the user (and the LLM) must see an unavailable marker")
	})
	t.Run("preserves non-image media", func(t *testing.T) {
		msgs := []providers.Message{
			{
				Role:  "user",
				Media: []string{"data:application/pdf;base64,JVBERi0xLjQK"},
			},
		}
		changed := stripRejectedImageMedia(msgs, imageStripRange{from: 0, to: len(msgs)})
		assert.False(t, changed, "PDF blocks must NOT be touched by the image-strip helper")
	})
	t.Run("empty messages", func(t *testing.T) {
		changed := stripRejectedImageMedia(nil, imageStripRange{from: 0, to: 0})
		assert.False(t, changed)
	})
	t.Run("out-of-range from is clamped", func(t *testing.T) {
		msgs := []providers.Message{
			{
				Role:  "user",
				Media: []string{"data:image/png;base64,iVBORw0KGgo="},
			},
		}
		changed := stripRejectedImageMedia(msgs, imageStripRange{from: -5, to: len(msgs)})
		assert.True(t, changed, "negative from must clamp to 0 and still strip")
		assert.Empty(t, msgs[0].Media)
	})
	t.Run("narrow range leaves other messages untouched", func(t *testing.T) {
		msgs := []providers.Message{
			{
				Role:  "user",
				Media: []string{"data:image/png;base64,iVBORw0KGgoAAAAN"},
			},
			{
				Role:  "user",
				Media: []string{"data:image/png;base64,iVBORw0KGgoAAAANOTHER"},
			},
		}
		changed := stripRejectedImageMedia(msgs, imageStripRange{from: 1, to: 2})
		assert.True(t, changed)
		assert.NotEmpty(t, msgs[0].Media,
			"image in messages[0] must NOT be stripped when range starts at 1")
		assert.Empty(t, msgs[1].Media,
			"image in messages[1] must be stripped by [1,2)")
	})
}

// TestErrorToProviderError walks a chain to extract the deepest
// FailoverError / ProviderError. Used by the runTurn error block to
// populate ErrorPayload.ProviderError.
func TestErrorToProviderError(t *testing.T) {
	t.Run("nil error → nil", func(t *testing.T) {
		assert.Nil(t, errorToProviderError(nil))
	})
	t.Run("direct provider error", func(t *testing.T) {
		want := &common.ProviderError{Status: 400, Body: "test"}
		got := errorToProviderError(want)
		assert.Equal(t, want.Status, got.Status)
		assert.Equal(t, want.Body, got.Body)
		assert.Same(t, want, got.Err, "providerErrorFromChain preserves the direct provider error")
	})
	t.Run("wrapped provider error", func(t *testing.T) {
		want := &common.ProviderError{Status: 500, Body: "server"}
		wrapped := &wrappedError{inner: want}
		got := errorToProviderError(wrapped)
		assert.Equal(t, want.Status, got.Status)
		assert.Equal(t, want.Body, got.Body)
		assert.Same(t, want, got.Err)
	})
	t.Run("unknown error → fallback pe carrying only the chain", func(t *testing.T) {
		plain := errors.New("plain error")
		got := errorToProviderError(plain)
		assert.NotNil(t, got)
		assert.Equal(t, 0, got.Status, "no status on the fallback pe")
		assert.Equal(t, "", got.Body, "no body on the fallback pe")
		assert.Same(t, plain, got.Err, "fallback pe.Err is the original error")
	})
}

// wrappedError is a tiny test helper that wraps an inner error.
type wrappedError struct {
	inner error
}

func (w *wrappedError) Error() string { return w.inner.Error() }
func (w *wrappedError) Unwrap() error { return w.inner }
