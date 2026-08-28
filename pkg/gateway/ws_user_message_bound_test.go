// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// countingProvider counts Chat calls; any call means a turn started.
type countingProvider struct{ calls atomic.Int32 }

func (p *countingProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	return &providers.LLMResponse{Content: "turn ran"}, nil
}
func (p *countingProvider) GetDefaultModel() string { return "counting-model" }

// TestWS_UserMessageBound_NoTranscriptEntryNoErrorFrame is the WebSocket
// intake half of spec test 38 (ADR-066 D4, FR-015, B-17, SC-005): the WS
// handler persists the user message ahead of the bus publish, so it must
// SKIP that write for a message processMessage is about to refuse. Over
// the bound: no transcript entry on the minted session, no provider call
// (no turn), no error frame; the refusal travels the ordinary outbound
// path on the webchat channel, quoting N and the live limit.
func TestWS_UserMessageBound_NoTranscriptEntryNoErrorFrame(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         t.TempDir(),
				DefaultModel: config.DefaultModel{Model: "counting-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
	}
	msgBus := bus.NewMessageBus()
	provider := &countingProvider{}
	al := mustAgentLoop(t, cfg, msgBus, provider)

	ctx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		if err := al.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Logf("agent loop Run exited: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancelRun()
		select {
		case <-runDone:
		case <-time.After(cancelTestTurnStartDeadline):
			t.Logf("agent loop Run did not exit within %v of cancel", cancelTestTurnStartDeadline)
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

	bound := al.UserMessageBound()
	require.Equal(t, config.DefaultBuiltinSuccessCap, bound, "the bound tracks the builtin-success cap")

	data, err := json.Marshal(wsClientFrameTestHelper{Type: "message", Content: strings.Repeat("x", bound+1)})
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	started := readFrameOfType(t, conn, "session_started", cancelTestTurnStartDeadline)
	sessionID := started.SessionID
	require.NotEmpty(t, sessionID)

	// The refusal is an ordinary outbound reply on the webchat channel (in
	// production the channel manager turns it into token + done frames).
	select {
	case out := <-msgBus.OutboundChan():
		assert.Equal(t, "webchat", out.Channel)
		assert.Contains(t, out.Content, "64001 characters")
		assert.Contains(t, out.Content, "limit is 64000")
	case <-time.After(cancelTestTurnStartDeadline):
		t.Fatal("no refusal reply published on the bus")
	}

	assert.Equal(t, int32(0), provider.calls.Load(), "no turn: the provider was never called")

	entries, err := al.GetSessionStore().ReadTranscript(sessionID)
	require.NoError(t, err)
	assert.Empty(t, entries, "the WS handler must not persist a user entry for a refused message")

	// No error frame: drain for a short window and fail on one.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(deadline)
		_, raw, rerr := conn.ReadMessage()
		if rerr != nil {
			break
		}
		var f replayFrameDecoder
		require.NoError(t, json.Unmarshal(raw, &f))
		assert.NotEqual(t, "error", f.Type, "an over-bound message must never produce an error frame: %s", string(raw))
	}
}
