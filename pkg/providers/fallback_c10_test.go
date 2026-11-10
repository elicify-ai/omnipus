// fallback_c10_test.go — C-10 (§7.4): bytes already streamed THIS attempt
// must block the in-place retry (a retry would duplicate visible and
// persisted content). Guard: fallback.go::Execute consults the ctx-carried
// StreamedBytesCheck before the rate-limit branch; the pkg/agent closure
// wires the attempt's own counter (loop_run_turn.go::callProviderOnce).
package providers

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecute_C10StreamedBytesBlockInPlaceRetry(t *testing.T) {
	t.Run("bytes this attempt blocks the retry", func(t *testing.T) {
		chain := NewFallbackChainWithTimeout(NewCooldownTracker(), 0)
		chain.waitFunc = func(ctx context.Context, d time.Duration) error { return nil }
		var calls int
		var streamed int64
		observed := 0
		ctx := WithStreamedBytesCheck(context.Background(), func() int64 {
			return atomic.LoadInt64(&streamed)
		})
		ctx = WithRetryObserver(ctx, func(RetryInfo) { observed++ })

		_, err := chain.Execute(ctx, []FallbackCandidate{{Provider: "p", Model: "m"}},
			func(ctx context.Context, provider, model string) (*LLMResponse, error) {
				calls++
				if calls == 1 {
					atomic.StoreInt64(&streamed, 42) // bytes flowed this attempt
					return nil, errors.New("429 Too Many Requests")
				}
				return nil, errors.New("unexpected second call")
			})
		require.Error(t, err)
		var exhausted *FallbackExhaustedError
		require.ErrorAs(t, err, &exhausted, "the candidate concludes exhausted, carrying the real 429")
		assert.Equal(t, 1, calls, "C-10: no in-place retry once bytes streamed this attempt")
		assert.Equal(t, 0, observed, "no retry was DECIDED, so the C-11 observer must not fire")
	})

	t.Run("zero bytes still retries in place", func(t *testing.T) {
		chain := NewFallbackChainWithTimeout(NewCooldownTracker(), 0)
		chain.waitFunc = func(ctx context.Context, d time.Duration) error { return nil }
		var calls int
		var streamed int64
		observed := 0
		ctx := WithStreamedBytesCheck(context.Background(), func() int64 {
			return atomic.LoadInt64(&streamed)
		})
		ctx = WithRetryObserver(ctx, func(RetryInfo) { observed++ })
		_, err := chain.Execute(ctx, []FallbackCandidate{{Provider: "p", Model: "m"}},
			func(ctx context.Context, provider, model string) (*LLMResponse, error) {
				calls++
				if calls == 1 {
					return nil, errors.New("429 Too Many Requests") // zero bytes: retry is safe
				}
				return &LLMResponse{Content: "second attempt"}, nil
			})
		require.NoError(t, err)
		assert.Equal(t, 2, calls, "zero streamed bytes — the in-place retry happens (C-8)")
		assert.Equal(t, 1, observed, "the decided retry fires the C-11 observer once")
	})
}
