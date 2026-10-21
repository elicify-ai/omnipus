package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Shared per-session repair publication interruption, invoked by the real
// standalone command. Batch/operator guards are tested in its own package.
func TestChatHistoryRepair_SharedPublicationCrashAndRepeat(t *testing.T) {
	for _, point := range []string{"lookups_staged", "generation_verified", "before_checkpoint_publish", "checkpoint_published"} {
		t.Run(point, func(t *testing.T) {
			s, sid := newEffectsSession(t)
			historyRows(t, s, sid, "base", 1)
			historyState(t, s, sid)
			historyUnpublishedArchive(t, s, sid, 1)
			checkpoint := filepath.Join(historyIndexDir(s, sid), "checkpoint.json")
			old, err := os.ReadFile(checkpoint)
			require.NoError(t, err)
			require.NoError(t, s.Close())
			historyRunCrash(t, s.BaseDir(), sid, point, 1, 86, "repair")
			after, err := os.ReadFile(checkpoint)
			require.NoError(t, err)
			if point == "checkpoint_published" {
				require.NotEqual(t, string(old), string(after))
			} else {
				require.Equal(t, string(old), string(after), "old valid generation remains until complete replacement")
			}
			dir := filepath.Join(s.BaseDir(), sid)
			build, err := PrepareChatHistoryIndex(context.Background(), dir, ChatHistoryIndexBuildOptions{})
			require.NoError(t, err)
			require.NoError(t, build.Publish(context.Background()))
			require.NoError(t, build.Close())
			require.NoError(t, VerifyChatHistoryIndex(context.Background(), dir))
			stable := historyTree(t, historyIndexDir(s, sid))
			again, err := PrepareChatHistoryIndex(context.Background(), dir, ChatHistoryIndexBuildOptions{})
			require.NoError(t, err)
			require.Equal(t, "unchanged", again.Summary().Action)
			require.NoError(t, again.Publish(context.Background()))
			require.NoError(t, again.Close())
			require.Equal(t, stable, historyTree(t, historyIndexDir(s, sid)))
			reopened, err := NewUnifiedStore(s.BaseDir())
			require.NoError(t, err)
			defer func() { require.NoError(t, reopened.Close()) }()
			require.Equal(t, []string{"base-000001", "suffix-000"}, historyIDs(historyRead(t, reopened, sid, historyOptions(40))))
		})
	}
}
