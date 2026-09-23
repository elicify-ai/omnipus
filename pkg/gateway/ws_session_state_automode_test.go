// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// ADR-092 review finding D: the per-chat Auto-approve modifier survives a
// page reload on the server (SessionModeStore holds it until the session
// ends), so the session_state snapshot a reconnecting SPA receives must
// carry it — otherwise the composer switch and badge fall back to the
// agent x global value while the server still applies the chat's own.

// readSessionStateModifier reads one session_state frame from ch and returns
// the raw auto_approve_modifier member: (value, present). A JSON null is
// reported as present with a nil value so the test can tell null from absent.
func readSessionStateModifier(t *testing.T, ch chan []byte) (modifier *bool, present bool, sessionID *string) {
	t.Helper()
	select {
	case raw := <-ch:
		var members map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &members))
		var typ string
		require.NoError(t, json.Unmarshal(members["type"], &typ))
		require.Equal(t, "session_state", typ)
		if sid, ok := members["session_id"]; ok {
			var s string
			require.NoError(t, json.Unmarshal(sid, &s))
			sessionID = &s
		}
		rawMod, ok := members["auto_approve_modifier"]
		if !ok {
			return nil, false, sessionID
		}
		var v *bool
		require.NoError(t, json.Unmarshal(rawMod, &v))
		return v, true, sessionID
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for session_state frame")
		return nil, false, nil
	}
}

func TestSessionStateFrame_CarriesPerChatAutoApproveModifier(t *testing.T) {
	workspaceDir := filepath.Join(t.TempDir(), "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: workspaceDir, MaxTokens: 4096},
			List:     []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{Mode: config.SandboxModeOff, AutoApprove: true},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	handler := newWSHandler(msgBus, al, "")

	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	emit := func(sessionID string) (*bool, bool, *string) {
		wc := &wsConn{sendCh: make(chan []byte, 4), doneCh: make(chan struct{})}
		handler.emitSessionState(wc, sessionID)
		return readSessionStateModifier(t, wc.sendCh)
	}

	// No modifier set: the snapshot says so (null/absent), the chat follows
	// the agent x global resolution.
	mod, present, sid := emit(meta.ID)
	require.NotNil(t, sid)
	assert.Nil(t, mod, "no per-chat modifier set: auto_approve_modifier must be null/absent (present=%v)", present)

	// Modifier turned OFF for this chat while the global default is ON: the
	// reconnect snapshot must report false, not silently drop it.
	require.True(t, al.SessionModes().Set(meta.ID, false))
	mod, present, _ = emit(meta.ID)
	require.True(t, present, "a set modifier must be carried in session_state")
	require.NotNil(t, mod)
	assert.False(t, *mod)

	// Modifier turned ON.
	require.True(t, al.SessionModes().Set(meta.ID, true))
	mod, present, _ = emit(meta.ID)
	require.True(t, present)
	require.NotNil(t, mod)
	assert.True(t, *mod)

	// Another session's modifier never leaks into this one's snapshot.
	other, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	mod, _, _ = emit(other.ID)
	assert.Nil(t, mod, "a session with no modifier of its own must report none")

	// The connection-open emit (no session yet) carries no modifier.
	mod, present, sid = emit("")
	assert.Nil(t, sid)
	assert.False(t, present, "the connection-open emit has no session, so no modifier")
	assert.Nil(t, mod)

	// Session end clears the modifier; the snapshot follows the server
	// (the same thing a gateway restart does, since the store is in memory).
	al.SessionModes().ClearSession(meta.ID)
	mod, _, _ = emit(meta.ID)
	assert.Nil(t, mod, "after the server drops the modifier the snapshot must say none")
}
