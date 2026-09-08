// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ws_session_state_test.go — ADR-082 D4/FR-008/S-07: session_state.active_turn
// announces the in-flight foreground turn (if any) for the session a
// connection is bound to.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestSessionStateFrame_ActiveTurn proves S-07/T-07: session_state.active_turn
// is present (turn_id, agent_id, started_at all populated) while a real
// foreground turn is in flight for the bound session, and absent for an idle
// session. Drives a REAL turn via stubbornProvider (cancel_abandoned_test.go,
// same package) — a provider whose Chat() blocks until released — so
// AgentLoop.ActiveForegroundTurnInfo (the accessor emitSessionState reads)
// is exercised end to end, not faked.
func TestSessionStateFrame_ActiveTurn(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	workspaceDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	sp := newStubbornProvider(30 * time.Second)

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 18804, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         workspaceDir,
				DefaultModel: config.DefaultModel{Model: "stubborn-provider"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{Mode: config.SandboxModeOff},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, sp)

	ctx, cancelCtx := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		if err := al.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Logf("agent loop Run: %v", err)
		}
	}()
	t.Cleanup(func() {
		sp.Shutdown()
		cancelCtx()
		select {
		case <-runDone:
		case <-time.After(30 * time.Second):
			t.Logf("agent loop Run did not exit within 30s")
		}
	})
	time.Sleep(20 * time.Millisecond)

	handler := newWSHandler(msgBus, al, "")
	msgBus.SetStreamDelegate(handler)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)

	// Idle case FIRST, before any turn starts: a brand-new attach to a fresh
	// session must show no active_turn.
	idleMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	idleWC := &wsConn{sendCh: make(chan []byte, 8), doneCh: make(chan struct{})}
	handler.emitSessionState(idleWC, idleMeta.ID)
	idleFrame := readSessionStateFrame(t, idleWC.sendCh)
	assert.Nil(t, idleFrame.ActiveTurn, "active_turn must be absent for an idle session")
	// ADR-082 review CR3: session_state must carry session_id once a real
	// session is known — the connection-open emit (no session yet) leaves it
	// nil, but every emit that DOES pass a sessionID must stamp it.
	require.NotNil(t, idleFrame.SessionID, "session_state must carry session_id once a session is known")
	assert.Equal(t, idleMeta.ID, *idleFrame.SessionID)

	// Start a real, blocking turn.
	msgFrame := wsClientFrameTestHelper{Type: "message", Content: "start stubborn turn for session_state test"}
	data, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	started := readFrameOfType(t, conn, "session_started", 5*time.Second)
	sessionID := started.SessionID
	require.NotEmpty(t, sessionID)

	// Wait until the stubborn provider is inside Chat — the turn is
	// guaranteed registered in AgentLoop.activeTurnStates by this point
	// (newTurnState runs before any provider call).
	select {
	case <-sp.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("BLOCKED: stubbornProvider never entered Chat")
	}

	activeWC := &wsConn{sendCh: make(chan []byte, 8), doneCh: make(chan struct{})}
	handler.emitSessionState(activeWC, sessionID)
	activeFrame := readSessionStateFrame(t, activeWC.sendCh)
	require.NotNil(t, activeFrame.ActiveTurn, "active_turn must be present while a foreground turn is in flight")
	assert.NotEmpty(t, activeFrame.ActiveTurn.TurnID)
	assert.Equal(t, "mia", activeFrame.ActiveTurn.AgentID)
	assert.NotEmpty(t, activeFrame.ActiveTurn.StartedAt)
	// ADR-082 review CR3.
	require.NotNil(t, activeFrame.SessionID)
	assert.Equal(t, sessionID, *activeFrame.SessionID)
}

func readSessionStateFrame(t *testing.T, ch chan []byte) struct {
	Type       string  `json:"type"`
	SessionID  *string `json:"session_id"`
	ActiveTurn *struct {
		TurnID    string `json:"turn_id"`
		AgentID   string `json:"agent_id"`
		StartedAt string `json:"started_at"`
	} `json:"active_turn"`
} {
	t.Helper()
	type frameT = struct {
		Type       string  `json:"type"`
		SessionID  *string `json:"session_id"`
		ActiveTurn *struct {
			TurnID    string `json:"turn_id"`
			AgentID   string `json:"agent_id"`
			StartedAt string `json:"started_at"`
		} `json:"active_turn"`
	}
	select {
	case raw := <-ch:
		var f frameT
		require.NoError(t, json.Unmarshal(raw, &f))
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for session_state frame")
		return frameT{}
	}
}

// TestFix_CR1_S1_SessionStateFirstInWireOrder proves the ADR-082 review
// CR1/S1 fix: a connection (re)attaching to a session with an in-flight
// streaming turn receives frames in EXACTLY this order:
//
//	session_state(session_id, active_turn) → replay_message* →
//	done(frames_emitted) → token(catch-up) → token*(live) →
//	done(stats.tokens)
//
// Before this fix, session_state was emitted LAST — after the replay-
// terminating done, the catch-up token, and the divert drain — so a
// reconnecting SPA had no active_turn signal until everything else had
// already arrived.
//
// Drives a REAL turn (controllableStreamProvider, paused mid-stream) rather
// than a bare wsStreamer fixture, precisely mirroring
// TestReconnectMidTurn_CatchUpThenLive (turn_survives_disconnect_test.go) —
// so session_state.active_turn is backed by a genuine
// AgentLoop.activeTurnStates entry, not a synthetic one.
func TestFix_CR1_S1_SessionStateFirstInWireOrder(t *testing.T) {
	tokens := []string{"partial-", "narration."}
	provider := newControllableStreamProvider(providerRound{tokens: tokens})
	paused, resume := provider.pauseAfter(0, 0) // pause after the FIRST token

	srv, _, _ := newControllableStreamTestServer(t, provider)

	connA := dialTestWS(t, srv)
	sendWSAuthFrameDevMode(t, connA)

	msgFrame := wsClientFrameTestHelper{Type: "message", Content: "start turn for CR1 wire order test"}
	data, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	require.NoError(t, connA.WriteMessage(websocket.TextMessage, data))

	started := readFrameOfType(t, connA, "session_started", 5*time.Second)
	sessionID := started.SessionID
	require.NotEmpty(t, sessionID)

	select {
	case <-paused:
	case <-time.After(5 * time.Second):
		t.Fatal("BLOCKED: provider never reached its pause point")
	}

	// Attach a SECOND connection while the turn is still paused mid-stream —
	// its own attach must see the in-flight turn.
	connB := dialTestWS(t, srv)
	t.Cleanup(func() { _ = connB.Close() })
	sendWSAuthFrameDevMode(t, connB)

	attachFrame := wsClientFrameTestHelper{Type: "attach_session", SessionID: sessionID}
	attachData, err := json.Marshal(attachFrame)
	require.NoError(t, err)
	require.NoError(t, connB.WriteMessage(websocket.TextMessage, attachData))

	type frameShape struct {
		Type       string  `json:"type"`
		Content    string  `json:"content"`
		SessionID  *string `json:"session_id"`
		ActiveTurn *struct {
			TurnID string `json:"turn_id"`
		} `json:"active_turn"`
		Stats *struct {
			Tokens        *float64 `json:"tokens"`
			FramesEmitted *float64 `json:"frames_emitted"`
		} `json:"stats"`
	}

	var frames []frameShape
	skippedConnOpen := false
	resumed := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		connB.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, raw, rerr := connB.ReadMessage()
		if rerr != nil {
			break
		}
		var f frameShape
		if json.Unmarshal(raw, &f) != nil {
			continue
		}
		if !skippedConnOpen && f.Type == "session_state" && f.SessionID == nil {
			// The connection-open one-shot, emitted before B's own
			// attach_session frame can even be read — proves nothing about
			// the attach itself (see handleAttachSession's own doc comment
			// on this ordering). Skip it; the NEXT session_state is the
			// attach's own, per this fix.
			skippedConnOpen = true
			continue
		}
		frames = append(frames, f)
		if !resumed && f.Type == "session_state" {
			// B's own attach-triggered session_state (frames[0]) has now
			// been observed — safe to let the paused provider continue.
			// Resuming any earlier risks a live token racing B's bind and
			// silently folding into the catch-up snapshot instead of
			// arriving as a separate live frame (no duplicate/gap either
			// way, but it would make the ordering assertions below flaky
			// rather than deterministic — see
			// TestReconnectMidTurn_CatchUpThenLive's identical caution).
			close(resume)
			resumed = true
		}
		if f.Type == "done" && f.Stats != nil && f.Stats.Tokens != nil {
			break // the turn's own final done — sequence complete
		}
	}

	require.NotEmpty(t, frames)
	assert.Equal(t, "session_state", frames[0].Type, "session_state must be the FIRST frame of the attach")
	require.NotNil(t, frames[0].SessionID, "session_state must carry session_id")
	assert.Equal(t, sessionID, *frames[0].SessionID)
	require.NotNil(t, frames[0].ActiveTurn, "session_state must report the in-flight turn")
	assert.NotEmpty(t, frames[0].ActiveTurn.TurnID)

	replayDoneIdx, catchUpIdx, turnDoneIdx := -1, -1, -1
	for i, f := range frames {
		switch {
		case f.Type == "done" && f.Stats != nil && f.Stats.FramesEmitted != nil && replayDoneIdx == -1:
			replayDoneIdx = i
		case f.Type == "token" && catchUpIdx == -1:
			catchUpIdx = i
		case f.Type == "done" && f.Stats != nil && f.Stats.Tokens != nil:
			turnDoneIdx = i
		}
	}
	require.Greater(t, replayDoneIdx, 0, "replay's own done{frames_emitted} must follow session_state")
	require.Greater(t, catchUpIdx, replayDoneIdx, "the catch-up token must follow the replay-terminating done")
	require.Greater(t, turnDoneIdx, catchUpIdx, "the turn's own final done must be last")
}
