package gateway

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestWU15Rooms_DirectCrossWorkspacePeerMessagingRemainsSeparate(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	before := h.snapshot(room.Id)
	receipt, err := h.al.NewAddressRouter().SendPeer(context.Background(), tools.PeerRequest{
		RecipientWorkspaceID: "marketing", RecipientAgentID: "ray", Content: "direct cross-workspace request",
		Sender: tools.SendOrigin{WorkspaceID: wu15Workspace, AgentID: "mia"}, SenderSessionID: h.main("mia"),
	})
	require.NoError(t, err, "FR-173/BDD-17.13: existing cross-workspace main-to-main rule is preserved")
	rayMain, err := session.MainSessionID("marketing", "ray")
	require.NoError(t, err)
	require.Equal(t, rayMain, receipt.SessionID)
	capture, found, err := h.al.RequestLedger().Get(rayMain, receipt.RequestID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, addressing.SourceConversation, capture.Source.Kind, "FR-173: direct peer request is not a room request")
	require.Equal(t, h.main("mia"), capture.Source.SessionID)
	_, err = h.al.NewAddressRouter().Reply(context.Background(), tools.ReplyRequest{
		ReplyTo: receipt.RequestID, Content: "Ray's direct answer", ResponderSessionID: rayMain,
		Author: tools.SendOrigin{WorkspaceID: "marketing", AgentID: "ray"},
	})
	require.NoError(t, err)
	require.Equal(t, before.Entries, h.snapshot(room.Id).Entries, "FR-173: direct reply never appears in the room")
	var answers int
	for _, message := range h.messages("mia") {
		if message.Content != nil && *message.Content == "Ray's direct answer" {
			answers++
			require.Nil(t, message.RoomRef)
		}
	}
	require.Equal(t, 1, answers, "existing direct reply remains visible once in its owning main")
}
