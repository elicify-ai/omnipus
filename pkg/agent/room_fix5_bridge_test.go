package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/rooms"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// The direct-entry tests need package-private wake entry points. This bridge
// contains no room/request state machine: Service, journals, ledger, transcript,
// admission and completion are real. Only workspace-directory/effect edges and
// the paid model are fixtures. Jim is an unaddressed directory member.
type fix5RoomBridge struct {
	al        *AgentLoop
	svc       *rooms.Service
	workspace string
}

func (b *fix5RoomBridge) LookupWorkspace(id string) (rooms.WorkspaceInfo, bool, error) {
	return rooms.WorkspaceInfo{ID: b.workspace, Name: "Test", Agents: []rooms.Agent{{ID: testDefaultAgentID, Name: "Mia"}, {ID: "jim", Name: "Jim"}}}, id == b.workspace, nil
}
func (*fix5RoomBridge) AgentName(id string) (string, bool) {
	switch id {
	case testDefaultAgentID:
		return "Mia", true
	case "jim":
		return "Jim", true
	default:
		return "", false
	}
}
func (b *fix5RoomBridge) SessionInWorkspace(id, ws string) bool {
	m, err := b.al.GetSessionStore().GetMeta(id)
	return err == nil && m.WorkspaceID == ws && m.Type == session.SessionTypeMain
}
func (*fix5RoomBridge) Limits() (int, int) { return 60, 65536 }
func (b *fix5RoomBridge) PairEligible(ws, id string) (bool, error) {
	return ws == b.workspace && id == testDefaultAgentID, nil
}
func (*fix5RoomBridge) PublishGuestReply(string, session.TranscriptEntry) {}
func (*fix5RoomBridge) Publish(string, any)                               {}
func (*fix5RoomBridge) Audit(rooms.AuditEvent)                            {}
func (*fix5RoomBridge) CancelApproval(string)                             {}
func (b *fix5RoomBridge) Admit(ctx context.Context, a rooms.Admission) (rooms.AdmitResult, error) {
	r, err := b.al.AdmitRoomRequest(ctx, RoomRequestAdmission{
		Receiver: a.Receiver, DeliveryID: a.DeliveryID, RoomID: a.RoomID, RoomName: a.RoomName,
		Sender: a.Sender, SenderLabel: a.SenderLabel, DisplayText: a.DisplayText, ModelText: a.ModelText,
		OnAwaitingApproval: a.OnAwaitingApproval, BeforeCommit: a.BeforeCommit,
	})
	return rooms.AdmitResult{ReceiverSessionID: r.ReceiverSessionID, Held: r.Held, AlreadyAdmitted: r.AlreadyAdmitted}, err
}
func (b *fix5RoomBridge) Facts(p addressing.Pair, id string) (rooms.ReceiverFacts, error) {
	sid, err := session.MainSessionID(p.WorkspaceID, p.AgentID)
	if err != nil {
		return rooms.ReceiverFacts{}, err
	}
	c, found, err := b.al.RequestLedger().Get(sid, id)
	return rooms.ReceiverFacts{Found: found, Delivered: c.Delivered, Discarded: c.Discarded, Held: c.Held, SessionID: sid}, err
}
func (b *fix5RoomBridge) Discard(sid, id string) error { return b.al.DiscardRequest(sid, id) }
func (b *fix5RoomBridge) Mirror(m rooms.Mirror) error {
	return b.al.MirrorRoomAnswer(m.MainSessionID, m.MirrorID, m.Author, m.Ref, m.Content)
}
func (b *fix5RoomBridge) BeginRoomConsume(room, request, sid string) bool {
	return b.svc.BeginConsume(room, request, sid)
}
func (b *fix5RoomBridge) RoomConsumeHeld(room, request string) { b.svc.ConsumeHeld(room, request) }
func (b *fix5RoomBridge) RoomConsumeEnded(room, request string, err error) (bool, error) {
	return b.svc.ConsumeEnded(room, request, err)
}
func (b *fix5RoomBridge) RoomReminderTurnEnded(sid string) error { return b.svc.ReminderTurnEnded(sid) }
func (b *fix5RoomBridge) RoomTurnCost(c RoomTurnCost) {
	b.svc.AddTurnCost(rooms.TurnCost{RoomID: c.RoomID, RequestID: c.RequestID, TurnID: c.TurnID, CostUSD: c.CostUSD, Priced: c.Priced, Inputs: c.Inputs, RoomInputs: c.RoomInputs})
}
func (b *fix5RoomBridge) DeliverRoomAnswer(ctx context.Context, a RoomAnswer) (tools.ReplyReceipt, error) {
	r, err := b.svc.DeliverAnswer(ctx, rooms.Answer{RoomID: a.RoomID, RequestID: a.RequestID, Responder: a.Responder, ResponderSessionID: a.ResponderSessionID, Content: a.Content})
	return tools.ReplyReceipt{ReplyID: r.AnswerID, Destination: "the group chat " + r.RoomName}, err
}

func newFix5RoomMain(t *testing.T, stopped bool) (*d2bRoot, *fix5RoomBridge, gen.Room, string) {
	t.Helper()
	h := newD2bRoot(t)
	meta, err := h.al.GetSessionStore().GetOrCreateMainSession(testHarnessWorkspaceMembershipID, testDefaultAgentID)
	require.NoError(t, err)
	h.id = meta.ID
	owner := "fix5-owner"
	require.NoError(t, h.al.GetSessionStore().SetMeta(h.id, session.MetaPatch{Owner: &owner}))
	state := session.LifecycleCompleted
	if stopped {
		state = session.LifecycleStopped
	}
	h.seedState(t, state)
	b := &fix5RoomBridge{al: h.al, workspace: testHarnessWorkspaceMembershipID}
	b.svc = rooms.New(rooms.NewFileJournal(t.TempDir()), rooms.DepsFrom(b))
	t.Cleanup(func() { b.svc.Close(); b.svc.WaitIdleForTest() })
	h.al.SetRoomRuntime(b)
	h.al.SetAddressDeps(b)
	setSendMessagePolicy(t, h.al, testDefaultAgentID, "allow")
	name := "Launch"
	room, err := b.svc.Create(b.workspace, gen.RoomCreateRequest{Kind: gen.RoomCreateRequestKindSquad, Name: &name, AgentIds: []string{testDefaultAgentID, "jim"}}, "tester")
	require.NoError(t, err)
	require.NoError(t, b.svc.PostMessage(room.Id, gen.RoomMessageRequest{ClientMessageId: "request", Text: "DIRECT_ROOM_INPUT", Mentions: []string{testDefaultAgentID}}, "tester"))
	b.svc.WaitIdleForTest()
	snap := fix5RoomSnapshot(t, b, room.Id)
	require.Len(t, snap.MemberStates, 1)
	return h, b, room, snap.MemberStates[0].RequestId
}

func fix5RoomSnapshot(t *testing.T, b *fix5RoomBridge, room string) gen.RoomSnapshotFrame {
	t.Helper()
	var snap gen.RoomSnapshotFrame
	require.NoError(t, b.svc.Attach(room, 0, func(s gen.RoomSnapshotFrame) { snap = s }))
	return snap
}
