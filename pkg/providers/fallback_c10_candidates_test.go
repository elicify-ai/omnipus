// fallback_c10_candidates_test.go — C-10 across candidates (effort-levels
// S-88/S-95 made several candidates stream): once an attempt has shown bytes,
// a failure ends the chain; the next candidate is never called. A failure
// with nothing shown still falls back exactly as before.
package providers

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

func TestFallbackChain_C10_NoFallbackAfterVisibleBytes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		shown       int64
		wantCalls   [2]int
		wantSuccess bool
	}{
		{"bytes shown then failed: the chain ends", 7, [2]int{1, 0}, false},
		{"nothing shown then failed: falls back as before", 0, [2]int{1, 1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			chain := NewFallbackChainWithTimeout(NewCooldownTracker(), 0)
			var streamed int64
			ctx := WithStreamedBytesCheck(context.Background(), func() int64 { return atomic.LoadInt64(&streamed) })
			calls := [2]int{}
			res, err := chain.Execute(ctx, []FallbackCandidate{
				{Provider: "a", Model: "m"},
				{Provider: "b", Model: "m"},
			}, func(ctx context.Context, provider, model string) (*LLMResponse, error) {
				atomic.StoreInt64(&streamed, 0)
				if provider == "a" {
					calls[0]++
					atomic.StoreInt64(&streamed, tc.shown)
					// A billing-class stream error: retriable, never retried in
					// place, so without the guard it moves to the next candidate.
					return nil, &common.StreamProviderError{Code: "402", Message: "billed partial attempt"}
				}
				calls[1]++
				return &LLMResponse{Content: "fallback answer"}, nil
			})
			require.Equal(t, tc.wantCalls, calls)
			if tc.wantSuccess {
				require.NoError(t, err)
				require.Equal(t, "fallback answer", res.Response.Content)
				return
			}
			var exhausted *FallbackExhaustedError
			require.True(t, errors.As(err, &exhausted), "the concluded failure is the chain's exhaustion error: %v", err)
			require.Len(t, exhausted.Attempts, 1)
			require.Equal(t, "a", exhausted.Attempts[0].Provider)
		})
	}
}
