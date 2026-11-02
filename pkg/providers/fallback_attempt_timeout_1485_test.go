// fallback_attempt_timeout_1485_test.go — issue #1485: the 120 s
// per-candidate limit cut off very large prompts. The per-attempt limit must
// scale with the request size (documented rule: 120 s covers up to 100,000
// input tokens, each further started 100,000 tokens adds 60 s, never more
// than 10 minutes) and an explicit per-model request_timeout must win, per
// candidate. The chain learns the per-candidate limit from a ctx-carried
// resolver (WithAttemptTimeout), consulted with the candidate being called.
package providers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIssue1485_ScaledAttemptTimeoutFollowsDocumentedRule(t *testing.T) {
	const (
		base = 120 * time.Second // shipped per-attempt default, unchanged for normal prompts
		step = 60 * time.Second  // added per further started 100,000 tokens
		ceil = 10 * time.Minute  // hard ceiling of the scaled default
	)
	for _, tc := range []struct {
		name   string
		tokens int
		want   time.Duration
	}{
		{"negative estimate is treated as empty", -5, base},
		{"empty request", 0, base},
		{"one token", 1, base},
		{"exactly 100k stays at the base", 100_000, base},
		{"one token over 100k adds one step", 100_001, base + step},
		{"exactly 200k is one step", 200_000, base + step},
		{"200k+1 is two steps", 200_001, base + 2*step},
		// The incident request (RC3): 439k tokens -> 4 further started 100k blocks.
		{"incident 439k request", 439_000, base + 4*step},
		{"800k is seven steps (540 s, under the ceiling)", 800_000, base + 7*step},
		{"900k reaches exactly the ceiling", 900_000, ceil},
		{"900k+1 is capped at the ceiling", 900_001, ceil},
		{"huge estimate is capped", 50_000_000, ceil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ScaledAttemptTimeout(tc.tokens),
				"#1485: per-attempt limit for an estimated %d input tokens", tc.tokens)
		})
	}
}

// A slow-but-healthy candidate whose resolved limit is longer than the
// chain's static per-candidate cap must be allowed to finish.
func TestIssue1485_AttemptTimeoutResolverRaisesPerCandidateLimit(t *testing.T) {
	const (
		staticCap = 50 * time.Millisecond  // stands in for the old fixed 120 s
		resolved  = 5 * time.Second        // the size-scaled / configured limit
		work      = 300 * time.Millisecond // longer than staticCap, shorter than resolved
	)
	slowRun := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		select {
		case <-time.After(work):
			return &LLMResponse{Content: "done by " + model}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	candidates := []FallbackCandidate{{Provider: "big", Model: "slow"}, {Provider: "other", Model: "fallback"}}

	t.Run("resolver wins over the static cap", func(t *testing.T) {
		chain := NewFallbackChainWithTimeout(NewCooldownTracker(), staticCap)
		ctx := WithAttemptTimeout(context.Background(), func(provider, model string) time.Duration {
			return resolved
		})
		res, err := chain.Execute(ctx, candidates, slowRun)
		require.NoError(t, err, "#1485: a request still within its resolved limit must not be cut at the static cap")
		require.Equal(t, "done by slow", res.Response.Content, "the primary answered; no fallback was needed")
		require.Empty(t, res.Attempts, "no failed attempt may be recorded")
	})

	t.Run("control: without a resolver the static cap still applies", func(t *testing.T) {
		chain := NewFallbackChainWithTimeout(NewCooldownTracker(), staticCap)
		_, err := chain.Execute(context.Background(), candidates, slowRun)
		var exhausted *FallbackExhaustedError
		require.ErrorAs(t, err, &exhausted, "both candidates exceed the 50 ms cap")
		require.Len(t, exhausted.Attempts, 2)
		for _, a := range exhausted.Attempts {
			require.ErrorIs(t, a.Error, context.DeadlineExceeded, "attempt %s/%s must fail on the static cap", a.Provider, a.Model)
		}
	})

	t.Run("a resolver answer of zero keeps the static cap", func(t *testing.T) {
		chain := NewFallbackChainWithTimeout(NewCooldownTracker(), staticCap)
		ctx := WithAttemptTimeout(context.Background(), func(provider, model string) time.Duration { return 0 })
		_, err := chain.Execute(ctx, candidates[:1], slowRun)
		var exhausted *FallbackExhaustedError
		require.ErrorAs(t, err, &exhausted)
		require.ErrorIs(t, exhausted.Attempts[0].Error, context.DeadlineExceeded)
	})
}

// The resolver is asked per candidate, with the candidate actually being
// called, so a per-model request_timeout applies to that model only.
func TestIssue1485_AttemptTimeoutResolverIsAskedPerCandidate(t *testing.T) {
	chain := NewFallbackChainWithTimeout(NewCooldownTracker(), 50*time.Millisecond)
	type pair struct{ provider, model string }
	var asked []pair
	ctx := WithAttemptTimeout(context.Background(), func(provider, model string) time.Duration {
		asked = append(asked, pair{provider, model})
		if model == "tight" {
			return 20 * time.Millisecond
		}
		return 5 * time.Second
	})
	var deadlines []time.Duration
	res, err := chain.Execute(ctx,
		[]FallbackCandidate{{Provider: "p1", Model: "tight"}, {Provider: "p2", Model: "roomy"}},
		func(ctx context.Context, provider, model string) (*LLMResponse, error) {
			dl, ok := ctx.Deadline()
			require.True(t, ok, "every chain attempt carries its own deadline")
			deadlines = append(deadlines, time.Until(dl))
			if model == "tight" {
				return nil, errors.New("connection reset by peer") // retriable non-rate-limit failure: hands off to the next candidate
			}
			return &LLMResponse{Content: "ok"}, nil
		})
	require.NoError(t, err)
	require.Equal(t, "ok", res.Response.Content)
	require.Equal(t, []pair{{"p1", "tight"}, {"p2", "roomy"}}, asked,
		"#1485: the resolver must be consulted once per attempted candidate, with that candidate's own pair")
	require.Len(t, deadlines, 2)
	require.LessOrEqual(t, deadlines[0], 20*time.Millisecond, "the tight model's own limit applies to its attempt")
	require.Greater(t, deadlines[1], time.Second, "the roomy model's own limit applies to its attempt, not the 50 ms static cap")
}
