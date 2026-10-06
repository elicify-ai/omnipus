// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// concurrent_sessions_admission_test.go — founder ruling 2026-10-06: the
// ordinary-execution admission ("waiting room") is per chat SESSION, never per
// agent. Two chats with the same agent run in parallel; one chat's own turns
// still serialise. These tests drive the real AgentLoop through the bus
// (Run -> dispatchSessionWorker -> processMessage -> provider) with a provider
// whose calls block until the test releases them, so the assertions are about
// ordering, not wall-clock time.
package agent

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// gatedProvider blocks every Chat call whose last user message contains a
// registered marker until the test releases that marker. It records the order
// in which provider calls were entered.
type gatedProvider struct {
	mu      sync.Mutex
	entered map[string]chan struct{}
	release map[string]chan struct{}
}

func newGatedProvider(markers ...string) *gatedProvider {
	p := &gatedProvider{entered: map[string]chan struct{}{}, release: map[string]chan struct{}{}}
	for _, m := range markers {
		p.entered[m] = make(chan struct{}, 8)
		p.release[m] = make(chan struct{})
	}
	return p
}

func (p *gatedProvider) Chat(
	ctx context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	last := ""
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			last = msgs[i].Content
			break
		}
	}
	for marker, entered := range p.entered {
		if !strings.Contains(last, marker) {
			continue
		}
		entered <- struct{}{}
		select {
		case <-p.release[marker]:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &providers.LLMResponse{Content: "reply to " + marker}, nil
	}
	return &providers.LLMResponse{Content: "ungated"}, nil
}

func (p *gatedProvider) GetDefaultModel() string { return "gated-mock" }

// waitEntered reports whether marker's provider call was entered within d.
func (p *gatedProvider) waitEntered(marker string, d time.Duration) bool {
	select {
	case <-p.entered[marker]:
		return true
	case <-time.After(d):
		return false
	}
}

func (p *gatedProvider) releaseMarker(marker string) { close(p.release[marker]) }

// newAdmissionSessionsLoop builds a loop with one chat-target agent, starts
// Run, and returns the loop, the bus and two fresh chat sessions of that agent.
func newAdmissionSessionsLoop(t *testing.T, provider providers.LLMProvider) (*AgentLoop, *bus.MessageBus, string, string) {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: t.TempDir(), DefaultModel: config.DefaultModel{Model: "test-model"}},
			List:     []config.AgentConfig{{ID: "mia", Name: "Mia", Type: config.AgentTypeCore, Home: t.TempDir()}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(func() { al.Close() })
	// Production wiring: a lifecycle store and a minted boot epoch, so every
	// ordinary turn goes through the real admission (prepareOrdinaryExecution).
	al.SetSessionMessagingStores(nil, session.NewLifecycleStore(filepath.Join(t.TempDir(), "lifecycle")))
	mintGenuineBootEpochForLoop(t, al)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	a, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	b, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	runCtx, runCancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = al.Run(runCtx)
	}()
	t.Cleanup(func() { runCancel(); <-runDone })
	return al, msgBus, a.ID, b.ID
}

func publishChat(t *testing.T, msgBus *bus.MessageBus, sessionID, content string, meta map[string]string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), busWaitBudget)
	defer cancel()
	require.NoError(t, msgBus.PublishInbound(ctx, bus.InboundMessage{
		Channel:       "webchat",
		ChatID:        "chat-" + sessionID,
		SessionID:     sessionID,
		Content:       content,
		Sender:        bus.SenderInfo{CanonicalID: "webchat_user"},
		GatewayUserID: "daniel",
		UserInitiated: true,
		Metadata:      meta,
	}))
}

const admissionEnterBudget = 10 * time.Second

// Two chats of the same agent, default route (no agent_id — the shape the
// gateway's webchat path sends): B's provider call must be entered while A's is
// still blocked.
func TestOrdinaryAdmission_TwoSessionsSameAgent_DefaultRoute_RunInParallel(t *testing.T) {
	p := newGatedProvider("MARK-A", "MARK-B")
	_, msgBus, sidA, sidB := newAdmissionSessionsLoop(t, p)

	publishChat(t, msgBus, sidA, "MARK-A", nil)
	require.True(t, p.waitEntered("MARK-A", admissionEnterBudget), "session A's provider call was never entered")

	publishChat(t, msgBus, sidB, "MARK-B", nil)
	enteredB := p.waitEntered("MARK-B", admissionEnterBudget)
	// Unblock both so the loop can drain whatever the outcome.
	p.releaseMarker("MARK-A")
	p.releaseMarker("MARK-B")
	require.True(t, enteredB, "session B (same agent, different chat) was held behind session A's blocked turn")
}

// Same, with an explicit agent_id (session-qualified route key).
func TestOrdinaryAdmission_TwoSessionsSameAgent_ExplicitAgent_RunInParallel(t *testing.T) {
	p := newGatedProvider("MARK-A", "MARK-B")
	_, msgBus, sidA, sidB := newAdmissionSessionsLoop(t, p)
	meta := map[string]string{"agent_id": "mia"}

	publishChat(t, msgBus, sidA, "MARK-A", meta)
	require.True(t, p.waitEntered("MARK-A", admissionEnterBudget), "session A's provider call was never entered")

	publishChat(t, msgBus, sidB, "MARK-B", meta)
	enteredB := p.waitEntered("MARK-B", admissionEnterBudget)
	p.releaseMarker("MARK-A")
	p.releaseMarker("MARK-B")
	require.True(t, enteredB, "session B (same agent, explicit agent_id) was held behind session A's blocked turn")
}

// Regression guard: two messages to the SAME session still serialise — the
// second one's provider call is not entered while the first is blocked.
func TestOrdinaryAdmission_SameSession_StillSerialises(t *testing.T) {
	p := newGatedProvider("MARK-A1", "MARK-A2")
	_, msgBus, sidA, _ := newAdmissionSessionsLoop(t, p)

	publishChat(t, msgBus, sidA, "MARK-A1", nil)
	require.True(t, p.waitEntered("MARK-A1", admissionEnterBudget), "first turn's provider call was never entered")

	publishChat(t, msgBus, sidA, "MARK-A2", nil)
	// The second message must not start its own provider call while the first is blocked.
	enteredEarly := p.waitEntered("MARK-A2", 1500*time.Millisecond)
	p.releaseMarker("MARK-A1")
	p.releaseMarker("MARK-A2")
	require.False(t, enteredEarly, "a second message to the same session ran in parallel with the first turn")
}

// The admission itself is keyed by the chat session id, not by the routing
// SessionKey: two different sessions that carry the SAME agent-level routing
// key must both be admitted, while a second admission for the same session id
// is still refused while the first execution is pending.
func TestOrdinaryAdmission_KeyedBySessionID_NotRoutingKey(t *testing.T) {
	al, _, sidA, sidB := newAdmissionSessionsLoop(t, newGatedProvider())
	opts := processOptions{SessionKey: "agent:mia:main", TranscriptStore: al.GetSessionStore()} // agent-level key shared by both chats
	human := func(sid string) bus.InboundMessage {
		return bus.InboundMessage{Channel: "webchat", ChatID: "chat-" + sid, SessionID: sid,
			Sender: bus.SenderInfo{CanonicalID: "webchat_user"}, GatewayUserID: "daniel", UserInitiated: true}
	}
	ctx := context.Background()

	prepA, err := al.prepareOrdinaryExecution(ctx, human(sidA), opts)
	require.NoError(t, err)
	require.NotNil(t, prepA.execution)

	// A is still pending: a different session of the same agent is admitted at once.
	start := time.Now()
	prepB, err := al.prepareOrdinaryExecution(ctx, human(sidB), opts)
	require.NoError(t, err, "a different session must not be refused because another session of the agent is pending")
	require.NotNil(t, prepB.execution)
	require.Less(t, time.Since(start), previousExecutionSettleBudget/2, "a different session must not wait in the previous-execution waiting room")

	// The SAME session is still held back while its own execution is pending.
	waitCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	_, err = al.prepareOrdinaryExecution(waitCtx, human(sidA), opts)
	require.Error(t, err, "a second admission for the same session must be refused while its first execution is pending")
}

// Every turn-scoped key derived from the route (turn registry, history,
// steering queue, continuation target) must be per chat session when the
// message carries a session id — on the default route too. If two chats of the
// same agent shared "agent:<id>:main", a follow-up typed into one chat could be
// drained by the other chat's running turn.
func TestResolveMessageRoute_DefaultRoute_SessionKeyIsPerSession(t *testing.T) {
	al, _, sidA, sidB := newAdmissionSessionsLoop(t, newGatedProvider())
	msgFor := func(sid string) bus.InboundMessage {
		return bus.InboundMessage{Channel: "webchat", ChatID: "chat-" + sid, SessionID: sid,
			Sender: bus.SenderInfo{CanonicalID: "webchat_user"}}
	}
	routeA, _, err := al.resolveMessageRoute(msgFor(sidA))
	require.NoError(t, err)
	routeB, _, err := al.resolveMessageRoute(msgFor(sidB))
	require.NoError(t, err)
	require.NotEqual(t, routeA.SessionKey, routeB.SessionKey, "two chats of one agent resolved to the same routing session key")
	require.Contains(t, routeA.SessionKey, sidA)
	require.Contains(t, routeB.SessionKey, sidB)

	targetA, err := al.buildContinuationTarget(msgFor(sidA))
	require.NoError(t, err)
	targetB, err := al.buildContinuationTarget(msgFor(sidB))
	require.NoError(t, err)
	require.NotEqual(t, targetA.SessionKey, targetB.SessionKey, "steering-queue key is shared between two chats of one agent")
}
