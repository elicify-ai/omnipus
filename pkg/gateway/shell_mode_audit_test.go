// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// ADR-092 FR-032(a): each of the three Auto-approve write points writes one
// shell.mode_change audit event. Each test fails if its call site is removed.

// newShellModeAuditAPI builds a restAPI over a real AgentLoop with audit
// logging on, Auto-approve on globally, and one agent ("agent-a"). Returns
// the api and the directory tree the audit log lives under.
func newShellModeAuditAPI(t *testing.T) (*restAPI, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	root := t.TempDir()
	home := filepath.Join(root, "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              home,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 20,
			},
			List: []config.AgentConfig{{ID: "agent-a", Name: "Agent A"}},
		},
		Context: config.DefaultContextSettings(),
	}
	cfg.Sandbox.AuditLog = true
	cfg.Sandbox.AutoApprove = true
	require.NoError(t, os.WriteFile(home+"/config.json", marshalConfigForDisk(t, cfg), 0o600))

	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	require.NotNil(t, al.AuditLogger(), "audit logging must be on for these tests")
	seedRoutingAgentEntities(t, home, cfg.Agents.List)
	return &restAPI{agentLoop: al, homePath: home}, root
}

// shellModeChanges returns every shell.mode_change entry under root.
func shellModeChanges(t *testing.T, root string) []audit.Entry {
	t.Helper()
	var out []audit.Entry
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".jsonl" || filepath.Base(filepath.Dir(path)) != "system" {
			return err
		}
		f, openErr := os.Open(path)
		if openErr != nil {
			return openErr
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			var e audit.Entry
			if json.Unmarshal(sc.Bytes(), &e) == nil && e.Event == audit.EventShellModeChange {
				out = append(out, e)
			}
		}
		return sc.Err()
	})
	require.NoError(t, err)
	return out
}

func TestShellModeAudit_GlobalAutoApproveWrite(t *testing.T) {
	api, root := newShellModeAuditAPI(t)
	w := sandboxConfigPUT(t, api, `{"auto_approve":false}`)
	require.Equal(t, 200, w.Code, "body: %s", w.Body.String())

	events := shellModeChanges(t, root)
	require.Len(t, events, 1)
	assert.Equal(t, "global", events[0].Details["level"])
	assert.Equal(t, "ask", events[0].Details["new_mode"])
	assert.Equal(t, "operator", events[0].Details["actor"])
	assert.Equal(t, "sandbox.auto_approve", events[0].PolicyRule)
}

func TestShellModeAudit_PerAgentAutoApproveDisabledWrite(t *testing.T) {
	api, root := newShellModeAuditAPI(t)
	w := putAgentJSON(t, api, "agent-a", `{"auto_approve_disabled":true}`)
	require.Equal(t, 200, w.Code, "body: %s", w.Body.String())

	events := shellModeChanges(t, root)
	require.Len(t, events, 1)
	assert.Equal(t, "agent", events[0].Details["level"])
	assert.Equal(t, "ask", events[0].Details["new_mode"], "global on + agent off-switch resolves to Ask")
	assert.Equal(t, "agent-a", events[0].AgentID)
}

func TestShellModeAudit_PerAgentWriteWithoutTheFieldIsSilent(t *testing.T) {
	api, root := newShellModeAuditAPI(t)
	w := putAgentJSON(t, api, "agent-a", `{"color":"#123456"}`)
	require.Equal(t, 200, w.Code, "body: %s", w.Body.String())
	assert.Empty(t, shellModeChanges(t, root))
}

func TestShellModeAudit_PerChatSessionModeUpdate(t *testing.T) {
	api, root := newShellModeAuditAPI(t)
	wc := &wsConn{sendCh: make(chan []byte, 4), doneCh: make(chan struct{})}
	wh := &wsHandlerReadLoop{h: &WSHandler{agentLoop: api.agentLoop}, ctx: context.Background(), wc: wc}

	flow := wh.dispatchFrame([]byte(`{"type":"session_mode_update","session_id":"chat-1","auto_approve":false}`),
		wsTypeOnly{Type: "session_mode_update"})
	require.Equal(t, wsHandlerReadLoopNext, flow)

	v, ok := api.agentLoop.SessionModes().Get("chat-1")
	require.True(t, ok, "the per-chat modifier must be stored")
	assert.False(t, v)

	events := shellModeChanges(t, root)
	require.Len(t, events, 1)
	assert.Equal(t, "chat", events[0].Details["level"])
	assert.Equal(t, "ask", events[0].Details["new_mode"])
	assert.Equal(t, "chat-1", events[0].SessionID)

	var ack map[string]any
	require.NoError(t, json.Unmarshal(<-wc.sendCh, &ack))
	assert.Equal(t, "session_mode_updated", ack["type"])
	assert.Equal(t, false, ack["auto_approve_effective"], "chat off tightens a global on")
}

func TestSessionModeUpdate_NullClearsAndLoosenOnIsAllowed(t *testing.T) {
	api, _ := newShellModeAuditAPI(t)
	wc := &wsConn{sendCh: make(chan []byte, 4), doneCh: make(chan struct{})}
	wh := &wsHandlerReadLoop{h: &WSHandler{agentLoop: api.agentLoop}, ctx: context.Background(), wc: wc}
	send := func(body string) map[string]any {
		require.Equal(t, wsHandlerReadLoopNext, wh.dispatchFrame([]byte(body), wsTypeOnly{Type: "session_mode_update"}))
		var ack map[string]any
		require.NoError(t, json.Unmarshal(<-wc.sendCh, &ack))
		return ack
	}

	send(`{"type":"session_mode_update","session_id":"chat-2","auto_approve":false}`)
	ack := send(`{"type":"session_mode_update","session_id":"chat-2","auto_approve":null}`)
	_, ok := api.agentLoop.SessionModes().Get("chat-2")
	assert.False(t, ok, "null must clear the modifier")
	assert.Equal(t, true, ack["auto_approve_effective"], "cleared chat follows the global default (on)")

	api.agentLoop.GetConfig().Sandbox.AutoApprove = false
	ack = send(`{"type":"session_mode_update","session_id":"chat-2","auto_approve":true}`)
	assert.Equal(t, true, ack["auto_approve_effective"], "the chat scope may loosen past a global off")
}
