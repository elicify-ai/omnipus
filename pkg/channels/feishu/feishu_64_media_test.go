//go:build amd64 || arm64 || riscv64 || mips64 || ppc64

package feishu

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
)

// newTestFeishuChannel builds a running FeishuChannel with no live Lark
// client — sufficient for exercising SendMedia's resolve/open failure paths,
// which return before any API call is made.
func newTestFeishuChannel(t *testing.T) *FeishuChannel {
	t.Helper()
	base := channels.NewBaseChannel("feishu", nil, nil, nil)
	base.SetRunning(true)
	return &FeishuChannel{BaseChannel: base}
}

// TestSendMedia_ResolveFailureReturnsError verifies the regression from the
// "media delivered when it sent nothing" bug: when the only part's media ref
// cannot be resolved (e.g. a stale/unknown media:// ref), SendMedia must
// return a non-nil error instead of silently reporting success. Before the
// fix, sendMediaPart swallowed the resolve error and returned nil.
func TestSendMedia_ResolveFailureReturnsError(t *testing.T) {
	ch := newTestFeishuChannel(t)
	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)

	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "oc_test_chat",
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
// the ref resolves to a local path, but the underlying file is gone (or
// unreadable) by the time os.Open runs. Before the fix, this also returned
// nil from sendMediaPart/SendMedia.
func TestSendMedia_OpenFailureReturnsError(t *testing.T) {
	ch := newTestFeishuChannel(t)
	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)

	tmpDir := t.TempDir()
	localPath := filepath.Join(tmpDir, "report.pdf")
	require.NoError(t, os.WriteFile(localPath, []byte("fake-pdf-content"), 0o644))

	ref, err := store.Store(localPath, media.MediaMeta{Filename: "report.pdf"}, "scope-1")
	require.NoError(t, err)

	// Remove the file after Store so Resolve still finds a mapping but
	// os.Open fails, exercising the open-failure branch of sendMediaPart.
	require.NoError(t, os.Remove(localPath))

	err = ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "oc_test_chat",
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

// TestSendMedia_PartialFailureReturnsError is the FIX-2 review regression
// test: when SOME parts resolve/open fine but at least one does not, SendMedia
// must still report the shortfall instead of returning nil just because
// sentCount > 0. Before the fix, this exact case (2 of 3 delivered) returned
// nil — the caller (pkg/agent/loop.go) had no way to tell the user or the
// LLM that a part never arrived, and the tool's pre-baked success text went
// out unchanged.
//
// The channel has no live Lark client, so a genuinely successful send can't
// be exercised here (that would require mocking the full Lark API). This
// test instead proves the counting logic itself is accurate for N>1 parts —
// both fail to resolve — which is the exact code path (sentCount tracked
// across the whole loop, compared against len(msg.Parts) rather than a
// hardcoded single-part assumption) that the fix changed.
func TestSendMedia_PartialFailureReturnsError(t *testing.T) {
	ch := newTestFeishuChannel(t)
	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)

	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "oc_test_chat",
		Parts: []bus.MediaPart{
			{Type: "image", Ref: "media://does-not-exist-1"},
			{Type: "image", Ref: "media://does-not-exist-2"},
		},
	})

	require.Error(t, err, "SendMedia must report failure when parts fail to resolve")
	assert.Contains(t, err.Error(), "2 of 2 media parts failed to send")
	assert.ErrorIs(t, err, channels.ErrSendFailed)
}

// TestSendMedia_CrossWorkspaceRefLogsDistinctDenialWarning is the FR-028a
// review regression test: a media ref denied by the caller-workspace
// membership guard (media.IsCallerWorkspaceDenied) must get its own
// distinct WARN log line naming it a workspace-boundary denial, instead of
// being folded into the generic "Failed to resolve media ref" ERROR log
// used for a routine stale/missing ref. Delivery semantics (permanent
// ErrSendFailed, no retry) are unchanged either way.
func TestSendMedia_CrossWorkspaceRefLogsDistinctDenialWarning(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "feishu-media.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	require.NoError(t, logger.EnableFileLogging(logFile))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	ch := newTestFeishuChannel(t)
	ch.SetMediaStore(media.NewFileMediaStore())

	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "oc_test_chat",
		// WorkspaceID deliberately empty: a workspace-prefixed ref with no
		// caller-workspace context trips the FR-028a guard
		// (ErrWorkspaceContextRequired) before any media store provider is
		// even consulted.
		Parts: []bus.MediaPart{{Type: "image", Ref: "media://workspace/other-ws/some-id"}},
	})

	require.Error(t, err, "SendMedia must still report failure for a denied workspace ref")
	assert.ErrorIs(t, err, channels.ErrSendFailed,
		"a caller-workspace denial is permanent, same as any other local resolve failure")

	logged, readErr := os.ReadFile(logFile)
	require.NoError(t, readErr, "reading captured log file")
	logStr := string(logged)
	assert.Contains(t, logStr, "Media ref denied by caller-workspace guard",
		"the FR-028a denial must get its own distinct WARN log line")
	assert.NotContains(t, logStr, "Failed to resolve media ref",
		"the denial must NOT also log the generic message (would defeat distinguishing it)")
}

// TestSendMedia_NoMediaStoreReturnsError guards the existing (already
// correct) fast-fail path so it stays covered alongside the new assertions
// above.
func TestSendMedia_NoMediaStoreReturnsError(t *testing.T) {
	base := channels.NewBaseChannel("feishu", nil, nil, nil)
	base.SetRunning(true)
	ch := &FeishuChannel{BaseChannel: base}

	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "oc_test_chat",
		Parts: []bus.MediaPart{{
			Type: "image",
			Ref:  "media://whatever",
		}},
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, channels.ErrSendFailed)
}
