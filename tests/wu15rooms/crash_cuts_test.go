package wu15rooms_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/rooms"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func entryFor(t *testing.T, snap gen.RoomSnapshotFrame, clientID string) gen.RoomEntry {
	t.Helper()
	var found []gen.RoomEntry
	for _, entry := range snap.Entries {
		if entry.ClientMessageId != nil && *entry.ClientMessageId == clientID {
			found = append(found, entry)
		}
	}
	require.Len(t, found, 1, "FR-216: one room entry for the caller id")
	return found[0]
}
func deliveryFor(t *testing.T, entry gen.RoomEntry, id string) gen.RoomDeliveryState {
	t.Helper()
	var found []gen.RoomDeliveryState
	for _, delivery := range entry.Deliveries {
		if delivery.AgentId == id {
			found = append(found, delivery)
		}
	}
	require.Len(t, found, 1)
	require.NotEmpty(t, found[0].RequestId)
	return found[0]
}
func (f *fixture) requireOneCapture(roomID, clientID string, agents ...string) gen.RoomEntry {
	f.t.Helper()
	entry := entryFor(f.t, f.snapshot(roomID), clientID)
	for _, id := range agents {
		delivery := deliveryFor(f.t, entry, id)
		capture, found, err := f.loop.RequestLedger().Get(f.main(id), delivery.RequestId)
		require.NoError(f.t, err)
		require.True(f.t, found)
		require.False(f.t, capture.Discarded)
		require.Equal(f.t, delivery.RequestId, capture.RequestID)
		require.Equal(f.t, f.main(id), capture.ReceiverSessionID)
		require.Equal(f.t, 1, f.writer.attempts(delivery.RequestId), "FR-216: Retry never calls receiver append twice")
		require.Equal(f.t, 1, f.writer.committed(delivery.RequestId))
	}
	return entry
}
func (f *fixture) reply(roomID, requestID, text string) error {
	_, err := f.loop.NewAddressRouter().Reply(context.Background(), tools.ReplyRequest{
		ReplyTo: requestID, Content: text, ResponderSessionID: f.main("mia"),
		Author: tools.SendOrigin{WorkspaceID: workspaceID, AgentID: "mia"},
	})
	return err
}
func (f *fixture) requireAnswerAndMirrorOnce(roomID, requestID, text string) gen.RoomEntry {
	f.t.Helper()
	var answers []gen.RoomEntry
	for _, entry := range f.snapshot(roomID).Entries {
		if entry.Author.Kind == "agent" && entry.Text == text {
			answers = append(answers, entry)
		}
	}
	require.Len(f.t, answers, 1, "FR-216: one committed room answer")
	messages, err := f.loop.GetSessionStore().ReadTranscript(f.main("mia"))
	require.NoError(f.t, err)
	mirrors := 0
	for _, message := range messages {
		if message.RoomRef != nil && message.RoomRef.RequestId == requestID && message.RoomRef.Role == "answer" {
			mirrors++
			require.Equal(f.t, text, message.Content)
		}
	}
	require.Equal(f.t, 1, mirrors, "FR-216: exact same answer mirrored once")
	return answers[0]
}

func TestWU15Rooms_CrashAfterDeliveryIDBeforeReceiverCapture(t *testing.T) {
	f := newFixture(t)
	room := f.room("Launch")
	f.journal.setCut(func(_ string, stream string, _ any) string {
		if stream == rooms.StreamEntries {
			return "after"
		}
		return ""
	})
	// The message and its delivery ids reached the real journal, but the
	// interrupted POST cannot start the fan-out after its append returned error.
	require.Error(t, f.post(room.Id, "request", "status?"))
	require.Equal(t, int64(1), f.journal.hits.Load(), "instrument: selected durable-before-capture cut fired")
	// The process cache cannot acknowledge an append whose writer returned
	// an error. Inspect only the real durable journal before restarting it.
	lines, err := f.journal.Read(room.Id, rooms.StreamDeliveries)
	require.NoError(t, err)
	require.Len(t, lines, 2, "both stable delivery ids were durable before the cut")
	beforeIDs := map[string]string{}
	for _, line := range lines {
		requestID, id, _ := deliveryIdentity(t, json.RawMessage(line))
		beforeIDs[id] = requestID
		_, found, err := f.loop.RequestLedger().Get(f.main(id), requestID)
		require.NoError(t, err)
		require.False(t, found, "cut really preceded receiver capture")
	}
	f.journal.setCut(nil)
	f.reload()
	require.NoError(t, f.post(room.Id, "request", "status?"))
	after := f.requireOneCapture(room.Id, "request", "mia", "jim")
	for _, id := range []string{"mia", "jim"} {
		require.Equal(t, beforeIDs[id], deliveryFor(t, after, id).RequestId, "delivery identity survived the cut")
	}
	require.NoError(t, f.post(room.Id, "request", "status?"))
	f.requireOneCapture(room.Id, "request", "mia", "jim")
}

func TestWU15Rooms_CrashAfterCommittedReceiverAppendBeforeRoomAcknowledgement(t *testing.T) {
	f := newFixture(t)
	room := f.room("Launch")
	f.journal.setCut(func(_ string, stream string, record any) string {
		if stream != rooms.StreamDeliveries {
			return ""
		}
		_, id, state := deliveryIdentity(t, record)
		if id == "mia" && state == "admitted" {
			return "before"
		}
		return ""
	})
	require.NoError(t, f.post(room.Id, "request", "status?"))
	require.Equal(t, int64(1), f.journal.hits.Load(), "instrument: receiver commit preceded lost acknowledgement")
	before := entryFor(t, f.snapshot(room.Id), "request")
	miaID := deliveryFor(t, before, "mia").RequestId
	require.Equal(t, 1, f.writer.committed(miaID), "real receiver append is the commit fact")
	f.journal.setCut(nil)
	f.reload()
	require.NoError(t, f.post(room.Id, "request", "status?"))
	f.requireOneCapture(room.Id, "request", "mia", "jim")
	// The real wake transport is buffered and synchronously published before
	// admission acknowledgement. No loop consumer ran: count exact wake ids.
	wakes := map[string]int{}
	for {
		select {
		case message := <-f.bus.InboundChan():
			wakes[message.TranscriptEntryID]++
		default:
			require.Equal(t, 1, wakes[miaID], "FR-216: lost room acknowledgement never wakes Mia twice")
			return
		}
	}
}

func TestWU15Rooms_CrashAfterRoomAnswerBeforeMainMirror(t *testing.T) {
	f := newFixture(t)
	room := f.room("Launch")
	require.NoError(t, f.post(room.Id, "request", "@Mia answer", "mia"))
	request := deliveryFor(t, entryFor(t, f.snapshot(room.Id), "request"), "mia")
	f.writer.setFault(func(_ string, entry session.TranscriptEntry) bool {
		return entry.RoomRef != nil && entry.RoomRef.Role == "answer"
	})
	replyErr := f.reply(room.Id, request.RequestId, "answer before mirror")
	require.Greater(t, f.writer.failed.Load(), int64(0), "instrument: real mirror append cut invoked")
	if !errors.Is(replyErr, errCrashCut) {
		t.Errorf("FR-216 / Q-H A: injected mirror error was swallowed: %v", replyErr)
	}
	var answers []gen.RoomEntry
	for _, entry := range f.snapshot(room.Id).Entries {
		if entry.Author.Kind == "agent" {
			answers = append(answers, entry)
		}
	}
	require.Len(t, answers, 1, "Q-H A: room answer had committed before the cut")
	messages, err := f.loop.GetSessionStore().ReadTranscript(f.main("mia"))
	require.NoError(t, err)
	for _, message := range messages {
		if message.RoomRef != nil {
			require.NotEqual(t, "answer", message.RoomRef.Role, "failed mirror is not a commit")
		}
	}
	f.writer.setFault(nil)
	f.reload()
	f.requireAnswerAndMirrorOnce(room.Id, request.RequestId, "answer before mirror")
	require.NoError(t, f.post(room.Id, "request", "@Mia answer", "mia"))
	f.requireAnswerAndMirrorOnce(room.Id, request.RequestId, "answer before mirror")
}

func TestWU15Rooms_CrashAfterMainMirrorBeforeMentionAdmission(t *testing.T) {
	f := newFixture(t)
	room := f.room("Launch")
	require.NoError(t, f.post(room.Id, "request", "@Mia answer", "mia"))
	request := deliveryFor(t, entryFor(t, f.snapshot(room.Id), "request"), "mia")
	var cut atomic.Bool
	f.journal.setCut(func(_ string, stream string, _ any) string {
		if stream == rooms.StreamMirrors {
			cut.Store(true)
			return "before"
		}
		return ""
	})
	replyErr := f.reply(room.Id, request.RequestId, "@Jim one mention")
	if replyErr != nil {
		require.ErrorIs(t, replyErr, errCrashCut, "a post-commit interrupted receipt must preserve the crash cause")
	}
	require.True(t, cut.Load())
	require.Equal(t, int64(1), f.journal.hits.Load(), "instrument: mirror marker cut fired before the receiver adapter could admit the mention")
	answer := f.requireAnswerAndMirrorOnce(room.Id, request.RequestId, "@Jim one mention")
	mention := deliveryFor(t, answer, "jim")
	_, found, err := f.loop.RequestLedger().Get(f.main("jim"), mention.RequestId)
	require.NoError(t, err)
	require.False(t, found, "cut really preceded mention admission")
	f.journal.setCut(nil)
	f.reload()
	require.NoError(t, f.post(room.Id, "request", "@Mia answer", "mia"))
	f.requireAnswerAndMirrorOnce(room.Id, request.RequestId, "@Jim one mention")
	capture, found, err := f.loop.RequestLedger().Get(f.main("jim"), mention.RequestId)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, mention.RequestId, capture.RequestID)
	require.Equal(t, 1, f.writer.attempts(mention.RequestId), "FR-216: one mention delivery")
	require.NoError(t, f.post(room.Id, "request", "@Mia answer", "mia"))
	require.Equal(t, 1, f.writer.attempts(mention.RequestId), "Retry did not append mention again")
}
