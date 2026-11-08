package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

const r6GatewayPause = "No progress after 5 reminders — goal paused. Reply to continue."

type r6GatewayProvider struct {
	mu       sync.Mutex
	calls    int
	hold     bool
	openAll  bool
	requests chan chan struct{}
	gates    []chan struct{}
}

func (*r6GatewayProvider) GetDefaultModel() string { return "r6-public-pause-provider" }
func (p *r6GatewayProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	first := p.calls == 1
	if first {
		p.mu.Unlock()
		return &providers.LLMResponse{FinishReason: "tool_calls", ToolCalls: []providers.ToolCall{{ID: "r6-real-set-goal", Name: "set_goal", Arguments: map[string]any{
			"definition": "Continue producing independently reviewed work.",
			"criteria":   []any{map[string]any{"text": "The requested work and review are supplied.", "judgment": "boolean"}},
			"dod":        []any{map[string]any{"text": "Every claimed result cites evidence.", "judgment": "boolean", "provenance": "stated"}},
		}}}}, nil
	}
	if p.hold && !p.openAll {
		gate := make(chan struct{})
		p.gates = append(p.gates, gate)
		p.mu.Unlock()
		select {
		case p.requests <- gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	} else {
		p.mu.Unlock()
	}
	return &providers.LLMResponse{Content: "The real public work round returned without productive work."}, nil
}
func (p *r6GatewayProvider) releaseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.openAll = true
	for _, g := range p.gates {
		select {
		case <-g:
		default:
			close(g)
		}
	}
}

type r6PublicFixture struct {
	al         *agent.AgentLoop
	api        *restAPI
	engine     *agent.PlanEngine
	provider   *r6GatewayProvider
	root       string
	goalID     string
	frames     chan []byte
	other      chan []byte
	unbound    chan []byte
	mu         sync.Mutex
	dispatches int
}

func r6NewPublicFixture(t *testing.T) *r6PublicFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	cfg := &config.Config{Agents: config.AgentsConfig{Defaults: config.AgentDefaults{Home: filepath.Join(home, "agents"), DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096, MaxToolIterations: 10}, List: []config.AgentConfig{{ID: "mia", Home: filepath.Join(home, "agents", "mia")}}}}
	cfg.Sandbox.ToolPolicies = map[string]string{"set_goal": "allow"}
	provider := &r6GatewayProvider{requests: make(chan chan struct{}, 4)}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, provider)
	al.SetGoalRouteSessionStore()
	// Real metadata/input producer + normal Run receiver. ProcessDirect does
	// not bind a concrete ordinary root and is not an admission fixture.
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	owner, ws := "r6-public-owner", testHarnessWorkspaceMembershipID
	require.NoError(t, al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{Owner: &owner, WorkspaceID: &ws}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- al.Run(ctx) }()
	t.Cleanup(func() {
		provider.releaseAll()
		cancel()
		select {
		case runErr := <-done:
			require.True(t, runErr == nil || errors.Is(runErr, context.Canceled), "real runtime shutdown failed: %v", runErr)
		case <-time.After(10 * time.Second):
			t.Error("real gateway test runtime did not join")
		}
	})
	const input = "Register the goal for this work."
	require.NoError(t, al.GetSessionStore().AppendTranscriptStrict(meta.ID, session.TranscriptEntry{ID: "r6-public-human-input", Role: "user", AgentID: "mia", Content: input, Timestamp: time.Now().UTC()}))
	require.NoError(t, msgBus.PublishInbound(ctx, bus.InboundMessage{Channel: "webchat", ChatID: "r6-public-chat", SessionID: meta.ID, SessionKey: "agent:mia:session:" + meta.ID, GatewayUserID: owner, UserInitiated: true, Sender: bus.SenderInfo{CanonicalID: "human:" + owner}, Content: input}))
	require.Eventually(t, func() bool {
		rec, readErr := al.GetSessionLifecycleStore().Load(meta.ID)
		return readErr == nil && rec.State == session.LifecycleCompleted && rec.ExecutionID != nil
	}, 10*time.Second, 10*time.Millisecond, "SETUP: actual human ordinary receiver must finish its own durable root")
	root, err := al.GetSessionLifecycleStore().Load(meta.ID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleCompleted, root.State)
	require.NotNil(t, root.ExecutionID)
	require.NotEmpty(t, root.ExecutionID.RunID)
	require.NotZero(t, root.ExecutionID.BootSeq)
	goals, skipped, err := goal.NewStore(config.OmnipusHomeDir()).List()
	require.NoError(t, err)
	require.Empty(t, skipped, "SETUP: no unreadable goal may be silently skipped")
	var active *goal.Goal
	for i := range goals {
		if goals[i].ActiveSessionID == root.SessionID {
			active = &goals[i]
			break
		}
	}
	require.NotNil(t, active, "SETUP: the actual set_goal call must activate a real goal on the public root")
	require.NotEmpty(t, active.Criteria)
	ps := plan.New(filepath.Join(config.OmnipusHomeDir(), "plans"))
	al.SetPlanStore(ps)
	pe := agent.NewPlanEngine(al, ps, agent.GetTaskStore(al), agent.GetTaskExecutor(al))
	al.SetPlanEngine(pe)
	api := &restAPI{agentLoop: al, homePath: config.OmnipusHomeDir()}
	f := &r6PublicFixture{al: al, api: api, engine: pe, provider: provider, root: root.SessionID, goalID: active.GoalID}
	h := makeMinimalHandler()
	h.agentLoop = al
	h.msgBus = msgBus
	conn, frames := makeForwarderTestConn(128)
	bindTestConnToSession(h, "r6-public-chat", f.root, conn)
	f.frames = frames
	other, otherFrames := makeForwarderTestConn(128)
	bindTestConnToSession(h, "r6-other-chat", "r6-unrelated-session", other)
	f.other = otherFrames
	unbound, unboundFrames := makeForwarderTestConn(128)
	h.sessions["r6-unbound"] = unbound
	f.unbound = unboundFrames
	al.SetEventSyncTap(func(event agent.Event) {
		if event.Kind == agent.EventKindFollowUpQueued {
			if p, ok := event.Payload.(agent.FollowUpQueuedPayload); ok && p.SourceTool == "goal_idle_settle" {
				f.mu.Lock()
				f.dispatches++
				f.mu.Unlock()
			}
		}
		h.hubSyncTap(event)
	})
	t.Cleanup(func() { al.SetEventSyncTap(nil) })
	return f
}
func (f *r6PublicFixture) age(t *testing.T) {
	t.Helper()
	_, err := goal.NewStore(config.OmnipusHomeDir()).Update(f.goalID, func(g *goal.Goal) error { g.LastActivityAt = time.Now().UTC().Add(-time.Hour); return nil })
	require.NoError(t, err)
}
func (f *r6PublicFixture) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.dispatches }
func r6ReplayFrames(t *testing.T, ch chan []byte) []generated.ReplayMessageFrame {
	t.Helper()
	var frames []generated.ReplayMessageFrame
	for {
		select {
		case data := <-ch:
			var frame generated.ReplayMessageFrame
			require.NoError(t, json.Unmarshal(data, &frame))
			if frame.Type == "replay_message" {
				frames = append(frames, frame)
			}
		default:
			return frames
		}
	}
}
