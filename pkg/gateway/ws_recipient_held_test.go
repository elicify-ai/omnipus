package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// S2-11, founder ruling F1 (2026-10-12): when a person @-messages a STOPPED
// agent the request is held, and the sender's row is told so with a
// message_status "held" after "received"; nothing later turns a held request
// into "failed". ORACLE: that ruling and contracts MessageStatusFrame.yaml.

func stopRecipientMain(t *testing.T, h *WSHandler, ws, agentID string) {
	t.Helper()
	lc := session.NewLifecycleStore(t.TempDir())
	h.agentLoop.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lc)
	main, err := h.agentLoop.GetSessionStore().GetOrCreateMainSession(ws, agentID)
	require.NoError(t, err)
	require.NoError(t, lc.Persist(&session.LifecycleRecord{
		SessionID: main.ID, Generation: 2, State: session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID: ws, AgentID: agentID,
		Stop: &session.Stop{At: time.Now(), Generation: 2, By: session.Principal{Kind: session.PrincipalKindHuman}},
	}))
}

// statusStates drains wc for window and returns every message_status state in order.
func statusStates(t *testing.T, wc *wsConn, window time.Duration) []string {
	t.Helper()
	var states []string
	deadline := time.After(window)
	for {
		select {
		case raw := <-wc.sendCh:
			var f struct {
				Type  string `json:"type"`
				State string `json:"state"`
			}
			require.NoError(t, json.Unmarshal(raw, &f))
			if f.Type == "message_status" {
				states = append(states, f.State)
			}
		case <-deadline:
			return states
		}
	}
}

func TestRecipient_StoppedRecipientHoldsTheRequestAndTellsTheSenderRow(t *testing.T) {
	h, b, _ := newRecipientHandler(t)
	wc := makeTestConn()
	sid := mintOwnerChat(t, h, b, wc)
	bindTestConnToSession(h, "chat-owner", sid, wc)
	stopRecipientMain(t, h, "ws-1", "ray")
	statusStates(t, wc, 50*time.Millisecond) // drop owner-chat leftovers

	to := addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}
	h.handleChatMessageToRecipient(context.Background(), "chat-owner", sid, "@ray are you there", "ann", nil, "", "ws-1", false, "held-cid-1", nil, &to, wc)

	assert.Equal(t, []string{"received", "held"}, statusStates(t, wc, 400*time.Millisecond),
		"received first, then held, and never failed for a held request")
}

func TestRecipient_RunnableRecipientIsNeverReportedHeld(t *testing.T) {
	h, b, _ := newRecipientHandler(t)
	wc := makeTestConn()
	sid := mintOwnerChat(t, h, b, wc)
	bindTestConnToSession(h, "chat-owner", sid, wc)
	h.agentLoop.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), session.NewLifecycleStore(t.TempDir()))
	statusStates(t, wc, 50*time.Millisecond)

	to := addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}
	h.handleChatMessageToRecipient(context.Background(), "chat-owner", sid, "@ray hello", "ann", nil, "", "ws-1", false, "held-cid-2", nil, &to, wc)

	assert.Equal(t, []string{"received"}, statusStates(t, wc, 400*time.Millisecond))
}
