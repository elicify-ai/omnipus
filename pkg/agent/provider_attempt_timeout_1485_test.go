package agent

// Issue #1485: every attempt of a multi-candidate fallback chain was cut at
// the fixed 120 s per-candidate limit, so a very large prompt never got an
// answer from any candidate. The real send path (callProviderOnce, chain
// branch) must give each attempt a limit that scales with the request size,
// and a model's explicit request_timeout must win for that model.
//
// Expected values come from the documented rule (docs/troubleshooting.md and
// providers.ScaledAttemptTimeout's own table test): 120 s up to 100,000
// estimated input tokens, +60 s per further started 100,000, at most 10 min.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// issue1485DeadlineProvider records the remaining time on every attempt's
// ctx. explicit, when non-zero, is the operator's configured request_timeout
// the provider reports (providers.RequestTimeoutReporter).
type issue1485DeadlineProvider struct {
	mu        sync.Mutex
	explicit  time.Duration
	remaining []time.Duration
	models    []string
}

func (*issue1485DeadlineProvider) GetDefaultModel() string { return "primary" }

func (p *issue1485DeadlineProvider) ExplicitRequestTimeout() time.Duration { return p.explicit }

func (p *issue1485DeadlineProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	remaining := time.Duration(-1)
	if dl, ok := ctx.Deadline(); ok {
		remaining = time.Until(dl)
	}
	p.remaining = append(p.remaining, remaining)
	p.models = append(p.models, model)
	return &providers.LLMResponse{Content: "answered", FinishReason: "stop"}, nil
}

func issue1485AttemptRemaining(t *testing.T, provider *issue1485DeadlineProvider, promptChars int) time.Duration {
	t.Helper()
	h := wu22NewHarness(t, provider)
	candidates := []providers.FallbackCandidate{{Provider: "big", Model: "primary"}, {Provider: "big", Model: "fallback"}}
	h.agent.Model, h.agent.Candidates = "primary", candidates
	h.agent.StoreProviderPool(map[string]providers.LLMProvider{"big": provider})
	// Production chain shape: the shipped 120 s per-candidate default.
	h.al.fallback = providers.NewFallbackChainWithTimeout(providers.NewCooldownTracker(), 0)
	rr := h.responseStage()
	rt := rr.rq.ri.rf.rt
	rt.activeCandidates, rt.activeProvider, rt.llmModel = candidates, provider, "primary"
	messages := []providers.Message{{Role: "user", Content: strings.Repeat("x", promptChars)}}
	resp, err := rt.callProviderOnce(messages, nil)
	require.NoError(t, err, "instrument: the real chain send must reach the provider")
	require.Equal(t, "answered", resp.Content)
	provider.mu.Lock()
	defer provider.mu.Unlock()
	require.Equal(t, []string{"primary"}, provider.models, "instrument: exactly the primary attempt ran")
	require.Positive(t, provider.remaining[0], "every chain attempt carries a deadline")
	return provider.remaining[0]
}

func TestIssue1485_LargePromptAttemptGetsScaledLimit(t *testing.T) {
	// 1,000,000 chars / 2.5 chars per token = 400,000 estimated tokens ->
	// 3 further started 100k blocks -> 120 s + 3*60 s = 300 s.
	got := issue1485AttemptRemaining(t, &issue1485DeadlineProvider{}, 1_000_000)
	require.Greater(t, got, 290*time.Second,
		"#1485: a ~400k-token request must get about 300 s per attempt, not the fixed 120 s; got %s", got)
	require.LessOrEqual(t, got, 10*time.Minute, "the scaled default never exceeds its 10-minute ceiling")
}

func TestIssue1485_SmallPromptKeepsTheShippedLimit(t *testing.T) {
	got := issue1485AttemptRemaining(t, &issue1485DeadlineProvider{}, 2_000)
	require.LessOrEqual(t, got, 120*time.Second, "a normal request keeps the shipped 120 s per-attempt limit")
	require.Greater(t, got, 110*time.Second, "and gets the whole of it (no fair-split slice without a turn deadline)")
}

func TestIssue1485_ExplicitRequestTimeoutWinsPerModel(t *testing.T) {
	t.Run("explicit longer limit on a small prompt", func(t *testing.T) {
		got := issue1485AttemptRemaining(t, &issue1485DeadlineProvider{explicit: 900 * time.Second}, 2_000)
		require.Greater(t, got, 890*time.Second, "#1485: an operator's request_timeout of 900 s applies to that model's attempt; got %s", got)
		require.LessOrEqual(t, got, 900*time.Second)
	})
	t.Run("explicit shorter limit on a large prompt", func(t *testing.T) {
		got := issue1485AttemptRemaining(t, &issue1485DeadlineProvider{explicit: 30 * time.Second}, 1_000_000)
		require.LessOrEqual(t, got, 30*time.Second, "an explicit request_timeout is a deliberate ceiling even for a large prompt; got %s", got)
	})
}
