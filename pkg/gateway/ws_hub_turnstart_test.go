// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestHub_BackgroundRootTurnStart_ResetsLatchBySessionID pins #823 review
// item 8: a background root turn (keeper, goal loop, scheduled run) runs on
// a chat id no browser connection is bound to, and its event SessionKey is
// "agent:<id>:session:<sid>", not the session id — so the hub could only
// find the session through the payload's own routing session id. Without
// it the root-turn-ended latch from the previous turn stayed set, and a
// delegate spawned by the NEW, live turn was armed at registration and got
// a false "interrupted" end once the watchdog expired.
func TestHub_BackgroundRootTurnStart_ResetsLatchBySessionID(t *testing.T) {
	restore := SetOrphanWatchdogTimeoutForTest(50 * time.Millisecond)
	defer restore()

	const sid = "session_background"
	bus := agent.NewEventBus()
	defer bus.Close()
	h := makeMinimalHandler()
	bus.SetSyncTap(h.hubSyncTap)
	// A tab watches the session; the background turn's own chat id is bound
	// to nothing.
	wc, ch := makeForwarderTestConn(64)
	bindTestConnToSession(h, "chat-tab", sid, wc)

	bus.Emit(agent.Event{Kind: agent.EventKindTurnEnd, Payload: agent.TurnEndPayload{
		Status: agent.TurnEndStatusCompleted, ChatID: "keeper-chat", SessionID: sid, IsRoot: true,
	}})
	bus.Emit(agent.Event{
		Kind: agent.EventKindTurnStart,
		Meta: agent.EventMeta{SessionKey: "agent:mia:session:" + sid},
		Payload: agent.TurnStartPayload{
			Channel: "system", ChatID: "keeper-chat", IsRoot: true, SessionID: sid,
		},
	})
	bus.Emit(agent.Event{Kind: agent.EventKindSubTurnSpawn, Payload: agent.SubTurnSpawnPayload{
		SpanID: "span_bg", ParentSpawnCallID: "c-bg", ChatID: "keeper-chat", SessionID: sid,
	}})
	time.Sleep(400 * time.Millisecond) // 8x the watchdog period

	frames := drainAllFrames(ch)
	require.Len(t, frames, 1, "only subagent_start — an interrupted end means the new turn's latch was never reset")
	assert.Equal(t, "subagent_start", decodeWire(t, frames[0]).Type)
}

// TestTurnStart_CarriesTheRoutingSessionID pins the agent side of item 8
// through a real agent loop: a root turn's TurnStart names the same routing
// session id as its TurnEnd.
func TestTurnStart_CarriesTheRoutingSessionID(t *testing.T) {
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{Home: t.TempDir(), DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		List:     []config.AgentConfig{{ID: "mia"}},
	}}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	var mu sync.Mutex
	var startSID, endSID string
	var sawStart, sawEnd bool
	al.SetEventSyncTap(func(evt agent.Event) {
		mu.Lock()
		defer mu.Unlock()
		switch p := evt.Payload.(type) {
		case agent.TurnStartPayload:
			if p.IsRoot && !sawStart {
				sawStart, startSID = true, p.SessionID
			}
		case agent.TurnEndPayload:
			if p.IsRoot && !sawEnd {
				sawEnd, endSID = true, p.SessionID
			}
		}
	})
	t.Cleanup(func() { al.SetEventSyncTap(nil) })
	_, err := al.ProcessDirectWithChannel(context.Background(), "hello", "sess-key", "telegram", "chat-x")
	require.NoError(t, err)
	mu.Lock()
	defer mu.Unlock()
	require.True(t, sawStart && sawEnd)
	require.NotEmpty(t, endSID)
	assert.Equal(t, endSID, startSID, "a root turn's start and end name the same routing session")
}
