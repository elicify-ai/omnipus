package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
)

// These cases execute the same persistence operations as context relief and
// /clear. They are storage acceptance, not slash-command or browser UAT.
func lifecycleClearDivider(t *testing.T) {
	store, sid, _, _ := wiredModelRetentionSession(t)
	view, err := store.WindowView(context.Background(), sid)
	require.NoError(t, err)
	after := lifecycleClearedState(view.State)
	require.NoError(t, store.CommitWindow(context.Background(), sid, view.State, after))
	require.NoError(t, store.AppendTranscriptStrict(sid, TranscriptEntry{
		ID: "clear-lifecycle", Type: EntryTypeSystem, Role: "system", Content: ClearMarkerText,
		AgentID: "mia", Timestamp: time.Now().UTC(), ViewMembership: ViewMembershipChat,
	}))
	entries, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	found := false
	for _, entry := range entries {
		if entry.ID == "clear-lifecycle" {
			require.True(t, IsClearMarker(entry))
			found = true
		}
	}
	require.True(t, found, "the real clear-context divider was persisted")
	view, err = store.WindowView(context.Background(), sid)
	require.NoError(t, err)
	require.Empty(t, view.Live)
	require.Equal(t, 4, view.State.Skip)
	require.Equal(t, 4, view.State.ClearFloor)
	lifecycleAssertFreshHelperReport(t, store, sid, "clear-divider")
}

func lifecycleClearedState(before memory.WindowState) memory.WindowState {
	after := before.Clone()
	after.Skip, after.ClearFloor = after.Count, after.Count
	after.DropExpiryNote = true
	after.AnchorLine = nil
	after.Projection.Entries = memory.ProjectionSet{}
	after.Projection.SourceRunes = map[memory.ProjectionKey]int{}
	after.Projection.TranscriptAddr = map[memory.ProjectionKey]memory.RecordAddress{}
	return after
}

func lifecycleTrimWindow(t *testing.T) {
	store, sid, archive, rolled := wiredModelRetentionSession(t)
	before, err := os.ReadFile(rolled)
	require.NoError(t, err)
	view, err := store.WindowView(context.Background(), sid)
	require.NoError(t, err)
	after := view.State.Clone()
	after.Skip = 3
	require.NoError(t, store.CommitWindow(context.Background(), sid, view.State, after))
	retained, err := os.ReadFile(rolled)
	require.NoError(t, err)
	require.Equal(t, before, retained, "context trim is not physical archive compaction")
	n, err := archive.OrdinalCount()
	require.NoError(t, err)
	require.Equal(t, 4, n)
	lifecycleAssertFreshHelperReport(t, store, sid, "trim")
}

func lifecycleTrimAnchor(t *testing.T) {
	store, sid, _, _ := wiredModelRetentionSession(t)
	view, err := store.WindowView(context.Background(), sid)
	require.NoError(t, err)
	after := view.State.Clone()
	after.Skip = 3
	anchor := 2 // The day-one user remains explicitly required before Skip.
	after.AnchorLine = &anchor
	require.NoError(t, store.CommitWindow(context.Background(), sid, view.State, after))
	view, err = store.WindowView(context.Background(), sid)
	require.NoError(t, err)
	require.NotNil(t, view.Anchor)
	require.Equal(t, "day one user", view.Anchor.Message.Content)
	lifecycleAssertFreshHelperReport(t, store, sid, "trim-anchor")
}

func lifecycleRestoreClear(t *testing.T) {
	store, sid, _, _ := wiredModelRetentionSession(t)
	ctx := context.Background()
	view, err := store.WindowView(ctx, sid)
	require.NoError(t, err)
	cleared := lifecycleClearedState(view.State)
	require.NoError(t, store.CommitWindow(ctx, sid, view.State, cleared))
	before := view.State.Clone()
	before.RestoreExpiryNote = view.ExpiryNote
	cleared.DropExpiryNote = false
	require.NoError(t, store.RestoreWindow(ctx, sid, cleared, before), "undo of an uninstalled clear candidate")
	restored, err := store.WindowView(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, 0, restored.State.Skip)
	require.Equal(t, 0, restored.State.ClearFloor)
	require.Len(t, restored.Live, 4)
	lifecycleAssertFreshHelperReport(t, store, sid, "restore-clear")
}

func lifecycleRollback(t *testing.T) {
	store, sid, archive, _ := wiredModelRetentionSession(t)
	ctx := context.Background()
	start, err := store.WindowView(ctx, sid)
	require.NoError(t, err)
	aborted := lifecycleAppendUser(t, store, sid, "aborted message")
	require.NoError(t, store.RollbackWindow(ctx, sid, start.State))
	view, err := store.WindowView(ctx, sid)
	require.NoError(t, err)
	require.Len(t, view.Live, 4)
	require.Equal(t, []memory.ArchiveSpan{{Start: 4, End: 5}}, view.Excluded)
	rec, err := archive.ReadAt(aborted.Addr)
	require.NoError(t, err)
	require.Equal(t, "aborted message", rec.ModelMessage.Content, "rollback retains original bytes for recall")
	lifecycleAssertFreshHelperReport(t, store, sid, "rollback")
}

func lifecycleRollbackRetention(t *testing.T) {
	store := newUnifiedStoreForTest(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	archive, err := store.backend.store(meta.ID)
	require.NoError(t, err)
	ctx := context.Background()
	start, err := store.WindowView(ctx, meta.ID)
	require.NoError(t, err)
	atDay(archive, "2026-04-01")
	lifecycleAppendUser(t, store, meta.ID, "aborted old message")
	atDay(archive, "2026-04-10")
	lifecycleAppendUser(t, store, meta.ID, "aborted recent message")
	require.NoError(t, store.RollbackWindow(ctx, meta.ID, start.State))
	lifecycleAppendUser(t, store, meta.ID, "retained new message")
	rolled := filepath.Join(archive.dir(), "2026-04-01.jsonl")
	ageFileTo(t, rolled, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	removed, err := store.RetentionSweep(7)
	require.NoError(t, err)
	require.Equal(t, 1, removed)
	view, err := store.WindowView(ctx, meta.ID)
	require.NoError(t, err)
	require.Equal(t, 0, view.State.Skip, "exclusions, not a numerical prefix, define these non-live rows")
	require.Equal(t, []memory.ArchiveSpan{{Start: 0, End: 2}}, view.Excluded)
	require.Len(t, view.Live, 1)
	require.Equal(t, "retained new message", view.Live[0].Message.Content)
	lifecycleAssertFreshHelperReport(t, store, meta.ID, "rollback-retention")
}

func lifecycleProjection(t *testing.T) {
	store, sid, archive, rolled := wiredModelRetentionSession(t)
	before, err := os.ReadFile(rolled)
	require.NoError(t, err)
	pk := memory.ProjectionKey{ToolCallID: "call_0", ArchiveLine: 1}
	require.NoError(t, store.SetProjectionState(sid, pk, memory.ProjectionEmptied))
	require.Equal(t, memory.ProjectionEmptied, store.Projection(sid).Entries[pk])
	after, err := os.ReadFile(rolled)
	require.NoError(t, err)
	require.Equal(t, before, after, "projection changes request metadata, not the saved tool-result payload")
	n, err := archive.OrdinalCount()
	require.NoError(t, err)
	require.Equal(t, 4, n)
	lifecycleAssertFreshHelperReport(t, store, sid, "projection")
}

func lifecycleAppendUser(t *testing.T, store *UnifiedStore, sid, body string) ModelSlot {
	t.Helper()
	slot, _, err := store.AppendModelMessage(context.Background(), sid, ModelAppend{
		Message: providers.Message{Role: "user", Content: body}, ViewMembership: ViewMembershipBoth,
		Source: EntrySource{Kind: "user", Principal: "daniel"}, AgentID: "mia",
	})
	require.NoError(t, err)
	return slot
}

func lifecycleOffsetRepair(t *testing.T) {
	store, sid, archive, _ := wiredModelRetentionSession(t)
	rows, err := os.ReadFile(archive.ordinalRowsPath())
	require.NoError(t, err)
	require.NoError(t, os.Remove(archive.ordinalOffsetsPath()), "discard only a derived cache")
	n, err := archive.OrdinalCount() // Real automatic row-log repair.
	require.NoError(t, err)
	require.Equal(t, 4, n)
	after, err := os.ReadFile(archive.ordinalRowsPath())
	require.NoError(t, err)
	require.Equal(t, rows, after, "every published row survives derived-index rebuild")
	lifecycleAssertFreshHelperReport(t, store, sid, "offset-repair")
}

func lifecycleTornOrdinalRepair(t *testing.T) {
	store, sid, archive, _ := wiredModelRetentionSession(t)
	rows, err := os.ReadFile(archive.ordinalRowsPath())
	require.NoError(t, err)
	f, err := os.OpenFile(archive.ordinalRowsPath(), os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	_, writeErr := f.WriteString(`{"o":4,"a":`)
	closeErr := f.Close()
	require.NoError(t, writeErr)
	require.NoError(t, closeErr)
	n, err := archive.OrdinalCount() // Cuts the unframed unpublished tail only.
	require.NoError(t, err)
	require.Equal(t, 4, n)
	after, err := os.ReadFile(archive.ordinalRowsPath())
	require.NoError(t, err)
	require.Equal(t, rows, after)
	lifecycleAssertFreshHelperReport(t, store, sid, "torn-ordinal-repair")
}

func lifecycleStoreReopen(t *testing.T) {
	store, sid, _, _ := wiredModelRetentionSession(t)
	require.NoError(t, store.Close())
	reopened, err := NewUnifiedStore(store.BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	view, err := reopened.WindowView(context.Background(), sid)
	require.NoError(t, err)
	require.Len(t, view.Live, 4)
	lifecycleAssertFreshHelperReport(t, reopened, sid, "reopen")
}
