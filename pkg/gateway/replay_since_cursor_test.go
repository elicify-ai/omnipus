// replay_since_cursor_test.go — tests for the since-cursor filtering feature
// (Work item A) and the lazy tool-result offload feature (Work item B).

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStreamReplay_SinceCursor_Integration verifies the full round-trip:
// streamReplay with a pre-filtered entry slice skips old entries correctly.
func TestStreamReplay_SinceCursor_Integration(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	entries := []session.TranscriptEntry{
		{
			ID:        "e1",
			Role:      "user",
			Content:   "hello",
			Timestamp: base.Add(-time.Second),
		},
		{
			ID:        "e2",
			Role:      "user",
			Content:   "world",
			Timestamp: base.Add(time.Second),
		},
	}

	// Filter to only entries after base.
	cursorStr := base.Format(time.RFC3339Nano)
	filtered := applySinceCursor(context.Background(), "sid", &cursorStr, entries, nil)
	require.Len(t, filtered, 1, "cursor must filter to one entry")

	sink := &sliceSink{}
	rs := computeReplayStats(filtered)
	_, err := streamReplay(context.Background(), "sid", filtered, rs, sink.emit, nil, nil, nil)
	require.NoError(t, err)

	frames := sink.all()
	// Expect: 1 replay_message (for "world") + 1 done
	require.Len(t, frames, 2, "must emit exactly 1 content frame + 1 done frame")
	assert.Equal(t, "replay_message", frames[0].Type)
	assert.Equal(t, "world", frames[0].Content)
	assert.Equal(t, "done", frames[1].Type)
}

// ─────────────────────────────────────────────────────────────────────────────
// Work item B — toolResultStore
// ─────────────────────────────────────────────────────────────────────────────

// TestToolResultStore_SaveAndRead verifies the write-then-read round-trip.
func TestToolResultStore_SaveAndRead(t *testing.T) {
	dir := t.TempDir()
	store := newToolResultStore(dir)

	body := []byte(`{"answer":42}`)
	ref, ok := store.saveJSON("sess123", body)
	require.True(t, ok, "saveJSON must succeed")
	require.NotEmpty(t, ref, "saveJSON must return a non-empty ref")

	// Verify file exists with mode 0600.
	path := filepath.Join(dir, "tool_results", "sess123", ref+".json")
	info, err := os.Stat(path)
	require.NoError(t, err, "file must exist on disk")
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "file must be mode 0600")

	// Read it back.
	got, err := store.readByRef("sess123", ref)
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

// TestToolResultStore_NilStore verifies that a nil store is a no-op.
func TestToolResultStore_NilStore(t *testing.T) {
	var store *toolResultStore
	ref, ok := store.saveJSON("sid", []byte(`{}`))
	assert.False(t, ok)
	assert.Empty(t, ref)

	_, err := store.readByRef("any-session", "anyref")
	assert.Error(t, err)
}

// TestToolResultStore_EmptyHomePath verifies that a store with empty homePath is disabled.
func TestToolResultStore_EmptyHomePath(t *testing.T) {
	store := newToolResultStore("")
	ref, ok := store.saveJSON("sid", []byte(`{}`))
	assert.False(t, ok)
	assert.Empty(t, ref)
}

// TestToolResultStore_NotFound verifies that reading a non-existent ref returns an error.
func TestToolResultStore_NotFound(t *testing.T) {
	dir := t.TempDir()
	store := newToolResultStore(dir)
	_, err := store.readByRef("any-session", "doesnotexist0123456789abcdef01")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrToolResultNotFound)
}

// TestMaybeOffloadResult_BelowThreshold verifies that small results are not offloaded.
func TestMaybeOffloadResult_BelowThreshold(t *testing.T) {
	dir := t.TempDir()
	store := newToolResultStore(dir)

	// 1-byte payload — well below 50 KiB.
	encoded := []byte(`1`)
	sentinel, offloaded := maybeOffloadResult(store, "sid", encoded)
	assert.False(t, offloaded)
	assert.Nil(t, sentinel)
}

// TestMaybeOffloadResult_AboveThreshold verifies that results exceeding 50 KiB are offloaded.
func TestMaybeOffloadResult_AboveThreshold(t *testing.T) {
	dir := t.TempDir()
	store := newToolResultStore(dir)

	// 60 KiB payload — above the 50 KiB threshold.
	large := make([]byte, 60*1024)
	for i := range large {
		large[i] = 'x'
	}
	// JSON-encode as a string.
	encoded, err := json.Marshal(string(large))
	require.NoError(t, err)
	require.Greater(t, len(encoded), InlineToolResultMaxBytes)

	sentinel, offloaded := maybeOffloadResult(store, "sid", encoded)
	require.True(t, offloaded, "result exceeding threshold must be offloaded")

	// Sentinel must be a ToolResultRef.
	ref, ok := sentinel.(generated.ToolResultRef)
	require.True(t, ok, "sentinel must be generated.ToolResultRef")
	assert.True(t, ref.IsRef, "_ref must be true")
	assert.NotEmpty(t, ref.Ref, "ref must be non-empty")
	assert.Equal(t, len(encoded), ref.OriginalSizeBytes)
	assert.Len(t, []byte(ref.Preview), min(toolResultPreviewBytes, len(encoded)),
		"preview must be min(4KiB, encoded_size)")

	// The file must be readable by ref (under the same session).
	got, readErr := store.readByRef("sid", ref.Ref)
	require.NoError(t, readErr)
	assert.Equal(t, encoded, got)
}

// TestMaybeOffloadResult_NilStore verifies that a nil store returns (nil, false).
func TestMaybeOffloadResult_NilStore(t *testing.T) {
	large := make([]byte, 60*1024)
	sentinel, offloaded := maybeOffloadResult(nil, "sid", large)
	assert.False(t, offloaded)
	assert.Nil(t, sentinel)
}

// ─────────────────────────────────────────────────────────────────────────────
// Work item B — REST handler for GET /api/v1/sessions/{session_id}/tool-results/{ref}
// ─────────────────────────────────────────────────────────────────────────────

// TestHandleToolResults_NotFound verifies that a missing ref returns 404.
func TestHandleToolResults_NotFound(t *testing.T) {
	dir := t.TempDir()
	api := &restAPI{homePath: dir}

	req := newTestReq(t, "GET", "/api/v1/sessions/sess-a/tool-results/doesnotexist0123456789abcdef01", nil)
	rw := newTestRW()
	api.HandleToolResults(rw, req)
	assert.Equal(t, 404, rw.code)
}

// TestHandleToolResults_MethodNotAllowed verifies that POST returns 405.
func TestHandleToolResults_MethodNotAllowed(t *testing.T) {
	dir := t.TempDir()
	api := &restAPI{homePath: dir}

	req := newTestReq(t, "POST", "/api/v1/sessions/sess-a/tool-results/anyref", nil)
	rw := newTestRW()
	api.HandleToolResults(rw, req)
	assert.Equal(t, 405, rw.code)
}

// TestHandleToolResults_InvalidRef verifies that a ref with path traversal characters returns 400.
func TestHandleToolResults_InvalidRef(t *testing.T) {
	dir := t.TempDir()
	api := &restAPI{homePath: dir}

	req := newTestReq(t, "GET", "/api/v1/sessions/sess-a/tool-results/../secret", nil)
	rw := newTestRW()
	api.HandleToolResults(rw, req)
	assert.Equal(t, 400, rw.code)
}

// TestHandleToolResults_Found verifies that a stored result is returned as application/json.
func TestHandleToolResults_Found(t *testing.T) {
	dir := t.TempDir()
	store := newToolResultStore(dir)
	body := []byte(`{"ok":true}`)
	ref, ok := store.saveJSON("sessionA", body)
	require.True(t, ok)

	api := &restAPI{homePath: dir}
	req := newTestReq(t, "GET", "/api/v1/sessions/sessionA/tool-results/"+ref, nil)
	rw := newTestRW()
	api.HandleToolResults(rw, req)

	assert.Equal(t, 200, rw.code)
	assert.Equal(t, "application/json", rw.header.Get("Content-Type"))
	assert.Equal(t, body, rw.body.Bytes())
}

// TestHandleToolResults_CrossSessionForbidden verifies that a ref from session A
// cannot be fetched under session B's path (returns 404).
func TestHandleToolResults_CrossSessionForbidden(t *testing.T) {
	dir := t.TempDir()
	store := newToolResultStore(dir)
	body := []byte(`{"secret":"session-a-data"}`)
	refA, ok := store.saveJSON("session-a", body)
	require.True(t, ok)

	api := &restAPI{homePath: dir}
	// Attempt to fetch session-a's ref under session-b's path.
	req := newTestReq(t, "GET", "/api/v1/sessions/session-b/tool-results/"+refA, nil)
	rw := newTestRW()
	api.HandleToolResults(rw, req)

	assert.Equal(t, 404, rw.code, "cross-session fetch must return 404 (not found under session-b)")
}

// ─────────────────────────────────────────────────────────────────────────────
// Test helpers — minimal HTTP request/response writer
// ─────────────────────────────────────────────────────────────────────────────

type testResponseWriter struct {
	header http.Header
	body   *bytes.Buffer
	code   int
}

func newTestRW() *testResponseWriter {
	return &testResponseWriter{
		header: make(http.Header),
		body:   &bytes.Buffer{},
		code:   200,
	}
}

func (rw *testResponseWriter) Header() http.Header { return rw.header }

func (rw *testResponseWriter) WriteHeader(code int) { rw.code = code }

func (rw *testResponseWriter) Write(b []byte) (int, error) { return rw.body.Write(b) }

func newTestReq(t *testing.T, method, path string, _ any) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, path, nil)
	require.NoError(t, err)
	req.URL.Path = path
	return req
}
