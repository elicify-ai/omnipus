package agent

// Issue #1478 (and the #1280 recovery note): while a provider call is silent
// the turn publishes ONE provider_status "waiting" event after a short silence
// (the 5-minute silence limit itself is unchanged), a "responding" event when
// data arrives again or the call returns, and a "recovered" event when a call
// succeeded after automatic retries. The thresholds are shortened through the
// package seams so the tests run in milliseconds.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

func issue1478ShortSilence(t *testing.T) time.Duration {
	t.Helper()
	savedAfter, savedEvery := providerSilenceNoticeAfter, providerSilenceCheckEvery
	providerSilenceNoticeAfter, providerSilenceCheckEvery = 60*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { providerSilenceNoticeAfter, providerSilenceCheckEvery = savedAfter, savedEvery })
	return providerSilenceNoticeAfter
}

// issue1478Provider runs script inside Chat with the attempt's real ctx.
type issue1478Provider struct {
	mu     sync.Mutex
	calls  int
	script func(ctx context.Context, call int) (*providers.LLMResponse, error)
}

func (*issue1478Provider) GetDefaultModel() string { return "m" }

func (p *issue1478Provider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()
	return p.script(ctx, call)
}

func issue1478Statuses(h *wu22Harness) []ProviderStatusPayload {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []ProviderStatusPayload
	for _, e := range h.events {
		if e.Kind == EventKindProviderStatus {
			if p, ok := e.Payload.(ProviderStatusPayload); ok {
				out = append(out, p)
			}
		}
	}
	return out
}

func issue1478Call(t *testing.T, provider *issue1478Provider) (*wu22Harness, time.Time, error) {
	t.Helper()
	h := wu22NewHarness(t, provider)
	candidates := []providers.FallbackCandidate{{Provider: "p", Model: "m"}}
	h.agent.Model, h.agent.Candidates = "m", candidates
	h.agent.StoreProviderPool(map[string]providers.LLMProvider{"p": provider})
	h.al.fallback = providers.NewFallbackChain(providers.NewCooldownTracker()).
		WithRetryWait(func(ctx context.Context, _ time.Duration) error { return ctx.Err() })
	rr := h.responseStage()
	rt := rr.rq.ri.rf.rt
	rt.activeCandidates, rt.activeProvider, rt.llmModel = candidates, provider, "m"
	callStart := time.Now()
	_, err := rt.callProviderOnce([]providers.Message{{Role: "user", Content: "hello"}}, nil)
	return h, callStart, err
}

func TestIssue1478_SilentCallPublishesWaitingThenResponding(t *testing.T) {
	after := issue1478ShortSilence(t)
	provider := &issue1478Provider{script: func(ctx context.Context, _ int) (*providers.LLMResponse, error) {
		time.Sleep(5 * after) // a non-streamed call that says nothing for a while
		return &providers.LLMResponse{Content: "late but fine"}, nil
	}}
	h, start, err := issue1478Call(t, provider)
	require.NoError(t, err)
	got := issue1478Statuses(h)
	require.Len(t, got, 2, "#1478: exactly one waiting notice and one clear, got %+v", got)
	require.Equal(t, "waiting", got[0].State)
	require.Equal(t, string(h.ts.routingSessionID), got[0].SessionID)
	require.WithinDuration(t, start, got[0].SilentSince, after, "silence is counted from the start of the call when nothing arrived")
	require.GreaterOrEqual(t, got[0].SentAt.Sub(got[0].SilentSince), after, "the notice comes only after the short silence")
	require.True(t, got[0].StallAt.IsZero(), "a non-streamed call has no silence limit to announce")
	require.Equal(t, "responding", got[1].State, "the call returned: the notice is cleared")
}

func TestIssue1478_StreamSilenceAnnouncesTheSilenceLimitAndClearsOnData(t *testing.T) {
	after := issue1478ShortSilence(t)
	const stallLimit = 7 * time.Minute // the model's stream_stall_timeout
	provider := &issue1478Provider{script: func(ctx context.Context, _ int) (*providers.LLMResponse, error) {
		// What a streaming transport does: start the silence monitor, go
		// quiet, then receive an event (Arm), then finish quickly.
		w := common.WatchStreamStall(ctx, func() {}, stallLimit)
		defer w.Stop()
		time.Sleep(4 * after)
		w.Arm()
		return &providers.LLMResponse{Content: "streamed"}, nil
	}}
	h, _, err := issue1478Call(t, provider)
	require.NoError(t, err)
	got := issue1478Statuses(h)
	require.Len(t, got, 2, "got %+v", got)
	require.Equal(t, "waiting", got[0].State)
	require.Equal(t, stallLimit, got[0].StallAt.Sub(got[0].SilentSince),
		"#1478: the notice says when the unchanged silence limit will end the call")
	require.Equal(t, "responding", got[1].State, "data arrived again")
}

func TestIssue1478_FastCallPublishesNothing(t *testing.T) {
	issue1478ShortSilence(t)
	provider := &issue1478Provider{script: func(context.Context, int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: "quick"}, nil
	}}
	h, _, err := issue1478Call(t, provider)
	require.NoError(t, err)
	require.Empty(t, issue1478Statuses(h), "a prompt answer needs no notice")
}

func TestIssue1280_RecoveredAfterRetriesIsAnnounced(t *testing.T) {
	issue1478ShortSilence(t)
	provider := &issue1478Provider{script: func(_ context.Context, call int) (*providers.LLMResponse, error) {
		if call <= 2 {
			return nil, errors.New("read tcp 10.0.0.1:443: read: connection reset by peer")
		}
		return &providers.LLMResponse{Content: "back"}, nil
	}}
	h, _, err := issue1478Call(t, provider)
	require.NoError(t, err)
	got := issue1478Statuses(h)
	require.Len(t, got, 1, "got %+v", got)
	require.Equal(t, "recovered", got[0].State)
	require.Equal(t, 2, got[0].Retries, "#1280: 'reconnected after 2 retries'")
}
