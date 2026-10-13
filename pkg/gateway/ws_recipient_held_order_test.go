package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/addressing"
)

// R3-SRV-CR-03 / SFH-05: the held tick is sent after the admission returns, so a
// recipient that resumes inside that window can deliver the request first. The
// late "held" must then be dropped: the row never goes back from working to
// waiting, and a retried client_message_id replays the CURRENT state.
func TestRecipient_AHeldTickThatArrivesAfterWorkingIsDropped(t *testing.T) {
	h, b, _ := newRecipientHandler(t)
	wc := makeTestConn()
	sid := mintOwnerChat(t, h, b, wc)
	bindTestConnToSession(h, "chat-owner", sid, wc)
	stopRecipientMain(t, h, "ws-1", "ray")
	statusStates(t, wc, 50*time.Millisecond)

	to := addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}
	h.handleChatMessageToRecipient(context.Background(), "chat-owner", sid, "@ray wake up", "ann", nil, "", "ws-1", false, "held-cid-n3", nil, &to, wc)
	require.Equal(t, []string{"received", "held"}, statusStates(t, wc, 300*time.Millisecond))

	h.announceHeldRequestDelivered(sid, "held-cid-n3") // the recipient resumed and took it
	require.Equal(t, []string{"working"}, statusStates(t, wc, 300*time.Millisecond))

	h.announceRequestHeld(sid, "held-cid-n3", wc) // the late held tick
	assert.Empty(t, statusStates(t, wc, 300*time.Millisecond), "no held frame after working")
	assert.Equal(t, "working", h.acceptedMessageState(sid, "held-cid-n3"),
		"the state a retry replays must stay working")
}
