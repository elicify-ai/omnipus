package gateway

// #1537 1e/1f and lifecycle mutant: real SteerLauncher -> turn -> retry
// producers -> real hub. Only the model transport and retry sleep are doubles.
import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/require"
)

type hmTransport struct {
	mu      sync.Mutex
	calls   []string
	mode    string
	waiting chan struct{}
}

func (*hmTransport) GetDefaultModel() string { return "hm-primary" }
func (p *hmTransport) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls = append(p.calls, model)
	call := len(p.calls)
	p.mu.Unlock()
	if p.mode == "fallback" && model == "hm-primary" {
		return nil, &common.ProviderError{Status: 429, Body: "rate limit exceeded", RetryAfterSeconds: 2}
	}
	if call == 1 {
		switch p.mode {
		case "no_response":
			return nil, context.DeadlineExceeded
		case "empty_reply":
			return nil, common.ErrEmptyStream
		case "silence":
			select {
			case <-p.waiting:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return nil, &common.ProviderError{Status: 429, Body: "rate limit exceeded", RetryAfterSeconds: 20}
		}
	}
	return &providers.LLMResponse{Content: "HM genuine helper answer", FinishReason: "stop"}, nil
}

func hmRealLoop(t *testing.T, p *hmTransport) (*agent.AgentLoop, *agent.SteerLauncher, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	cfg := &config.Config{Agents: config.AgentsConfig{Defaults: config.AgentDefaults{Home: filepath.Join(home, "agents"), DefaultModel: config.DefaultModel{Model: "hm-primary"}, MaxTokens: 4096, MaxToolIterations: 4}, List: []config.AgentConfig{{ID: "hm-parent", Home: filepath.Join(home, "agents", "hm-parent")}, {ID: hmAgent, Home: filepath.Join(home, "agents", hmAgent)}}}, Context: config.DefaultContextSettings()}
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	al := mustAgentLoop(t, cfg, mb, p)
	inst, ok := al.GetRegistry().GetAgent(hmAgent)
	require.True(t, ok)
	inst.Candidates = []providers.FallbackCandidate{{Provider: "hm", Model: "hm-primary"}}
	if p.mode == "fallback" {
		inst.Candidates = append(inst.Candidates, providers.FallbackCandidate{Provider: "hm", Model: "hm-fallback"})
	}
	inst.StoreProviderPool(map[string]providers.LLMProvider{"hm": p})
	al.SetProviderRetryWait(func(ctx context.Context, _ time.Duration) error { return ctx.Err() })
	classifier := agent.NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())
	al.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(classifier), steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())
	parent, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "hm-parent")
	require.NoError(t, err)
	wid := testHarnessWorkspaceMembershipID
	require.NoError(t, al.GetSessionStore().SetMeta(parent.ID, session.MetaPatch{WorkspaceID: &wid}))
	launcher := agent.NewSteerLauncher(al)
	child, err := launcher.Launch(context.Background(), steer.LaunchRequest{SteeringSessionID: parent.ID, TargetAgentID: hmAgent, Task: "Return HM genuine helper answer.", Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "hm-delegate"}})
	require.NoError(t, err)
	// Preserve generation in the launcher helper below by loading its real record.
	return al, launcher, parent.ID, child.SessionID
}

type hmRealResult struct {
	top, own, afterEnd, projection []map[string]any
	calls                          []string
	events                         []agent.Event
}

func hmDriveReal(t *testing.T, mode string) hmRealResult {
	t.Helper()
	p := &hmTransport{mode: mode, waiting: make(chan struct{})}
	al, l, root, child := hmRealLoop(t, p)
	h := makeMinimalHandler()
	h.agentLoop = al
	topC, ownC := newFakeHubConn(root), newFakeHubConn(child)
	_, ok := h.hubs.getOrCreate(root).bindLive(topC)
	require.True(t, ok)
	_, ok = h.hubs.getOrCreate(child).bindLive(ownC)
	require.True(t, ok)
	var mu sync.Mutex
	events := []agent.Event{}
	var preEnd, projected []map[string]any
	var release sync.Once
	al.SetEventSyncTap(func(e agent.Event) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
		if e.Kind == agent.EventKindTurnEnd {
			preEnd = hmFrames(t, topC)
		}
		h.hubSyncTap(e)
		if e.Kind == agent.EventKindProviderRetry {
			hub := h.hubs.getOrCreate(root)
			hub.mu.Lock()
			items := hub.proj.snapshot()
			hub.mu.Unlock()
			frames := projectionFrames(root, items, map[string]bool{})
			projected = make([]map[string]any, 0, len(frames))
			for _, raw := range frames {
				var frame map[string]any
				require.NoError(t, json.Unmarshal(raw, &frame))
				projected = append(projected, frame)
			}
		}
		if e.Kind == agent.EventKindProviderStatus {
			if s, yes := e.Payload.(agent.ProviderStatusPayload); yes && s.State == "waiting" {
				release.Do(func() { close(p.waiting) })
			}
		}
	})
	defer al.SetEventSyncTap(nil)
	rec, err := al.GetSessionLifecycleStore().Load(child)
	require.NoError(t, err)
	_, err = l.Dispatch(context.Background(), child, rec.Generation)
	require.NoError(t, err)
	require.Eventually(t, func() bool { r, e := al.GetSessionLifecycleStore().Load(child); return e == nil && r.Terminal() }, 45*time.Second, 5*time.Millisecond, "SETUP: real helper must terminate")
	mu.Lock()
	defer mu.Unlock()
	p.mu.Lock()
	calls := append([]string(nil), p.calls...)
	p.mu.Unlock()
	require.NotEmpty(t, preEnd, "lifecycle control: helper MUST emit TurnEnd, not merely mark its record terminal")
	return hmRealResult{top: preEnd, own: hmFrames(t, ownC), afterEnd: hmFrames(t, topC), projection: projected, calls: calls, events: events}
}

func TestHelperAnswerWithoutRecoveredSendsRespondingCopy(t *testing.T) {
	for _, mode := range []string{"fallback", "no_response", "empty_reply"} {
		t.Run(mode, func(t *testing.T) {
			run := hmDriveReal(t, mode)
			top, own, calls := run.top, run.own, run.calls
			want := []string{"hm-primary", "hm-primary"}
			if mode == "fallback" {
				want = []string{"hm-primary", "hm-primary", "hm-primary", "hm-fallback"}
			}
			require.Equal(t, want, calls, "positive control: exact real candidate/request trace")
			responding := 0
			for _, f := range top {
				require.NotEqual(t, "provider_fallback", f["type"], "FR-011: fallback note never copied to root")
				if f["type"] == "provider_status" && f["state"] == "responding" {
					responding++
					require.Equal(t, hmAgent, f["agent_id"])
					require.NotContains(t, f, "retries", "no invented recovered count")
				}
			}
			require.Equal(t, 1, responding, "FR-006: successful fallback/outer retry sends a named responding clear BEFORE helper TurnEnd")
			require.NotEmpty(t, own, "FR-001: helper receives own notices")
			ended := 0
			for _, f := range run.afterEnd {
				if f["type"] == "provider_status" && f["state"] == "ended" {
					ended++
				}
			}
			require.Equal(t, 1, ended, "real helper TurnEnd produces exactly one named clear")
		})
	}
}

func TestSilenceClearPublishedBeforeNewRetry(t *testing.T) {
	run := hmDriveReal(t, "silence")
	top, own, calls := run.top, run.own, run.calls
	require.Equal(t, []string{"hm-primary", "hm-primary"}, calls, "positive control: real silence, 429 and success")
	for name, frames := range map[string][]map[string]any{"top": top, "helper": own} {
		var relevant []string
		for _, f := range frames {
			if f["type"] == "provider_retry" {
				relevant = append(relevant, "retry")
			}
			if f["type"] == "provider_status" {
				if s, ok := f["state"].(string); ok {
					relevant = append(relevant, s)
				}
			}
		}
		require.GreaterOrEqual(t, len(relevant), 3, "FR-001/013: %s gets waiting, clear and retry", name)
		require.Equal(t, []string{"waiting", "responding", "retry"}, relevant[:3], "FR-013: %s countdown survives silence clear", name)
	}
	var notices []map[string]any
	for _, f := range run.projection {
		if f["type"] == "provider_retry" || f["type"] == "provider_status" {
			notices = append(notices, f)
		}
	}
	require.NotEmpty(t, notices, "FR-013: real retry publication left a replayable notice")
	last := notices[len(notices)-1]
	require.Equal(t, "provider_retry", last["type"], "FR-013: replay does not clear the newly scheduled countdown")
	require.Equal(t, float64(20), last["retry_after_seconds"])
}
