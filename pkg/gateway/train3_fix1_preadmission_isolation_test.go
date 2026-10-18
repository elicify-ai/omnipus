package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// Gate round 1, pr-test-analyzer F4: a room's PRE-ADMISSION approval card
// (FR-251, RoomPreAdmission) is not the member's work. While Mia is working
// on room A's request, a new room-B request that needs her approval must not
// mark A's running request "waiting_on_user" (FR-169 marks the member's OWN
// approvals only). The control — a member's own in-turn approval does mark
// its owned work waiting — is TestWU15Rooms_TurnToolApprovalWaitsInReceiverMainNotRoom.
func TestTrain3Fix1_PreAdmissionCardDoesNotMarkOtherRoomWorkWaiting(t *testing.T) {
	h := wu15NewHarness(t)
	roomA := h.create("squad", "Alpha", "mia", "jim")
	roomB := h.create("squad", "Beta", "mia", "jim")
	busy := &wu15ScriptProvider{answer: "done", entered: make(chan struct{}), release: make(chan struct{})}
	h.provider("mia", busy)
	h.startLoop()
	t.Cleanup(func() {
		for _, e := range h.api.approvalReg.pendingApprovals() {
			h.api.approvalReg.resolve(e.ApprovalID, ApprovalActionCancel, false)
		}
		select {
		case <-busy.release:
		default:
			close(busy.release)
		}
	})

	h.post(roomA.Id, "work-a", "@Mia work on A")
	reqA := wu15Delivery(t, wu15FindEntry(t, h.snapshot(roomA.Id), "work-a"), "mia")
	<-busy.entered // Mia's turn is running room A's request (barrier, not a sleep)
	h.waitState(roomA.Id, reqA.RequestId, "working")

	h.receiverPolicy("mia", config.ToolPolicyAsk)
	h.post(roomB.Id, "ask-b", "@Mia look at B")
	reqB := wu15Delivery(t, wu15FindEntry(t, h.snapshot(roomB.Id), "ask-b"), "mia")
	h.waitState(roomB.Id, reqB.RequestId, "awaiting_approval")

	for _, state := range h.snapshot(roomA.Id).MemberStates {
		if state.RequestId == reqA.RequestId {
			require.Equal(t, "working", state.State,
				"room B's pre-admission card must not mark room A's running request waiting_on_user")
			return
		}
	}
	t.Fatalf("room A's request %s is missing from its snapshot", reqA.RequestId)
}
