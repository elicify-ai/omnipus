// Copyright (c) 2026 Omnipus contributors
// License: MIT

package gateway

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// ADR-082, UI-independent turns and session-bound webchat streaming, D6:
// the real turn's outbound must carry its transcript session, independent of a
// viewer's connection-local chat ID. The channel's explicit-session delivery
// already works; an absent producer field defeats it when that socket closes.
// The provider/operation are external edges; the worker, loop, bus and channel
// remain real. Existing E2E checks retain the cursor-reconnect/render oracle.
func TestWS_TerminalOutcome_OutboundRetainsSessionIdentity(t *testing.T) {
	provider := &terminalAcceptanceProvider{narration: "I will run the acceptance probe."}
	probe := &terminalAcceptanceProbe{}
	handler, _, delivered := newTerminalAcceptanceHandler(t, provider, probe, 1)
	store := handler.agentLoop.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)
	conn := dialTestWS(t, srv)
	t.Cleanup(func() { assert.NoError(t, conn.Close()) })
	sendWSAuthFrameDevMode(t, conn)
	agentID := "mia"
	require.NoError(t, conn.WriteJSON(generated.MessageFrame{
		Type: "message", Content: "Run the acceptance probe.", AgentId: &agentID, SessionId: &meta.ID,
	}))
	select {
	case delivery := <-delivered:
		require.NoError(t, delivery.err)
		require.Equal(t, terminalAcceptanceCapNotice, delivery.message.Content, "real loop reached the narrated cap branch")
		require.Equal(t, int32(1), provider.calls.Load(), "one provider round")
		require.Equal(t, int32(1), probe.calls.Load(), "one actual tool operation")
		require.Equal(t, "webchat", delivery.message.Channel)
		require.Equal(t, meta.ID, delivery.message.SessionID, "terminal delivery must not depend on the old socket's chat-to-session map")
	case <-time.After(busDeliveryTimeout):
		t.Fatal("real terminal outbound was not delivered")
	}
}
