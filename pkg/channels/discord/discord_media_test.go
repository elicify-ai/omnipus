package discord

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

// newTestDiscordChannel builds a running DiscordChannel with no live
// discordgo.Session — sufficient for exercising SendMedia's resolve-failure
// path, which `continue`s past the session entirely and never reaches
// ChannelMessageSendComplex.
func newTestDiscordChannel(t *testing.T) *DiscordChannel {
	t.Helper()
	base := channels.NewBaseChannel("discord", nil, nil, nil)
	base.SetRunning(true)
	return &DiscordChannel{BaseChannel: base}
}

// TestSendMedia_CrossWorkspaceRefLogsDistinctDenialWarning is the FR-028a
// review regression test: a media ref denied by the caller-workspace
// membership guard (media.IsCallerWorkspaceDenied) must get its own
// distinct WARN log line naming it a workspace-boundary denial, instead of
// being folded into the generic "Failed to resolve media ref" ERROR log
// used for a routine stale/missing ref. Discord's per-part resolve loop only
// `continue`s past a failed part (never a hard return); with a single part
// and no other successes, SendMedia's existing all-failed guard
// ("all %d media files failed to load") still fires — delivery semantics
// are unchanged, and the point of this test is that the distinguishing log
// line fires alongside it.
func TestSendMedia_CrossWorkspaceRefLogsDistinctDenialWarning(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "discord-media.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	require.NoError(t, logger.EnableFileLogging(logFile))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	ch := newTestDiscordChannel(t)
	ch.SetMediaStore(media.NewFileMediaStore())

	err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "123456789012345678",
		// WorkspaceID deliberately empty: a workspace-prefixed ref with no
		// caller-workspace context trips the FR-028a guard
		// (ErrWorkspaceContextRequired) before any media store provider is
		// even consulted.
		Parts: []bus.MediaPart{{Type: "image", Ref: "media://workspace/other-ws/some-id"}},
	})
	require.Error(t, err, "SendMedia must still report failure when every part fails to load")
	assert.Contains(t, err.Error(), "all 1 media files failed to load")

	logged, readErr := os.ReadFile(logFile)
	require.NoError(t, readErr, "reading captured log file")
	logStr := string(logged)
	assert.Contains(t, logStr, "Media ref denied by caller-workspace guard",
		"the FR-028a denial must get its own distinct WARN log line")
	assert.NotContains(t, logStr, "Failed to resolve media ref",
		"the denial must NOT also log the generic message (would defeat distinguishing it)")
}
