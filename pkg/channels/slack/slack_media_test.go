package slack

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

// newTestSlackChannel builds a running SlackChannel with no live slack.Client
// — sufficient for exercising SendMedia's resolve-failure paths, which
// `continue` past the client entirely and never reach UploadFileContext.
//
// A genuinely successful send cannot be exercised in this package today: the
// slack-go v0.24 UploadFileContext (called by SendMedia) requires
// UploadFileParameters.FileSize to be set, but SendMedia's call site never
// sets it — a separate, pre-existing defect outside this fix's scope (not
// one of the FIX-2/FIX-3 review items) that means every real Slack media
// send currently fails immediately with "FileSize cannot be 0" before any
// HTTP call is made, retried needlessly by sendMediaWithRetry as if
// transient. Flagged for follow-up; not fixed here.
func newTestSlackChannel(t *testing.T) *SlackChannel {
	t.Helper()
	base := channels.NewBaseChannel("slack", nil, nil, nil)
	base.SetRunning(true)
	return &SlackChannel{BaseChannel: base}
}

// TestSendMedia_ResolveFailureReturnsError mirrors the Feishu/Matrix/Telegram
// regression coverage: when the only part's media ref cannot be resolved,
// SendMedia must return a non-nil error wrapping channels.ErrSendFailed
// (permanent — not retried), not silently report success.
func TestSendMedia_ResolveFailureReturnsError(t *testing.T) {
	ch := newTestSlackChannel(t)
	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)

	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "C123456",
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

// TestSendMedia_PartialFailureReturnsError is the FIX-2 review regression
// test: a multi-part message where every part fails to resolve must report
// the accurate "N of M" count, proving the counting logic added by the fix
// (sentCount compared against len(msg.Parts) across the whole loop) is not
// hardcoded to the single-part case. See newTestSlackChannel's doc comment
// for why a genuinely MIXED outcome (some parts truly delivered) cannot be
// exercised in this package without first fixing the unrelated FileSize bug.
func TestSendMedia_PartialFailureReturnsError(t *testing.T) {
	ch := newTestSlackChannel(t)
	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)

	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "C123456",
		Parts: []bus.MediaPart{
			{Type: "image", Ref: "media://does-not-exist-1"},
			{Type: "image", Ref: "media://does-not-exist-2"},
		},
	})

	require.Error(t, err, "SendMedia must report failure when parts fail to resolve")
	assert.Contains(t, err.Error(), "2 of 2 media parts failed to send")
	assert.ErrorIs(t, err, channels.ErrSendFailed)
}

// TestSendMedia_NoMediaStoreReturnsError guards the existing (already
// correct) fast-fail path so it stays covered alongside the new assertions
// above.
//
// Note: Slack's SendMedia, unlike Feishu/Matrix/Telegram, never opens the
// local file itself — slack-go's UploadFileContext takes a path and opens it
// internally. A missing/unreadable file after a successful Resolve therefore
// surfaces as an UploadFileContext error (a genuine send/upload failure),
// not as a locally-skipped part — which is why this package has no
// "OpenFailureReturnsError" counterpart to Feishu/Matrix's tests.
// TestSendMedia_CrossWorkspaceRefLogsDistinctDenialWarning is the FR-028a
// review regression test: a media ref denied by the caller-workspace
// membership guard (media.IsCallerWorkspaceDenied) must get its own
// distinct WARN log line naming it a workspace-boundary denial, instead of
// being folded into the generic "Failed to resolve media ref" ERROR log
// used for a routine stale/missing ref. Delivery semantics (permanent
// ErrSendFailed, no retry) are unchanged either way.
func TestSendMedia_CrossWorkspaceRefLogsDistinctDenialWarning(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "slack-media.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	require.NoError(t, logger.EnableFileLogging(logFile))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	ch := newTestSlackChannel(t)
	ch.SetMediaStore(media.NewFileMediaStore())

	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "C123456",
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

func TestSendMedia_NoMediaStoreReturnsError(t *testing.T) {
	base := channels.NewBaseChannel("slack", nil, nil, nil)
	base.SetRunning(true)
	ch := &SlackChannel{BaseChannel: base}

	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "C123456",
		Parts: []bus.MediaPart{{
			Type: "image",
			Ref:  "media://whatever",
		}},
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, channels.ErrSendFailed)
}
