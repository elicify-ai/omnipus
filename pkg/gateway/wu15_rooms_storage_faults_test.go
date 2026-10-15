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

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

var errWU15WriterFailure = errors.New("wu15 injected persistence failure")

// Wrap the existing error-returning writer only: readers, archive, turn loop and
// room admission/reply remain real. No production-only room seam is invented.
type wu15FailingWriter struct {
	*session.UnifiedStore
	target, role string
	attempts     atomic.Int64
}

func (w *wu15FailingWriter) AddMessage(key, role, content string) error {
	if key == w.target && role == w.role {
		w.attempts.Add(1)
		return errWU15WriterFailure
	}
	return w.UnifiedStore.AddMessage(key, role, content)
}
func (w *wu15FailingWriter) AddFullMessage(key string, msg providers.Message) error {
	if key == w.target && msg.Role == w.role {
		w.attempts.Add(1)
		return errWU15WriterFailure
	}
	return w.UnifiedStore.AddFullMessage(key, msg)
}
func (w *wu15FailingWriter) AppendModelMessage(ctx context.Context, key string, in session.ModelAppend) (session.ModelSlot, session.WindowView, error) {
	if key == w.target && in.Message.Role == w.role {
		w.attempts.Add(1)
		return session.ModelSlot{}, session.WindowView{}, errWU15WriterFailure
	}
	return w.UnifiedStore.AppendModelMessage(ctx, key, in)
}
func (w *wu15FailingWriter) PlaceSavedInput(ctx context.Context, key string, source session.ArchiveAddress) (session.ModelSlot, session.WindowView, error) {
	if key == w.target && w.role == "user" {
		w.attempts.Add(1)
		return session.ModelSlot{}, session.WindowView{}, errWU15WriterFailure
	}
	return w.UnifiedStore.PlaceSavedInput(ctx, key, source)
}

func TestWU15Rooms_FailedReceiverWriterNeverCountsAsSuccessfulAnswer(t *testing.T) {
	for _, role := range []string{"user", "assistant"} {
		t.Run(role, func(t *testing.T) {
			h := wu15NewHarness(t)
			room := h.create("squad", "Launch", "mia", "jim")
			inst, ok := h.al.GetRegistry().GetAgent("mia")
			require.True(t, ok)
			original := inst.Sessions
			fault := &wu15FailingWriter{UnifiedStore: h.al.GetSessionStore(), target: h.main("mia"), role: role}
			inst.Sessions = fault
			// No extra tool reply: final assistant append itself is the fault.
			p := &wu15ScriptProvider{answer: "this uncommitted answer must not be accepted", autoReply: true}
			h.provider("mia", p)
			conn := h.connect()
			require.NoError(t, conn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
			wu15ReadFrame(t, conn, "catch_up_complete")
			h.startLoop()
			h.post(room.Id, "writer-fault", "@Mia test durable write")
			request := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "writer-fault"), "mia")
			snap := h.waitState(room.Id, request.RequestId, "failed")
			require.Greater(t, fault.attempts.Load(), int64(0), "instrument: failed writer was actually invoked")
			for _, entry := range snap.Entries {
				require.NotEqual(t, p.answer, entry.Text, "FR-216/#1178: failed append is never inferred as committed answer")
			}
			var errorFrame gen.ErrorFrame
			require.NoError(t, json.Unmarshal(wu15ReadFrame(t, conn, "error"), &errorFrame))
			require.Equal(t, "Sorry, I couldn't save your message — please try again later.", errorFrame.Message, "amendment §4.2: existing visible could-not-save notice")
			var done gen.DoneFrame
			require.NoError(t, json.Unmarshal(wu15ReadFrame(t, conn, "done"), &done))
			require.NotNil(t, done.Stats)
			require.NotNil(t, done.Stats.TurnFailed)
			require.True(t, *done.Stats.TurnFailed)
			inst.Sessions = original
			h.post(room.Id, "healthy-control", "@Mia healthy writer")
			healthy := wu15Delivery(t, wu15FindEntry(t, h.snapshot(room.Id), "healthy-control"), "mia")
			require.NotEqual(t, request.RequestId, healthy.RequestId)
			// The healthy turn is driven through the same main and writer path.
			wu15ReadFrame(t, conn, "done")
			healthySnap := h.waitState(room.Id, healthy.RequestId, "answered")
			var answers []gen.RoomEntry
			for _, entry := range healthySnap.Entries {
				if entry.Author.Kind == "agent" {
					answers = append(answers, entry)
				}
			}
			require.Len(t, answers, 1, "only the healthy turn publishes a committed room answer")
			require.Equal(t, p.answer, answers[0].Text, "positive control uses the same real writer path")
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

var _ session.SessionWriter = (*wu15FailingWriter)(nil)
var _ session.ContextWindowStore = (*wu15FailingWriter)(nil)
