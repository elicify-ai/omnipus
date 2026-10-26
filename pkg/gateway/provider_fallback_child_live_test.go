package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

const (
	childFallbackPrimary = "child-primary"
	childFallbackModel   = "child-fallback"
	childFallbackAnswer  = "The helper answered using its fallback."
)

// Only the paid provider transport is scripted. In particular, no synthetic
// child fallback event, turnState, runTurn or delegation implementation is used.
type childFallbackTransport struct {
	mu    sync.Mutex
	calls []string
}

func (p *childFallbackTransport) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls = append(p.calls, model)
	p.mu.Unlock()
	switch model {
	case childFallbackPrimary:
		// Above the retry ceiling: the real chain switches immediately.
		return nil, &common.ProviderError{Status: 429, Body: "rate limit exceeded", RetryAfterSeconds: 3600}
	case childFallbackModel:
		return &providers.LLMResponse{Content: childFallbackAnswer, FinishReason: "stop"}, nil
	default:
		return nil, fmt.Errorf("unexpected provider call for model %q", model)
	}
}

func (*childFallbackTransport) GetDefaultModel() string { return childFallbackPrimary }

func newChildFallbackLoop(t *testing.T) (*agent.AgentLoop, *childFallbackTransport) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	transport := &childFallbackTransport{}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: filepath.Join(home, "agents"), DefaultModel: config.DefaultModel{Model: childFallbackPrimary},
				MaxTokens: 4096, MaxToolIterations: 4,
			},
			List: []config.AgentConfig{
				{ID: "fallback-parent", Home: filepath.Join(home, "agents", "fallback-parent")},
				{ID: "fallback-helper", Home: filepath.Join(home, "agents", "fallback-helper")},
			},
		},
		Context: config.DefaultContextSettings(),
	}
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	al := mustAgentLoop(t, cfg, mb, transport)
	helper, ok := al.GetRegistry().GetAgent("fallback-helper")
	require.True(t, ok)
	helper.Candidates = []providers.FallbackCandidate{
		{Provider: "child-provider", Model: childFallbackPrimary},
		{Provider: "child-provider", Model: childFallbackModel},
	}
	helper.StoreProviderPool(map[string]providers.LLMProvider{"child-provider": transport})
	classifier := agent.NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())
	al.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(classifier), steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())
	return al, transport
}

// F1 and ADR-091 D7: child frames address C, never cancellation root P. Test the
// real launch -> reconstruction -> turn -> fallback -> gateway hub path with
// distinct sessions, including the parent's pre-existing pending retry state.
func TestProviderFallbackLive_DelegatedChildKeepsOwnSession(t *testing.T) {
	al, transport := newChildFallbackLoop(t)
	store := al.GetSessionStore()
	parent, err := store.NewSession(session.SessionTypeChat, "webchat", "fallback-parent")
	require.NoError(t, err)
	workspaceID := testHarnessWorkspaceMembershipID
	require.NoError(t, store.SetMeta(parent.ID, session.MetaPatch{WorkspaceID: &workspaceID}))
	launcher := agent.NewSteerLauncher(al)
	child, err := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parent.ID, TargetAgentID: "fallback-helper", Task: "Answer the delegated question.",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "fallback-child-call"},
	})
	require.NoError(t, err, "a real delegated child must launch")
	require.NotEqual(t, parent.ID, child.SessionID, "the fixture must exercise distinct identities")
	record, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	require.NoError(t, err)
	require.NotNil(t, record.SteeredBy)
	require.Equal(t, parent.ID, record.SteeredBy.RootSessionID, "cancellation still belongs to the cascade root")

	h := makeMinimalHandler()
	h.agentLoop = al
	parentHub, childHub := h.hubs.getOrCreate(parent.ID), h.hubs.getOrCreate(child.SessionID)
	parentConn, childConn := newFakeHubConn("parent"), newFakeHubConn("child")
	_, bound := parentHub.bindLive(parentConn)
	require.True(t, bound)
	_, bound = childHub.bindLive(childConn)
	require.True(t, bound)
	// This is parent fixture state, not the child event being tested. The real
	// gateway producer creates both the pending retry frame and its projection.
	sent := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	h.hubSyncTap(agent.Event{
		Kind: agent.EventKindProviderRetry, Meta: agent.EventMeta{TurnID: "parent-pending-turn"},
		Payload: agent.LLMRetryPayload{
			SessionID: parent.ID, Provider: "parent-provider", Model: "parent-model", Reason: "rate_limit",
			Attempt: 2, MaxAttempts: 3, SentAt: sent, RetryAt: sent.Add(time.Minute),
		},
	})
	parentHub.mu.Lock()
	parentBefore := parentHub.proj.snapshot()
	parentHub.mu.Unlock()
	require.Len(t, parentBefore, 1, "positive control: the parent's retry state is populated")
	require.Equal(t, "item:provider_retry:parent-pending-turn", parentBefore[0].key)
	require.Len(t, parentConn.snapshot(), 1, "positive control: parent connection receives real hub frames")

	var mu sync.Mutex
	var fallbacks []agent.Event
	var parentAtFallback []projSnapshotItem
	var childTurnID, rootScopedTurnID string
	al.SetEventSyncTap(func(evt agent.Event) {
		h.hubSyncTap(evt) // the actual gateway publisher, not a routing mock
		if evt.Kind != agent.EventKindProviderFallback {
			return
		}
		parentHub.mu.Lock()
		projection := parentHub.proj.snapshot()
		parentHub.mu.Unlock()
		mu.Lock()
		defer mu.Unlock()
		fallbacks = append(fallbacks, evt)
		parentAtFallback = projection // before TurnEnd can forget a misrouted item
		if turn := al.GetActiveTurnBySession(child.SessionID); turn != nil {
			childTurnID = turn.TurnID
		}
		if hook := al.GetActiveTurnHookForSession(parent.ID); hook != nil {
			rootScopedTurnID = hook.TurnID()
		}
	})
	result, err := launcher.Dispatch(context.Background(), child.SessionID, child.Generation)
	require.NoError(t, err)
	require.Equal(t, steer.DispatchRunning, result.State)
	require.Eventually(t, func() bool {
		rec, loadErr := al.GetSessionLifecycleStore().Load(child.SessionID)
		return loadErr == nil && rec.Terminal()
	}, 5*time.Second, 5*time.Millisecond, "the real child must finish; no synthetic fallback event is injected")

	record, err = al.GetSessionLifecycleStore().Load(child.SessionID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleCompleted, record.State, "the helper must answer successfully, not merely end with an error")
	entries, err := store.ReadTranscript(child.SessionID)
	require.NoError(t, err)
	answerCount := 0
	for _, entry := range entries {
		if entry.Role == "assistant" && entry.Content == childFallbackAnswer {
			answerCount++
		}
	}
	require.Equal(t, 1, answerCount, "the real turn saves exactly one fallback answer")
	transport.mu.Lock()
	calls := append([]string(nil), transport.calls...)
	transport.mu.Unlock()
	require.Equal(t, []string{childFallbackPrimary, childFallbackModel}, calls, "only the child makes provider calls and falls back")
	childNotes := childFallbackNotes(t, store, child.SessionID)
	require.Len(t, childNotes, 1, "the genuine fallback note must be saved in C")
	require.Equal(t, "Answered by the Fallback model (child-fallback) because child-primary was unavailable.", childNotes[0].Content)
	require.Empty(t, childFallbackNotes(t, store, parent.ID), "P must not acquire C's saved fallback note")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, fallbacks, 1)
	payload, ok := fallbacks[0].Payload.(agent.ProviderFallbackPayload)
	require.True(t, ok)
	// Keep independent assertions non-fatal so RED records both the wrong
	// producer identity and its actual effect on both bound session streams.
	t.Run("producing_child_identity", func(t *testing.T) {
		require.Equal(t, child.SessionID, payload.SessionID, "F1: the live fallback event must carry C, not cancellation root P")
	})
	t.Run("child_connection", func(t *testing.T) {
		frames := childLiveFallbackFrames(t, childConn)
		require.Len(t, frames, 1, "C must receive exactly one actual live provider_fallback frame")
		require.Equal(t, child.SessionID, frames[0].SessionId)
		require.Equal(t, childTurnID, frames[0].TurnId)
		require.Equal(t, childFallbackModel, frames[0].AnsweredModel)
		require.Equal(t, childFallbackPrimary, frames[0].UnavailableModel)
		require.Equal(t, "rate_limited", frames[0].UnavailableCode)
	})
	t.Run("parent_connection", func(t *testing.T) {
		require.Empty(t, childLiveFallbackFrames(t, parentConn), "P must receive no child fallback frame that could clear its retry indicator")
	})
	t.Run("parent_retry_state", func(t *testing.T) {
		require.Equal(t, parentBefore, parentAtFallback, "P's pending retry projection must be unchanged when C falls back")
	})
	require.NotEmpty(t, childTurnID)
	require.Equal(t, childTurnID, rootScopedTurnID, "do not fix display routing by changing cancellation's cascade-root identity")
	require.Equal(t, childTurnID, payload.TurnID)
	require.Equal(t, childTurnID, childNotes[0].TurnID)
}

func childFallbackNotes(t *testing.T, store *session.UnifiedStore, sessionID string) []session.TranscriptEntry {
	t.Helper()
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	var notes []session.TranscriptEntry
	for _, entry := range entries {
		if entry.Type == session.EntryTypeProviderFallback {
			notes = append(notes, entry)
		}
	}
	return notes
}

func childLiveFallbackFrames(t *testing.T, conn *fakeHubConn) []generated.ProviderFallbackFrame {
	t.Helper()
	var frames []generated.ProviderFallbackFrame
	for _, raw := range conn.snapshot() {
		var frame generated.ProviderFallbackFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		if frame.Type == "provider_fallback" {
			frames = append(frames, frame)
		}
	}
	return frames
}
