package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestWU15Rooms_RealHTTPAndWSMemberTurnAnswersAndMirrorsOnce(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	roomConn := h.connect()
	h.attach(roomConn, room.Id, 0)
	h.post(room.Id, "reachability", "@Mia draft the exact launch plan")
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "reachability"), "mia")
	mainConn := h.connect()
	require.NoError(t, mainConn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
	wu15ReadFrame(t, mainConn, "catch_up_complete")
	p := &wu15ScriptProvider{requestID: request.RequestId, answer: "Exact launch plan: ship on Friday."}
	h.provider("mia", p)
	other := &wu15ScriptProvider{answer: "must not wake Jim"}
	h.provider("jim", other)
	h.startLoop()
	answer := wu15ReadAgentRoomEntry(t, roomConn, "mia", p.answer)
	require.Equal(t, room.Id, answer.RoomId)
	require.Empty(t, answer.Addressees, "FR-165: plain agent answer never broadcasts")
	wu15AssertProviderSaw(t, p, "draft the exact launch plan")
	snap := h.waitState(room.Id, request.RequestId, "answered")
	require.Empty(t, other.inputs(), "FR-215: a room has no owner to wake")
	wu15AwaitAnswerMirror(t, h, room, request.RequestId, p.answer)
	done := wu15ReadTurnDone(t, mainConn)
	require.Equal(t, h.main("mia"), done.SessionId, "R2: receiver's main is the real actor")
	require.NotNil(t, done.TurnId, "real turn, not handcrafted transcript insertion")
	calls := len(p.inputs())
	h.post(room.Id, "reachability", "@Mia draft the exact launch plan")
	retried := h.snapshot(room.Id)
	require.Equal(t, snap.Entries, retried.Entries, "FR-216: completed Retry is idempotent")
	require.Equal(t, calls, len(p.inputs()), "FR-216: Retry never starts a second model turn")
	wu15AwaitAnswerMirror(t, h, room, request.RequestId, p.answer)
	// Every actor session is an ordinary main. The room itself is not a session.
	for _, id := range []string{"mia", "jim"} {
		meta, err := h.al.GetSessionStore().GetMeta(h.main(id))
		require.NoError(t, err)
		require.Equal(t, id, meta.AgentID)
		require.Equal(t, "main", string(meta.Type))
		require.Equal(t, wu15Workspace, meta.WorkspaceID)
	}
	_, roomSessionErr := h.al.GetSessionStore().GetMeta(room.Id)
	require.Error(t, roomSessionErr, "FR-199: no session is created for the room")
}

func wu15ReadAgentRoomEntry(t *testing.T, conn *websocket.Conn, agentID, text string) gen.RoomEntry {
	t.Helper()
	for {
		var frame gen.RoomEntryFrame
		require.NoError(t, json.Unmarshal(wu15ReadFrame(t, conn, "room_entry"), &frame))
		if frame.Entry.Author.Kind == "agent" && frame.Entry.Author.AgentId != nil && *frame.Entry.Author.AgentId == agentID && frame.Entry.Text == text {
			return frame.Entry
		}
	}
}

func wu15RequireMirrors(t *testing.T, msgs []gen.Message, room gen.Room, requestID, answer string) {
	t.Helper()
	var requests, answers []gen.Message
	for _, msg := range msgs {
		if msg.RoomRef == nil || msg.RoomRef.RoomId != room.Id || msg.RoomRef.RequestId != requestID {
			continue
		}
		require.Equal(t, room.Name, msg.RoomRef.RoomName, "K33: same saved room name")
		if msg.RoomRef.Role == gen.MessageRoomRefRoleRequest {
			requests = append(requests, msg)
		}
		if msg.RoomRef.Role == gen.MessageRoomRefRoleAnswer {
			answers = append(answers, msg)
		}
	}
	require.Len(t, requests, 1, "FR-202/216: one main request carrier")
	require.Len(t, answers, 1, "FR-215/216: one main answer mirror")
	require.NotNil(t, answers[0].Content)
	require.Equal(t, answer, *answers[0].Content, "FR-215: mirror equals room answer verbatim")
}

func TestWU15Rooms_MainLiveHistoryAndReplayShareRoomReferences(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	mainConn := h.connect()
	require.NoError(t, mainConn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
	wu15ReadFrame(t, mainConn, "catch_up_complete")
	h.post(room.Id, "carriers", "@Mia mirror this request")
	var live gen.UserMessageFrame
	require.NoError(t, json.Unmarshal(wu15ReadFrame(t, mainConn, "user_message"), &live))
	require.NotNil(t, live.RoomRef, "K33/#1281: live request carries room_ref")
	require.Equal(t, room.Id, live.RoomRef.RoomId)
	require.Equal(t, "request", live.RoomRef.Role)
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "carriers"), "mia")
	require.Equal(t, request.RequestId, live.RoomRef.RequestId)
	p := &wu15ScriptProvider{requestID: request.RequestId, answer: "same answer in every carrier"}
	h.provider("mia", p)
	h.startLoop()
	h.waitState(room.Id, request.RequestId, "answered")
	wu15AwaitAnswerMirror(t, h, room, request.RequestId, p.answer)
	wu15ReadFrame(t, mainConn, "done")
	replayConn := h.connect()
	require.NoError(t, replayConn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
	refs := wu15ReplayRoomRefs(t, replayConn, room.Id)
	require.ElementsMatch(t, []gen.RoomRef{
		{RoomId: room.Id, RoomName: room.Name, RequestId: request.RequestId, Role: "request"},
		{RoomId: room.Id, RoomName: room.Name, RequestId: request.RequestId, Role: "answer"},
	}, refs, "K33: snapshot replay, live and REST carry identical request/answer links")
}

func wu15ReplayRoomRefs(t *testing.T, conn *websocket.Conn, roomID string) []gen.RoomRef {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(30*time.Second)))
	var refs []gen.RoomRef
	for {
		_, data, err := conn.ReadMessage()
		require.NoError(t, err)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(data, &fields))
		var kind string
		require.NoError(t, json.Unmarshal(fields["type"], &kind))
		if kind == "catch_up_complete" {
			return refs
		}
		if kind == "replay_message" {
			var frame gen.ReplayMessageFrame
			require.NoError(t, json.Unmarshal(data, &frame))
			if frame.RoomRef != nil && frame.RoomRef.RoomId == roomID {
				refs = append(refs, *frame.RoomRef)
			}
		}
	}
}

func TestWU15Rooms_AttachCursorIsOrderedAndDoesNotReplayEarlierEntries(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	h.post(room.Id, "pause", "/pause")
	h.post(room.Id, "first", "first")
	before := h.snapshot(room.Id)
	h.post(room.Id, "second", "second")
	all := h.snapshot(room.Id)
	conn := h.connect()
	after := h.attach(conn, room.Id, before.Seq)
	var wanted []gen.RoomEntry
	for _, entry := range all.Entries {
		if entry.Seq > before.Seq {
			wanted = append(wanted, entry)
		}
	}
	require.Equal(t, wanted, after.Entries, "K45: exact catch-up entries after cursor")
	require.GreaterOrEqual(t, after.Seq, before.Seq, "K45: no cursor rewind")
	last := before.Seq
	for _, entry := range after.Entries {
		require.Greater(t, entry.Seq, last)
		last = entry.Seq
		require.Equal(t, room.Id, entry.RoomId)
	}
}

func TestWU15Rooms_BoundedContextIncludesSinceLastRequestAndPriorAgentAnswer(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	h.post(room.Id, "jim-plan", "@Jim plan it")
	jimReq := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "jim-plan"), "jim")
	_, err := h.al.NewAddressRouter().Reply(context.Background(), tools.ReplyRequest{ReplyTo: jimReq.RequestId, Content: "Jim's exact launch plan", ResponderSessionID: h.main("jim"), Author: tools.SendOrigin{WorkspaceID: wu15Workspace, AgentID: "jim"}})
	require.NoError(t, err)
	h.post(room.Id, "mia-review", "@Mia review Jim's plan")
	miaReq := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "mia-review"), "mia")
	capture, found, err := h.al.RequestLedger().Get(h.main("mia"), miaReq.RequestId)
	require.NoError(t, err)
	require.True(t, found)
	require.Contains(t, capture.Text, "Jim's exact launch plan", "BDD-17.16: room context is real, not request text alone")
	h.post(room.Id, "pause", "/pause")
	for i := 0; i < 25; i++ {
		h.post(room.Id, "context-"+fmt.Sprint(i), "context-row-"+fmt.Sprint(i)+" "+strings.Repeat("é", 800))
	}
	h.post(room.Id, "resume", "/resume")
	h.post(room.Id, "bounded", "@Mia bounded context check")
	bounded := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "bounded"), "mia")
	capture, found, err = h.al.RequestLedger().Get(h.main("mia"), bounded.RequestId)
	require.NoError(t, err)
	require.True(t, found)
	require.LessOrEqual(t, strings.Count(capture.Text, "context-row-"), 20, "FR-200: at most twenty context entries")
	require.LessOrEqual(t, strings.Count(capture.Text, "é")*2, 8192, "FR-200: UTF-8 context byte bound, not rune count")
	require.NotContains(t, capture.Text, "Jim's exact launch plan", "FR-200: context before Mia's previous room request is not re-sent")
	require.Contains(t, capture.Text, "bounded context check")
}
