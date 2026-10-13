//go:build goolm && stdjson

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// seedWindowHistory fills an EMPTY session with msgs through the store's own append
// seam, in order — what the retired first-fill SetHistory fixture did. A nil or
// empty history is a no-op.
func seedWindowHistory(store session.SessionStore, key string, msgs []providers.Message) {
	for _, m := range msgs {
		store.AddFullMessage(key, m)
	}
}

// truncateWindowTo keeps only the last keepLast live messages by moving the window
// start with one compare-and-set commit (the retired TruncateHistory fixture's
// effect): no archive byte changes, projection rows for evicted slots are pruned.
func truncateWindowTo(t testing.TB, store session.SessionStore, key string, keepLast int) {
	t.Helper()
	cw, ok := store.(session.ContextWindowStore)
	require.True(t, ok, "the session store supports the checkpoint seam")
	ctx := context.Background()
	view, err := cw.WindowView(ctx, key)
	require.NoError(t, err)
	after := view.State.Clone()
	if keepLast <= 0 {
		after.Skip = after.Count
	} else if effective := after.Count - after.Skip; keepLast < effective {
		after.Skip = after.Count - keepLast
	}
	for k := range after.Projection.Entries {
		if k.ArchiveLine < after.Skip {
			delete(after.Projection.Entries, k)
			delete(after.Projection.SourceRunes, k)
		}
	}
	for k := range after.Projection.TranscriptAddr {
		if k.ArchiveLine < after.Skip {
			delete(after.Projection.TranscriptAddr, k)
		}
	}
	require.NoError(t, cw.CommitWindow(ctx, key, view.State, after))
}
