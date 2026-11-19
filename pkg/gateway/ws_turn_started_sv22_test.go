// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// SV-22 (#1428 step 2, UAT-15 option A): the server names a turn when it
// starts. A tab bound to the chat receives a live-only turn_started frame
// carrying the new turn's id as soon as the turn starts — before any token,
// and after the previous turn's done — so a command typed while the new turn
// is still silent can name it. Real socket, real gateway handler, real agent
// loop with the production steering wiring; only the model is scripted.
//
// The model's silent stretch is an open-ended block (the turn emits no token
// until the test ends) instead of a fake-clock run of keepalive pings: the
// frame must arrive without any token, whatever the ping clock does.

package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Markers the scripted model reacts to; the newest message carrying one wins.
const (
	sv22Hold   = "sv22-hold"   // one token, then stream until cancelled
	sv22Quiet  = "sv22-quiet"  // no token at all until cancelled
	sv22Answer = "sv22-answer" // one token, then finish at once
)

type sv22Model struct {
	mu      sync.Mutex
	markers []string
	release chan struct{}
}

func (m *sv22Model) GetDefaultModel() string { return "redirect-streaming" }

func (m *sv22Model) Chat(ctx context.Context, msgs []providers.Message, tools []providers.ToolDefinition,
	model string, opts map[string]any,
) (*providers.LLMResponse, error) {
	return m.ChatStream(ctx, msgs, tools, model, opts, func(string) {}, nil)
}

func (m *sv22Model) ChatStream(ctx context.Context, msgs []providers.Message, _ []providers.ToolDefinition,
	_ string, _ map[string]any, onChunk func(string), _ providers.OnToolCallProgress,
) (*providers.LLMResponse, error) {
	marker := ""
	for i := len(msgs) - 1; i >= 0 && marker == ""; i-- {
		for _, candidate := range []string{sv22Hold, sv22Quiet, sv22Answer} {
			if strings.Contains(msgs[i].Content, candidate) {
				marker = candidate
			}
		}
	}
	m.mu.Lock()
	m.markers = append(m.markers, marker)
	m.mu.Unlock()
	switch marker {
	case sv22Hold, sv22Quiet:
		if marker == sv22Hold {
			onChunk("old turn text ")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-m.release:
			return &providers.LLMResponse{Content: "released"}, nil
		}
	case sv22Answer:
		onChunk("first answer")
		return &providers.LLMResponse{Content: "first answer"}, nil
	}
	return &providers.LLMResponse{Content: "ok"}, nil
}

func (m *sv22Model) reached(marker string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, got := range m.markers {
		if got == marker {
			return true
		}
	}
	return false
}

// sv22Tab records every frame one socket receives, in arrival order.
type sv22Tab struct {
	mu     sync.Mutex
	frames [][]byte
}

func sv22Record(t *testing.T, conn *websocket.Conn) *sv22Tab {
	t.Helper()
	tab := &sv22Tab{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			tab.mu.Lock()
			tab.frames = append(tab.frames, raw)
			tab.mu.Unlock()
		}
	}()
	t.Cleanup(func() { _ = conn.Close(); <-done })
	return tab
}

func (tab *sv22Tab) all() [][]byte {
	tab.mu.Lock()
	defer tab.mu.Unlock()
	return append([][]byte(nil), tab.frames...)
}

func sv22Decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

// waitFor returns the index of the first recorded frame matching pred.
func (tab *sv22Tab) waitFor(t *testing.T, timeout time.Duration, what string, pred func(map[string]any) bool) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		for i, raw := range tab.all() {
			if pred(sv22Decode(t, raw)) {
				return i
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: not received within %s", what, timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func sv22Type(want string) func(map[string]any) bool {
	return func(m map[string]any) bool { return m["type"] == want }
}

type sv22Rig struct {
	al      *agent.AgentLoop
	h       *WSHandler
	model   *sv22Model
	conn    *websocket.Conn
	bound   *sv22Tab
	unbound *sv22Tab
}

func newSV22Rig(t *testing.T) *sv22Rig {
	t.Helper()
	model := &sv22Model{release: make(chan struct{})}
	srv, al, token, h := newRedirectStreamServerWithHandler(t, model)
	t.Cleanup(func() { close(model.release) })
	open := func() (*websocket.Conn, *sv22Tab) {
		conn := dialTestWS(t, srv)
		tab := sv22Record(t, conn)
		sv22Write(t, conn, wsClientFrameTestHelper{Type: "auth", Token: token})
		return conn, tab
	}
	r := &sv22Rig{al: al, h: h, model: model}
	r.conn, r.bound = open()
	_, r.unbound = open()
	return r
}

func sv22Write(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, raw))
}

// start sends the chat's first message and returns the minted session id.
func (r *sv22Rig) start(t *testing.T, content string) string {
	t.Helper()
	sv22Write(t, r.conn, wsClientFrameTestHelper{Type: "message", Content: content, AgentID: "mia"})
	i := r.bound.waitFor(t, 10*time.Second, "SETUP session_started", sv22Type("session_started"))
	sid, _ := sv22Decode(t, r.bound.all()[i])["session_id"].(string)
	require.NotEmpty(t, sid)
	return sid
}

func (r *sv22Rig) waitModel(t *testing.T, marker string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !r.model.reached(marker) {
		require.True(t, time.Now().Before(deadline), "SETUP: the model never received a %s request", marker)
		time.Sleep(10 * time.Millisecond)
	}
}

// doneOf waits for the done frame of turnID ("" = the first done) and
// returns its index and turn id.
func (r *sv22Rig) doneOf(t *testing.T, turnID string) (int, string) {
	t.Helper()
	i := r.bound.waitFor(t, 15*time.Second, "the old turn's done", func(m map[string]any) bool {
		return m["type"] == "done" && (turnID == "" || m["turn_id"] == turnID)
	})
	id, _ := sv22Decode(t, r.bound.all()[i])["turn_id"].(string)
	require.NotEmpty(t, id, "the old turn's done names its turn")
	return i, id
}

// assertNamed is the SV-22 contract for the turn that starts after oldTurnID.
func (r *sv22Rig) assertNamed(t *testing.T, sid, oldTurnID string, doneIdx int) {
	t.Helper()
	// The old turn named itself when it started too; R1 is about the frame
	// naming the NEXT turn.
	tsIdx := r.bound.waitFor(t, 10*time.Second, "turn_started for the new turn", func(m map[string]any) bool {
		active, _ := m["active_turn"].(map[string]any)
		return m["type"] == "turn_started" && active != nil && active["turn_id"] != oldTurnID
	})
	frames := r.bound.all()
	require.Less(t, doneIdx, tsIdx, "R1: the old turn's done must arrive before the next turn_started")
	for i := doneIdx + 1; i < tsIdx; i++ {
		require.NotEqual(t, "token", sv22Decode(t, frames[i])["type"], "no token of the new turn before turn_started")
	}
	frame := sv22Decode(t, frames[tsIdx])
	require.Equal(t, sid, frame["session_id"])
	_, hasSeq := frame["seq"]
	require.False(t, hasSeq, "turn_started is live-only: it carries no seq")
	active, ok := frame["active_turn"].(map[string]any)
	require.True(t, ok, "turn_started carries active_turn")
	turnID, agentID, startedAt, running := r.al.ActiveForegroundTurnInfo(sid)
	require.True(t, running, "the named turn is still running (the model is silent)")
	require.Equal(t, turnID, active["turn_id"], "the frame names the same turn a snapshot would")
	require.NotEqual(t, oldTurnID, active["turn_id"], "the frame names the NEW turn")
	require.True(t, strings.HasPrefix(turnID, "mia-turn-"))
	require.Equal(t, "mia", agentID)
	require.Equal(t, agentID, active["agent_id"])
	require.Equal(t, startedAt.UTC().Format(time.RFC3339), active["started_at"])

	// A quiet stretch: the turn still sends no token and no frame is repeated.
	time.Sleep(time.Second)
	perTurn := map[any]int{}
	for i, raw := range r.bound.all() {
		m := sv22Decode(t, raw)
		if m["type"] == "turn_started" {
			active, _ := m["active_turn"].(map[string]any)
			require.NotNil(t, active, "every turn_started carries active_turn")
			perTurn[active["turn_id"]]++
		}
		if i > tsIdx {
			require.NotEqual(t, "token", m["type"], "the silent turn sent no token")
		}
	}
	require.Equal(t, 1, perTurn[turnID], "exactly one turn_started for the new turn")
	for id, n := range perTurn {
		require.Equal(t, 1, n, "exactly one turn_started per turn (turn %v)", id)
	}
	for _, raw := range r.unbound.all() {
		require.NotEqual(t, "turn_started", sv22Decode(t, raw)["type"], "a tab bound to no chat gets nothing")
	}
}

// (V) A delivered /stop-redirect stops the streaming turn and starts a new
// turn that stays silent; the bound tab learns the new turn's id at once.
func TestSV22_TurnStarted_RedirectNamesTheNewTurn(t *testing.T) {
	r := newSV22Rig(t)
	sid := r.start(t, sv22Hold+": list plants")
	r.waitModel(t, sv22Hold)
	r.bound.waitFor(t, 10*time.Second, "SETUP the old turn's first token", sv22Type("token"))
	oldTurnID, _, _, ok := r.al.ActiveForegroundTurnInfo(sid)
	require.True(t, ok, "SETUP: the old turn is running")

	sv22Write(t, r.conn, generated.RedirectFrame{Type: "redirect", SessionId: sid, Instruction: sv22Quiet + ": say mango"})
	r.waitModel(t, sv22Quiet)
	doneIdx, _ := r.doneOf(t, oldTurnID)
	r.assertNamed(t, sid, oldTurnID, doneIdx)
}

// (T) An ordinary message after a finished turn starts a silent turn; the
// bound tab learns its id before any token.
func TestSV22_TurnStarted_OrdinaryMessageNamesTheNewTurn(t *testing.T) {
	r := newSV22Rig(t)
	sid := r.start(t, sv22Answer+": hello")
	doneIdx, oldTurnID := r.doneOf(t, "")

	sv22Write(t, r.conn, wsClientFrameTestHelper{Type: "message", SessionID: sid, Content: sv22Quiet + ": second", AgentID: "mia"})
	r.waitModel(t, sv22Quiet)
	r.assertNamed(t, sid, oldTurnID, doneIdx)
}

// R1 ordering for a queued follow-up: a message that reaches the steering
// queue after the turn's last poll is run by the session worker's post-turn
// drain as a new turn. The follow-up is enqueued from the real TurnEnd event
// (after the final poll, before the drain), the production path for a
// message typed just as a reply finishes.
func TestSV22_TurnStarted_QueuedFollowUpAfterOldDone(t *testing.T) {
	r := newSV22Rig(t)
	var once sync.Once
	enqueueErr := make(chan error, 1)
	r.al.SetEventSyncTap(func(evt agent.Event) {
		r.h.hubSyncTap(evt)
		if evt.Kind == agent.EventKindTurnEnd && evt.Meta.SessionKey != "" {
			once.Do(func() {
				_, err := r.al.EnqueueSteeringMessage(evt.Meta.SessionKey, "mia",
					providers.Message{Role: "user", Content: sv22Quiet + ": queued follow-up"}, "")
				enqueueErr <- err
			})
		}
	})
	t.Cleanup(func() { r.al.SetEventSyncTap(r.h.hubSyncTap) })

	sid := r.start(t, sv22Answer+": hello")
	select {
	case err := <-enqueueErr:
		require.NoError(t, err, "SETUP: the follow-up is queued for the session")
	case <-time.After(15 * time.Second):
		t.Fatal("SETUP: the first turn never ended")
	}
	r.waitModel(t, sv22Quiet)
	doneIdx, oldTurnID := r.doneOf(t, "")
	r.assertNamed(t, sid, oldTurnID, doneIdx)
}
