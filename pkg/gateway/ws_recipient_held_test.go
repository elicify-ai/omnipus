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

// R2-S2-02: the held tick is followed by "working" when the held request is
// finally delivered to the (resumed) recipient, through the same hub path.
func TestRecipient_HeldRequestDeliveryMovesTheSendersRowToWorking(t *testing.T) {
	h, b, _ := newRecipientHandler(t)
	wc := makeTestConn()
	sid := mintOwnerChat(t, h, b, wc)
	bindTestConnToSession(h, "chat-owner", sid, wc)
	stopRecipientMain(t, h, "ws-1", "ray")
	statusStates(t, wc, 50*time.Millisecond)

	to := addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}
	h.handleChatMessageToRecipient(context.Background(), "chat-owner", sid, "@ray wake up", "ann", nil, "", "ws-1", false, "held-cid-3", nil, &to, wc)
	require.Equal(t, []string{"received", "held"}, statusStates(t, wc, 300*time.Millisecond))

	recvMain, err := session.MainSessionID("ws-1", "ray")
	require.NoError(t, err)
	entries, err := h.agentLoop.GetSessionStore().ReadTranscript(recvMain)
	require.NoError(t, err)
	require.Len(t, entries, 1, "the request sits in the recipient's main")

	h.agentLoop.MarkRequestDeliveredForTest(recvMain, entries[0].ID)
	assert.Equal(t, []string{"working"}, statusStates(t, wc, 300*time.Millisecond),
		"delivery moves the row on exactly once, and never to failed")

	h.agentLoop.MarkRequestDeliveredForTest(recvMain, entries[0].ID)
	assert.Empty(t, statusStates(t, wc, 200*time.Millisecond), "a second delivery mark repeats nothing")
}

// R2-S2-03: a retried @-message answers with its CURRENT state, held included,
// not a fresh "received".
func TestRecipient_RetryOfAHeldMessageReplaysHeld(t *testing.T) {
	h, b, _ := newRecipientHandler(t)
	wc := makeTestConn()
	sid := mintOwnerChat(t, h, b, wc)
	bindTestConnToSession(h, "chat-owner", sid, wc)
	stopRecipientMain(t, h, "ws-1", "ray")
	statusStates(t, wc, 50*time.Millisecond)

	to := addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}
	send := func() {
		h.handleChatMessageToRecipient(context.Background(), "chat-owner", sid, "@ray wake up", "ann", nil, "", "ws-1", false, "held-cid-4", nil, &to, wc)
	}
	send()
	require.Equal(t, []string{"received", "held"}, statusStates(t, wc, 300*time.Millisecond))

	send() // the SPA re-sends the same client_message_id
	states := statusStates(t, wc, 300*time.Millisecond)
	require.NotEmpty(t, states)
	assert.Equal(t, "held", states[len(states)-1], "the replayed status is the current one; got %v", states)
}

// R2-S2-05: the request is recorded but its wake cannot be published: the row is
// reported as waiting, never failed (a failed row invites a duplicate re-send).
func TestRecipient_RecordedButWakeFailedIsHeldNotFailed(t *testing.T) {
	h, b, _ := newRecipientHandler(t)
	wc := makeTestConn()
	sid := mintOwnerChat(t, h, b, wc)
	bindTestConnToSession(h, "chat-owner", sid, wc)
	statusStates(t, wc, 50*time.Millisecond)
	b.Close() // every later PublishInbound fails

	to := addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}
	h.handleChatMessageToRecipient(context.Background(), "chat-owner", sid, "@ray hello", "ann", nil, "", "ws-1", false, "held-cid-5", nil, &to, wc)

	states := statusStates(t, wc, 300*time.Millisecond)
	assert.NotContains(t, states, "failed", "a recorded request is not a failed one: %v", states)
	assert.Equal(t, []string{"received", "held"}, states)
}

// R3 N1: the wake of a RUNNING recipient could not be published, so the request
// is recorded and reported held. When it is finally delivered the row must move
// on to working, exactly like a request held for a stopped recipient - the
// capture used to say Held=false and the row stayed "Waiting" for ever.
func TestRecipient_WakeFailedRequest_DeliveryMovesTheRowToWorking(t *testing.T) {
	h, b, _ := newRecipientHandler(t)
	wc := makeTestConn()
	sid := mintOwnerChat(t, h, b, wc)
	bindTestConnToSession(h, "chat-owner", sid, wc)
	statusStates(t, wc, 50*time.Millisecond)
	b.Close() // the recipient's wake cannot be published

	to := addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}
	h.handleChatMessageToRecipient(context.Background(), "chat-owner", sid, "@ray hello", "ann", nil, "", "ws-1", false, "held-cid-n1", nil, &to, wc)
	require.Equal(t, []string{"received", "held"}, statusStates(t, wc, 300*time.Millisecond))

	recvMain, err := session.MainSessionID("ws-1", "ray")
	require.NoError(t, err)
	entries, err := h.agentLoop.GetSessionStore().ReadTranscript(recvMain)
	require.NoError(t, err)
	require.Len(t, entries, 1, "the request is durable in the recipient's main")

	h.agentLoop.MarkRequestDeliveredForTest(recvMain, entries[0].ID)
	assert.Equal(t, []string{"working"}, statusStates(t, wc, 300*time.Millisecond),
		"delivery of a recorded-but-unwoken request moves the sender's row on")
}
