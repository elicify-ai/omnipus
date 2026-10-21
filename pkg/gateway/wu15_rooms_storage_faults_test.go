package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

var errWU15WriterFailure = errors.New("wu15 injected persistence failure")

// wu15AwaitAnswerMirror polls the receiver main's history until the answer
// mirror for requestId exists, then asserts the exact mirror contract. The
// order is designed (spec K27 §6.11; FR-215 append → mirror → fan-out):
// pkg/rooms/answers.go::recordAnswer sets "answered" under the room lock and
// mirrorAnswer writes the main-chat mirror afterwards, outside the lock — so
// "answered" is observable before the mirror exists, and a mirror read
// without this wait races the write under load. The deadline bounds a real
// regression (a mirror that never comes fails the test naming the missing
// mirror); wu15RequireMirrors still requires EXACTLY one mirror, so a
// duplicate mirror fails the test just as before.
func wu15AwaitAnswerMirror(t *testing.T, h *wu15RoomHarness, room gen.Room, requestID, answer string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		messages := h.messages("mia")
		for _, msg := range messages {
			if msg.RoomRef != nil && msg.RoomRef.RequestId == requestID && msg.RoomRef.Role == gen.MessageRoomRefRoleAnswer {
				wu15RequireMirrors(t, messages, room, requestID, answer)
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the answer mirror for request %s never reached the receiver main within 30s — FR-215 orders the mirror after the answered state, so the test must wait for it before asserting", requestID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Q-D: fault the exact named RoomReceiverWriter append, not inst.Sessions.
// The underlying store remains real and every unrelated append is forwarded.
type wu15FailingWriter struct {
	*session.UnifiedStore
	target, role string
	attempts     atomic.Int64
}

func (w *wu15FailingWriter) AppendTranscriptOnce(key string, entry session.TranscriptEntry) (bool, error) {
	if key == w.target && entry.Role == w.role {
		w.attempts.Add(1)
		return false, errWU15WriterFailure
	}
	return w.UnifiedStore.AppendTranscriptOnce(key, entry)
}

func TestWU15Rooms_FailedReceiverWriterNeverCountsAsSuccessfulAnswer(t *testing.T) {
	for _, role := range []string{"user", "assistant"} {
		t.Run(role, func(t *testing.T) {
			h := wu15NewHarness(t)
			room := h.create("squad", "Launch", "mia", "jim")
			fault := &wu15FailingWriter{UnifiedStore: h.al.GetSessionStore(), target: h.main("mia"), role: role}
			if role == "user" {
				h.roomWriter(fault)
				p := &wu15ScriptProvider{answer: "healthy receiver answer", autoReply: true}
				h.provider("mia", p)
				h.startLoop()
				h.post(room.Id, "writer-fault", "@Mia test durable write")
				require.Greater(t, fault.attempts.Load(), int64(0), "instrument: actual receiver append fault invoked before outcome assertions")
				request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "writer-fault"), "mia")
				require.Equal(t, "unavailable", request.State, "Q-H A / §6.11: failure was before any receiver turn")
				require.Empty(t, p.inputs(), "failed receiver append starts no model turn")
				_, captured, captureErr := h.al.RequestLedger().Get(h.main("mia"), request.RequestId)
				require.NoError(t, captureErr)
				require.False(t, captured, "Q-H A: failed receiver append writes no capture, not even a discarded admission")
				for _, message := range h.messages("mia") {
					if message.RoomRef != nil {
						require.NotEqual(t, request.RequestId, message.RoomRef.RequestId, "FR-216: no false committed receiver append")
					}
				}
				h.roomWriter(nil)
				h.post(room.Id, "writer-fault", "@Mia test durable write")
				h.waitState(room.Id, request.RequestId, "answered")
				wu15AwaitAnswerMirror(t, h, room, request.RequestId, p.answer)
				return
			}
			h.post(room.Id, "writer-fault", "@Mia test durable mirror")
			request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "writer-fault"), "mia")
			h.roomWriter(fault)
			answer := "this answer committed to the room before its main mirror"
			_, replyErr := h.al.NewAddressRouter().Reply(context.Background(), tools.ReplyRequest{
				ReplyTo: request.RequestId, Content: answer, ResponderSessionID: h.main("mia"),
				Author: tools.SendOrigin{WorkspaceID: wu15Workspace, AgentID: "mia"},
			})
			require.Greater(t, fault.attempts.Load(), int64(0), "instrument: actual mirror append fault invoked before outcome assertions")
			// Q-H A: do not adapt to a service swallowing the writer error.
			if !errors.Is(replyErr, errWU15WriterFailure) {
				t.Errorf("FR-216 / chief Q-H A: mirror failure must be returned, not swallowed; got %v", replyErr)
			}
			var committed []gen.RoomEntry
			for _, entry := range h.snapshot(room.Id).Entries {
				if entry.Author.Kind == "agent" && entry.Text == answer {
					committed = append(committed, entry)
				}
			}
			require.Len(t, committed, 1, "Q-H A: already committed room answer is retained")
			for _, message := range h.messages("mia") {
				if message.RoomRef != nil && message.RoomRef.RequestId == request.RequestId {
					require.NotEqual(t, gen.MessageRoomRefRoleAnswer, message.RoomRef.Role, "failed mirror was not committed")
				}
			}
			h.roomWriter(nil)
			h = h.restart()
			h.snapshot(room.Id) // actual reload reconciliation repairs the missing mirror
			wu15RequireMirrors(t, h.messages("mia"), room, request.RequestId, answer)
			h.snapshot(room.Id)
			wu15RequireMirrors(t, h.messages("mia"), room, request.RequestId, answer)
		})
	}
}

func TestWU15Rooms_ReceiverCaptureWriteFailureIsUnavailableThenRetryCommitsOnce(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")
	h.stopMain("mia")
	path := filepath.Join(h.al.GetSessionStore().BaseDir(), h.main("mia"), "requests.jsonl")
	restore := wu15BlockPath(t, path)
	h.post(room.Id, "capture-fault", "@Mia durable admission")
	request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "capture-fault"), "mia")
	require.Equal(t, "unavailable", request.State, "§6.11: storage refusal before admission, not admitted/working")
	require.NotNil(t, request.Reason)
	restore()
	_, found, err := h.al.RequestLedger().Get(h.main("mia"), request.RequestId)
	require.NoError(t, err)
	require.False(t, found, "instrument: failed write created no receiver facts")
	h.post(room.Id, "capture-fault", "@Mia durable admission")
	snap := h.snapshot(room.Id)
	retried := wu15Delivery(t, wu15FindEntry(t, snap, "capture-fault"), "mia")
	require.Equal(t, request.RequestId, retried.RequestId, "FR-216: Retry retains pre-recorded delivery id")
	capture, found, err := h.al.RequestLedger().Get(h.main("mia"), request.RequestId)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, capture.Held)
	h.post(room.Id, "capture-fault", "@Mia durable admission")
	var requests []gen.Message
	for _, msg := range h.messages("mia") {
		if msg.RoomRef != nil && msg.RoomRef.RequestId == request.RequestId {
			requests = append(requests, msg)
		}
	}
	require.Len(t, requests, 1, "FR-216: only successful committed receiver append counts")
}

// Replacing a file by a directory forces a real I/O error even as root. Preserve
// and restore bytes without stash, permissions tricks or touching real HOME.
func wu15BlockPath(t *testing.T, path string) func() {
	t.Helper()
	backup := filepath.Join(t.TempDir(), "saved")
	_, err := os.Stat(path)
	hadFile := err == nil
	if hadFile {
		require.NoError(t, os.Rename(path, backup))
	} else {
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	require.NoError(t, os.MkdirAll(path, 0o700))
	_, readErr := os.ReadFile(path)
	require.Error(t, readErr, "instrument: fault path is definitely unreadable as a file")
	var once sync.Once
	restore := func() {
		once.Do(func() {
			require.NoError(t, os.Remove(path))
			if hadFile {
				require.NoError(t, os.Rename(backup, path))
			}
		})
	}
	t.Cleanup(restore)
	return restore
}

func TestWU15Rooms_Issue1314ListStorageFailureReturns500ContractError(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Read failure sentinel", "mia", "jim")
	before := h.listRooms(wu15Workspace)
	// Discover the actual persisted room record after public creation; the
	// spec names no private storage path, so no implementation path is guessed.
	var records []string
	err := filepath.WalkDir(h.home, func(path string, info fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		var stored gen.Room
		if json.Unmarshal(data, &stored) == nil && stored.Id == room.Id && stored.WorkspaceId == wu15Workspace {
			records = append(records, path)
		}
		return nil
	})
	require.NoError(t, err)
	if len(records) != 1 {
		t.Fatalf("BLOCKED: discoverable room persistence read-fault boundary not implemented — required by #1314 (records=%v)", records)
	}
	restore := wu15BlockPath(t, records[0])
	status, body := h.http(http.MethodGet, "/api/v1/workspaces/sales/rooms", nil)
	wu15RequireError(t, status, body, http.StatusInternalServerError, "") // Chief Q1 A; contract owner adds 500.
	restore()
	require.Equal(t, before, h.listRooms(wu15Workspace), "#1314: read failure never deletes cached/persisted room records")
}

var _ wu15RoomCommitWriter = (*wu15FailingWriter)(nil)
