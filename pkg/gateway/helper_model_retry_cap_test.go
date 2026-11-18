package gateway

// #1537 Q1 A: helper-model-notices-spec.md, fake-model dataset rows 3/3b
// and Correction 2026-11-15. Exactly120 waits; 300 skips, never clamps.
// Real delegated turn, candidate policy, retry/recovery producers and hubs.
// Only the external model transport and retry-sleep clock are doubles.
import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const hmCapAnswer = "HM genuine helper answer"

type hmCapRequest struct {
	Model  string `json:"model"`
	Status int    `json:"status"`
}

type hmCapTransport struct {
	mu         sync.Mutex
	retryAfter int
	requests   []hmCapRequest
	waits      []time.Duration
}

func (*hmCapTransport) GetDefaultModel() string { return "hm-primary" }

func (p *hmCapTransport) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) == 0 {
		p.requests = append(p.requests, hmCapRequest{Model: model, Status: 429})
		return nil, &common.ProviderError{Status: 429, Body: "rate limit exceeded", RetryAfterSeconds: p.retryAfter}
	}
	p.requests = append(p.requests, hmCapRequest{Model: model, Status: 200})
	return &providers.LLMResponse{Content: hmCapAnswer, FinishReason: "stop"}, nil
}

type hmCapRun struct {
	top, own    []map[string]any
	root, child string
	events      []agent.Event
	requests    []hmCapRequest
	waits       []time.Duration
}

func hmDriveCap(t *testing.T, seconds int) hmCapRun {
	t.Helper()
	// Reuse unchanged setup only. Replace its transport before dispatch: the
	// real helper has both candidates, so an incorrect120 skip is observable.
	al, launcher, root, child := hmRealLoop(t, &hmTransport{mode: "fallback"})
	p := &hmCapTransport{retryAfter: seconds}
	inst, ok := al.GetRegistry().GetAgent(hmAgent)
	require.True(t, ok, "SETUP: real helper exists")
	inst.StoreProviderPool(map[string]providers.LLMProvider{"hm": p})
	al.SetProviderRetryWait(func(ctx context.Context, delay time.Duration) error {
		p.mu.Lock()
		p.waits = append(p.waits, delay)
		p.mu.Unlock()
		return ctx.Err()
	})
	h := makeMinimalHandler()
	h.agentLoop = al
	top, own := newFakeHubConn(root), newFakeHubConn(child)
	_, ok = h.hubs.getOrCreate(root).bindLive(top)
	require.True(t, ok, "SETUP: top hub bound")
	_, ok = h.hubs.getOrCreate(child).bindLive(own)
	require.True(t, ok, "SETUP: helper hub bound")
	var mu sync.Mutex
	var events []agent.Event
	var ended sync.Once
	done := make(chan struct{})
	al.SetEventSyncTap(func(e agent.Event) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
		h.hubSyncTap(e)
		if e.Kind == agent.EventKindTurnEnd {
			ended.Do(func() { close(done) })
		}
	})
	defer al.SetEventSyncTap(nil)
	rec, err := al.GetSessionLifecycleStore().Load(child)
	require.NoError(t, err, "SETUP: real lifecycle record")
	_, err = launcher.Dispatch(context.Background(), child, rec.Generation)
	require.NoError(t, err, "SETUP: actual helper dispatch")
	select {
	case <-done:
	case <-time.After(45 * time.Second): // Fixture watchdog, not a latency oracle.
		t.Fatal("SETUP: real helper did not emit TurnEnd")
	}
	mu.Lock()
	defer mu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	trace, err := json.Marshal(p.requests)
	require.NoError(t, err)
	t.Logf("DATASET Retry-After=%d; fake-model request log=%s; scheduled retry sleeps=%v", seconds, trace, p.waits)
	return hmCapRun{top: hmFrames(t, top), own: hmFrames(t, own), root: root, child: child,
		events: events, requests: p.requests, waits: p.waits}
}

func hmCapProducerStates(t *testing.T, run hmCapRun) []string {
	t.Helper()
	var states []string
	ends := 0
	for _, e := range run.events {
		switch e.Kind {
		case agent.EventKindProviderRetry, agent.EventKindLLMRetry:
			p, ok := e.Payload.(agent.LLMRetryPayload)
			require.True(t, ok, "actual retry event has a typed payload")
			require.Equal(t, "rate_limit", p.Reason, "row3 retries a429, not an invented outer retry")
			require.Equal(t, 120*time.Second, p.RetryAt.Sub(p.SentAt), "row3 schedules exactly120 seconds")
			states = append(states, "retry")
		case agent.EventKindProviderStatus:
			p, ok := e.Payload.(agent.ProviderStatusPayload)
			require.True(t, ok, "actual provider-status event has a typed payload")
			if p.State == "recovered" {
				require.Equal(t, 1, p.Retries, "row3 recovers after exactly one retry")
				states = append(states, "recovered")
			}
		case agent.EventKindTurnEnd:
			ends++
			p, ok := e.Payload.(agent.TurnEndPayload)
			require.True(t, ok, "real helper TurnEnd payload, not merely terminal record")
			require.Equal(t, agent.TurnEndStatusCompleted, p.Status, "fake200 completes the real helper")
			require.Equal(t, len(hmCapAnswer), p.FinalContentLen, "ordinary helper answer reaches turn completion")
		}
	}
	require.Equal(t, 1, ends, "positive control: exactly one real completed helper turn")
	return states
}

func hmCapAssertCopies(t *testing.T, frames []map[string]any, sessionID, child string) {
	t.Helper()
	var notices []map[string]any
	for _, f := range frames {
		if f["type"] == "provider_retry" || (f["type"] == "provider_status" && f["state"] == "recovered") {
			notices = append(notices, f)
		}
	}
	if !assert.Len(t, notices, 2, "dataset3: %s gets one120s retry then recovered, with no missing own/named copy", sessionID) {
		return
	}
	require.Equal(t, "provider_retry", notices[0]["type"], "dataset3 retry precedes recovery")
	require.Equal(t, float64(120), notices[0]["retry_after_seconds"], "dataset3: countdown input is120, not300")
	sentValue, ok := notices[0]["sent_at"].(string)
	require.True(t, ok, "retry sent_at is a wire timestamp")
	sent, err := time.Parse(time.RFC3339Nano, sentValue)
	require.NoError(t, err)
	atValue, ok := notices[0]["retry_at"].(string)
	require.True(t, ok, "retry_at is a wire timestamp")
	at, err := time.Parse(time.RFC3339Nano, atValue)
	require.NoError(t, err)
	require.Equal(t, 120*time.Second, at.Sub(sent), "dataset3: wire deadline preserves120s")
	require.Equal(t, "provider_status", notices[1]["type"])
	require.Equal(t, "recovered", notices[1]["state"])
	require.Equal(t, float64(1), notices[1]["retries"], "dataset3: recovered after one retry")
	for _, f := range notices {
		require.Equal(t, sessionID, f["session_id"], "correctly addressed copy")
		if child == "" {
			require.NotContains(t, f, "child_session_id", "own copy has no named identity")
			require.NotContains(t, f, "agent_id", "own copy has no named identity")
		} else {
			require.Equal(t, child, f["child_session_id"], "top copy names the actual helper chat")
			require.Equal(t, hmAgent, f["agent_id"], "top copy names the actual helper agent")
		}
	}
}

func TestHelperRetryAfter120SchedulesAtCapAndRecoversInBothChats(t *testing.T) {
	run := hmDriveCap(t, 120) // Corrected dataset3: max honoured wait, not300 clamped.
	require.Equal(t, []hmCapRequest{{Model: "hm-primary", Status: 429}, {Model: "hm-primary", Status: 200}}, run.requests, "dataset3: retry stays on the same model")
	require.Equal(t, []time.Duration{120 * time.Second}, run.waits, "dataset3: exactly one120s wait scheduled")
	require.Equal(t, []string{"retry", "recovered"}, hmCapProducerStates(t, run), "dataset3: real producer announces retry then recovery")
	// Nonfatal length assertion inside the helper lets BOTH destinations be
	// checked even when the preimplementation own copy is absent.
	hmCapAssertCopies(t, run.own, run.child, "")
	hmCapAssertCopies(t, run.top, run.root, run.child)
}

func TestHelperRetryAfter300SkipsToFallbackWithoutRetryInEitherChat(t *testing.T) {
	run := hmDriveCap(t, 300) // Corrected dataset3b: above-cap skip, not a clamp.
	require.Equal(t, []hmCapRequest{{Model: "hm-primary", Status: 429}, {Model: "hm-fallback", Status: 200}}, run.requests, "dataset3b: exact fake-model log1+1, no primary retry")
	require.Len(t, run.waits, 0, "dataset3b: fallback answers without a scheduled sleep")
	require.Len(t, hmCapProducerStates(t, run), 0, "dataset3b: no retry or invented recovered producer announcement")
	for name, frames := range map[string][]map[string]any{"helper": run.own, "top": run.top} {
		retries := 0
		for _, f := range frames {
			if f["type"] == "provider_retry" {
				retries++
			}
		}
		require.Equal(t, 0, retries, "dataset3b: %s has neither an own retry line nor a named retry-row input", name)
	}
}
