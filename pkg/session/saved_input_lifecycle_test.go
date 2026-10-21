package session

import (
	"context"
	"io/fs"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// PTA-05 / SFH-R5-01: intentional expiry leaves historical ordinal rows, not
// damaged live content. The real retention entry point must allow a new input.
func TestSavedInputStorageLifecycleMatrix(t *testing.T) {
	t.Run("retention_two_day", func(t *testing.T) {
		store, sid, archive, rolled := wiredModelRetentionSession(t)
		before, err := archive.OrdinalCount()
		require.NoError(t, err)
		require.Equal(t, 4, before)
		removed, err := store.RetentionSweep(7)
		require.NoError(t, err)
		require.Equal(t, 1, removed)
		_, err = os.Stat(rolled)
		require.ErrorIs(t, err, fs.ErrNotExist, "real retention removed the old partition")
		after, err := archive.OrdinalCount()
		require.NoError(t, err)
		require.Equal(t, before, after, "historical publication rows deliberately survive expiry")
		view, err := store.WindowView(context.Background(), sid)
		require.NoError(t, err, "retention left a healthy retained conversation")
		messages, ordinals := view.History()
		require.Len(t, messages, 1)
		require.Equal(t, []int{3}, ordinals)
		require.Equal(t, "day ten user", messages[0].Content)

		lifecycleAssertFreshHelperReport(t, store, sid, "retention")
	})
	t.Run("retention_expired_pending", lifecycleExpiredPending)
	t.Run("retention_live_pending_survives", lifecycleLivePending)
	t.Run("retention_all_old_model_content", lifecycleAllModelExpired)
	t.Run("retention_disabled", lifecycleRetentionDisabled)
	t.Run("retention_cutoff_equal", lifecycleRetentionBoundary)
	t.Run("clear_context_divider", lifecycleClearDivider)
	t.Run("trim_context", lifecycleTrimWindow)
	t.Run("trim_with_required_anchor", lifecycleTrimAnchor)
	t.Run("restore_after_clear", lifecycleRestoreClear)
	t.Run("rollback_cancelled_turn", lifecycleRollback)
	t.Run("rollback_then_retention", lifecycleRollbackRetention)
	t.Run("projection_metadata", lifecycleProjection)
	t.Run("derived_offset_repair", lifecycleOffsetRepair)
	t.Run("unpublished_ordinal_tail_repair", lifecycleTornOrdinalRepair)
	t.Run("clean_store_reopen", lifecycleStoreReopen)
	t.Run("retention_retires_gone_anchor", lifecycleRetainedAnchorExpires)
	t.Run("delete_session_then_new_recipient", lifecycleDeleteAndNew)
	t.Run("retire_last_pending_index", lifecycleRetireLastPending)
	t.Run("move_per_agent_store", lifecycleMoveAgentStore)
	t.Run("normalize_legacy_chat", lifecycleNormalizeChat)
	t.Run("convert_legacy_model_archive", lifecycleConvertModelArchive)
	t.Run("convert_flat_source_new_report_only", lifecycleConvertFlatSource)
	t.Run("settle_tool_call", lifecycleSettleToolCall)
	t.Run("project_tool_call", lifecycleProjectToolCall)
	t.Run("retract_tool_projection", lifecycleRetractToolCall)
	t.Run("consumed_placement_expires_source_survives", lifecycleConsumedPlacementExpires)
}

func lifecycleAssertFreshHelperReport(t *testing.T, store *UnifiedStore, sid, operation string) {
	t.Helper()
	ctx := context.Background()
	id := "saved-input-new-after-" + operation
	body := "genuinely new helper report after " + operation
	addr, err := store.SaveChatInput(sid, id, "mia", body)
	require.NoError(t, err, "a normal lifecycle operation must not block a genuinely new report")
	lookedUp, placed, err := store.SavedChatInput(sid, id)
	require.NoError(t, err)
	require.Equal(t, addr, lookedUp)
	require.False(t, placed)
	_, _, err = store.PlaceSavedInput(ctx, sid, addr)
	require.NoError(t, err)
	lookedUp, placed, err = store.SavedChatInput(sid, id)
	require.NoError(t, err)
	require.Equal(t, addr, lookedUp)
	require.True(t, placed)
	pending, err := store.PendingChatInputs(ctx, sid)
	require.NoError(t, err)
	for _, input := range pending {
		require.NotEqual(t, id, input.ID, "new input is consumed, not queued again")
	}
	view, err := store.WindowView(ctx, sid)
	require.NoError(t, err)
	messages, _ := view.History()
	occurrences := 0
	for _, message := range messages {
		if message.Content == body {
			occurrences++
		}
	}
	require.Equal(t, 1, occurrences)
	retried, err := store.SaveChatInput(sid, id, "mia", body)
	require.NoError(t, err, "same fresh input remains idempotent after placement")
	require.Equal(t, addr, retried)
}
