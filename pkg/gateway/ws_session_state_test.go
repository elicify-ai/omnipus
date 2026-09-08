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
}

func readSessionStateFrame(t *testing.T, ch chan []byte) struct {
	Type       string `json:"type"`
	ActiveTurn *struct {
		TurnID    string `json:"turn_id"`
		AgentID   string `json:"agent_id"`
		StartedAt string `json:"started_at"`
	} `json:"active_turn"`
} {
	t.Helper()
	type frameT = struct {
		Type       string `json:"type"`
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
