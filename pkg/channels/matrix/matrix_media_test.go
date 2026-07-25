//go:build goolm

package matrix

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/id"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/media"
)

// newTestMatrixChannel builds a running MatrixChannel with no live mautrix
// client — sufficient for exercising SendMedia's resolve/stat/open failure
// paths, which `continue` past the client entirely and never reach
// UploadMedia/SendMessageEvent.
func newTestMatrixChannel(t *testing.T) *MatrixChannel {
	t.Helper()
	base := channels.NewBaseChannel("matrix", nil, nil, nil)
	base.SetRunning(true)
	return &MatrixChannel{BaseChannel: base}
}

// TestSendMedia_ResolveFailureReturnsError verifies the regression: when the
// only part's media ref cannot be resolved, SendMedia must return a non-nil
// error instead of silently reporting success. Before the fix, the per-part
// loop `continue`d past resolve failures with no sentCount tracking, so
// SendMedia unconditionally returned nil at the end regardless of how many
// (or how few) parts actually made it out.
func TestSendMedia_ResolveFailureReturnsError(t *testing.T) {
	ch := newTestMatrixChannel(t)
	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)

	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "!room:matrix.test",
		Parts: []bus.MediaPart{{
			Type: "image",
			Ref:  "media://does-not-exist",
		}},
	})

	require.Error(t, err, "SendMedia must report failure when every part fails to resolve")
	assert.Contains(t, err.Error(), "1 of 1 media parts failed to send")
	assert.ErrorIs(t, err, channels.ErrSendFailed,
		"an all-local-failure aggregate error must be permanent (not retried)")
}

// TestSendMedia_OpenFailureReturnsError covers the second swallowed path:
// the ref resolves to a local path, but the underlying file is gone by the
// time os.Open (via os.Stat first) runs. Before the fix, this also fell
// through to an unconditional `return nil`.
func TestSendMedia_OpenFailureReturnsError(t *testing.T) {
	ch := newTestMatrixChannel(t)
	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)

	tmpDir := t.TempDir()
	localPath := filepath.Join(tmpDir, "report.pdf")
	require.NoError(t, os.WriteFile(localPath, []byte("fake-pdf-content"), 0o644))

	ref, err := store.Store(localPath, media.MediaMeta{Filename: "report.pdf"}, "scope-1")
	require.NoError(t, err)

	// Remove the file after Store so Resolve still finds a mapping but
	// os.Stat/os.Open fail, exercising the stat/open-failure branches.
	require.NoError(t, os.Remove(localPath))

	err = ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "!room:matrix.test",
		Parts: []bus.MediaPart{{
			Type: "file",
			Ref:  ref,
		}},
	})

	require.Error(t, err, "SendMedia must report failure when every part fails to open")
	assert.Contains(t, err.Error(), "1 of 1 media parts failed to send")
	assert.ErrorIs(t, err, channels.ErrSendFailed,
		"an all-local-failure aggregate error must be permanent (not retried)")
}

// TestSendMedia_NoMediaStoreReturnsError guards the existing (already
// correct) fast-fail path so it stays covered alongside the new assertions
// above.
func TestSendMedia_NoMediaStoreReturnsError(t *testing.T) {
	base := channels.NewBaseChannel("matrix", nil, nil, nil)
	base.SetRunning(true)
	ch := &MatrixChannel{BaseChannel: base}

	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "!room:matrix.test",
		Parts: []bus.MediaPart{{
			Type: "image",
			Ref:  "media://whatever",
		}},
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, channels.ErrSendFailed)
}

// TestSendMedia_PartialFailureReturnsError is the FIX-2 review regression
// test proving the actual defect end-to-end: 2 of 3 parts genuinely succeed
// (real UploadMedia + SendMessageEvent calls against a mock homeserver) and
// 1 fails to resolve locally. Before the fix, sentCount(2) > 0 made the
// original `if sentCount == 0` guard silently return nil — the 2 delivered
// parts left the tool's pre-baked success text untouched and neither the
// user nor the LLM ever learned the third part was dropped.
func TestSendMedia_PartialFailureReturnsError(t *testing.T) {
	var uploadCount, sendCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/upload"):
			uploadCount.Add(1)
			_, _ = w.Write([]byte(`{"content_uri":"mxc://test/abc"}`))
		case strings.Contains(r.URL.Path, "/send/"):
			sendCount.Add(1)
			_, _ = w.Write([]byte(`{"event_id":"$abc"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := mautrix.NewClient(server.URL, id.UserID("@bot:test"), "test-token")
	require.NoError(t, err)

	base := channels.NewBaseChannel("matrix", nil, nil, nil)
	base.SetRunning(true)
	ch := &MatrixChannel{BaseChannel: base, client: client}

	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)

	tmpDir := t.TempDir()
	parts := make([]bus.MediaPart, 0, 3)
	for _, name := range []string{"a.png", "b.png"} {
		localPath := filepath.Join(tmpDir, name)
		require.NoError(t, os.WriteFile(localPath, []byte("fake-content"), 0o644))
		ref, storeErr := store.Store(localPath, media.MediaMeta{Filename: name, ContentType: "image/png"}, "scope-1")
		require.NoError(t, storeErr)
		parts = append(parts, bus.MediaPart{Type: "image", Ref: ref, Filename: name})
	}
	// Third part deliberately unresolvable.
	parts = append(parts, bus.MediaPart{Type: "image", Ref: "media://does-not-exist"})

	err = ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "!room:matrix.test",
		Parts:  parts,
	})

	require.Error(t, err, "SendMedia must report failure even though 2 of 3 parts genuinely succeeded")
	assert.Contains(t, err.Error(), "1 of 3 media parts failed to send")
	assert.ErrorIs(t, err, channels.ErrSendFailed,
		"must be classified permanent so sendMediaWithRetry does not re-deliver the 2 already-successful parts")
	assert.Equal(t, int32(2), uploadCount.Load(), "the 2 resolvable parts must still have been uploaded")
	assert.Equal(t, int32(2), sendCount.Load(), "the 2 resolvable parts must still have been sent as room messages")
}
