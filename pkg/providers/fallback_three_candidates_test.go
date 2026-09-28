// fallback_three_candidates_test.go — the §7.4 chain across THREE candidates:
// two rate-limited primaries exhaust their C-8 3-call caps in place, the
// third answers — retry-in-place and the D14 per-turn budget co-existing
// across a whole chain. (The 2-candidate rows live in
// fallback_retry_loop_test.go; this pins the same invariants at depth 3,
// where the fair-split budget interacts with two exhausted candidates.)
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

func TestFallbackRetryLoop_ThreeCandidates_SecondAnswers_C8_D14(t *testing.T) {
	chain := NewFallbackChainWithTimeout(NewCooldownTracker(), 0)
	chain.waitFunc = func(ctx context.Context, d time.Duration) error { return nil }

	calls := [3]int{}
	idx := func(provider string) int {
		switch provider {
		case "a":
			return 0
		case "b":
			return 1
		default:
			return 2
		}
	}
	observed := 0
	var streamed int64
	budget := NewWaitBudget(MaxWaitBudgetPerTurn)
	ctx := WithWaitBudget(context.Background(), budget)
	ctx = WithStreamedBytesCheck(ctx, func() int64 { return atomic.LoadInt64(&streamed) })
	ctx = WithRetryObserver(ctx, func(info RetryInfo) { observed++ })

	res, err := chain.Execute(ctx, []FallbackCandidate{
		{Provider: "a", Model: "m"},
		{Provider: "b", Model: "m"},
		{Provider: "c", Model: "m"},
	}, func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		i := idx(provider)
		calls[i]++
		if i < 2 { // candidates a and b: always 429 — exhaust the C-8 cap
			return nil, errors.New("429 Too Many Requests")
		}
		return &LLMResponse{Content: "third candidate answers"}, nil
	})
	require.NoError(t, err, "the third candidate must answer")
	require.NotNil(t, res)
	assert.Equal(t, "third candidate answers", res.Response.Content)
	assert.Equal(t, [3]int{3, 3, 1}, calls,
		"C-8: each rate-limited candidate gets exactly 3 total calls; the last answers on its first")
	assert.Equal(t, 4, observed,
		"C-11: two retries per exhausted candidate = 4 decided retries total")

	// D14: the four waits (2 s ± 25% and 4 s ± 25% per exhausted candidate)
	// were each Reserved in full from the shared per-turn budget — nothing
	// truncated, nothing over the cap.
	left := budget.Remaining()
	assert.Less(t, left, MaxWaitBudgetPerTurn, "waits must consume the shared budget")
	assert.GreaterOrEqual(t, left, MaxWaitBudgetPerTurn-15*time.Second,
		"the 4 backoff waits total at most ~15 s of the 10-minute budget")

	// C-10 counter stayed 0 (nothing streamed) — the guard stayed inert.
	assert.Equal(t, int64(0), atomic.LoadInt64(&streamed))
}
