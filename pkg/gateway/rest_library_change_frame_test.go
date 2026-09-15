// Tests for the D-107 library_changed WS frame: the Library's REST write
// handlers must broadcast a frame after a mutation lands so other connected
// tabs can drop their stale folder listings. Two halves are pinned here:
//
//   1. WSHandler.broadcastLibraryChange fans the frame out to every connected
//      client (same construction + drop-counter shape as broadcastAskUserCard).
//   2. The REST write handlers actually emit it — once per landed mutation,
//      with the right workspace id, and NOT for refused operations (a 404
//      delete emits nothing: the tree did not change).

package gateway

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

func TestBroadcastLibraryChange_FanOutAndDropCounter(t *testing.T) {
	wcOK := &wsConn{sendCh: make(chan []byte, 4)}
	wcFull := &wsConn{sendCh: make(chan []byte)} // unbuffered, nobody reading → drop
	h := &WSHandler{sessions: map[string]*wsConn{"ok": wcOK, "full": wcFull}}

	path := "notes/a.txt"
	reason := "delete"
	h.broadcastLibraryChange(gen.LibraryChangedFrame{
		WorkspaceId: "ws-1",
		Path:        &path,
		Reason:      &reason,
	})

	select {
	case raw := <-wcOK.sendCh:
		var frame map[string]any
		require.NoError(t, json.Unmarshal(raw, &frame))
		assert.Equal(t, "library_changed", frame["type"])
		assert.Equal(t, "ws-1", frame["workspace_id"])
		assert.Equal(t, "notes/a.txt", frame["path"])
		assert.Equal(t, "delete", frame["reason"])
	default:
		t.Fatal("connected client with buffer room never received the frame")
	}
	assert.Equal(t, int32(1), wcFull.droppedFrames.Load(),
		"full-buffer client must count exactly one dropped frame")
	assert.Equal(t, int32(0), wcOK.droppedFrames.Load())
}

// libraryChangeRecorder captures every frame the handlers emit, in order.
func libraryChangeRecorder(api *restAPI) func() []gen.LibraryChangedFrame {
	var mu sync.Mutex
	frames := make([]gen.LibraryChangedFrame, 0, 8)
	fn := func(f gen.LibraryChangedFrame) {
		mu.Lock()
		defer mu.Unlock()
		frames = append(frames, f)
	}
	api.libraryChangeBroadcast.Store(&fn)
	return func() []gen.LibraryChangedFrame {
		mu.Lock()
		defer mu.Unlock()
		out := make([]gen.LibraryChangedFrame, len(frames))
		copy(out, frames)
		return out
	}
}

func requireFrame(t *testing.T, frames []gen.LibraryChangedFrame, workspaceID, path, reason string) {
	t.Helper()
	for _, f := range frames {
		if f.WorkspaceId == workspaceID && f.Path != nil && *f.Path == path && f.Reason != nil && *f.Reason == reason {
			return
		}
	}
	t.Fatalf("no library_changed frame with workspace=%q path=%q reason=%q; got %+v",
		workspaceID, path, reason, frames)
}

func TestLibraryWrites_EmitLibraryChangedFrame(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	frames := libraryChangeRecorder(api)

	// write: a text save creates a file (and a later save changes its size —
	// both change the listing).
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"a.txt","content":"x","expect_version":"v1:absent"}`).Code)
	requireFrame(t, frames(), id, "a.txt", "write")

	// mkdir: a new folder appears in the listing.
	require.Equal(t, http.StatusCreated,
		libPostJSON(t, api, "/api/v1/library/"+id+"/mkdir", `{"path":"sub"}`).Code)
	requireFrame(t, frames(), id, "sub", "mkdir")

	// upload: new entries land in the listing.
	require.Equal(t, http.StatusCreated,
		libUpload(t, api, "/api/v1/library/"+id+"/upload?path=sub", map[string]string{"u.txt": "u"}).Code)
	require.Len(t, frames(), 3, "write+mkdir+upload should each have emitted exactly one frame; got %+v", frames())

	// rename: the old name disappears and the new one appears.
	require.Equal(t, http.StatusOK,
		libPostJSON(t, api, "/api/v1/library/"+id+"/rename", `{"from":"a.txt","to":"b.txt"}`).Code)
	requireFrame(t, frames(), id, "b.txt", "rename")

	// delete: the entry disappears.
	require.Equal(t, http.StatusNoContent,
		libDelete(t, api, "/api/v1/library/"+id+"/entries?path=b.txt").Code)
	requireFrame(t, frames(), id, "b.txt", "delete")

	// REFUSED operations must not emit: a delete of a missing path is a 404
	// that changed nothing.
	before := len(frames())
	assert.Equal(t, http.StatusNotFound,
		libDelete(t, api, "/api/v1/library/"+id+"/entries?path=b.txt").Code)
	assert.Len(t, frames(), before, "a refused (404) delete must not emit a frame")

	// A refused WRITE (version conflict) must not emit either: the tree did
	// not change. expect_version "v1:absent" over an existing file conflicts.
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"keep.txt","content":"1","expect_version":"v1:absent"}`).Code)
	before = len(frames())
	assert.Equal(t, http.StatusConflict,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"keep.txt","content":"2","expect_version":"v1:absent"}`).Code)
	assert.Len(t, frames(), before, "a refused (409 conflict) write must not emit a frame")
}

// A cross-workspace MOVE changes BOTH trees: the source workspace loses the
// entry, the destination gains it. One frame naming only the source would
// leave the destination tab serving a listing that predates the arrival.
func TestLibraryTransfer_CrossWorkspaceMoveEmitsBothSides(t *testing.T) {
	api, id := buildLibraryTestAPI(t)
	id2 := seedLibraryWorkspace(t, api, "Dest WS")
	frames := libraryChangeRecorder(api)

	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"mv.txt","content":"x","expect_version":"v1:absent"}`).Code)
	require.Equal(t, http.StatusOK,
		libPostJSON(t, api, "/api/v1/library/move", `{"from_workspace_id":"`+id+`","from_path":"mv.txt","to_workspace_id":"`+id2+`","to_path":"mv.txt"}`).Code)

	got := frames()
	requireFrame(t, got, id, "mv.txt", "move")
	requireFrame(t, got, id2, "mv.txt", "move")
}

// The broadcaster is nil until the gateway wires the WS handler in at boot;
// every write handler must survive that (test APIs, and any boot ordering
// hiccup) rather than nil-deref.
func TestLibraryWrites_NilBroadcasterIsSafe(t *testing.T) {
	api, id := buildLibraryTestAPI(t) // no broadcaster installed
	require.Equal(t, http.StatusOK,
		libPutJSON(t, api, "/api/v1/library/"+id+"/content", `{"path":"a.txt","content":"x","expect_version":"v1:absent"}`).Code)
	require.Equal(t, http.StatusNoContent,
		libDelete(t, api, "/api/v1/library/"+id+"/entries?path=a.txt").Code)
}
