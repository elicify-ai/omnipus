package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// D1a + explicit M2 requirement: complete checked nodes beyond MutationEnd
// without a commit are provably unpublished, not framed corruption. The64
// cap counts unique canonical mutations; a mutation may write MANY tree nodes.
func TestChatHistoryRecovery_CompleteFooterlessNodesAreRedoable(t *testing.T) {
	for _, pending := range []int{0, 1, 63, 64, 65} {
		t.Run(fmt.Sprintf("footerless_pending_%d", pending), func(t *testing.T) {
			fixture := gate0SavedNodeTailFixture(t, pending)
			if pending > 0 {
				gate0SetTailFault(t, fixture, "footerless")
			}
			gate0ReadSavedNodeTail(t, fixture, pending, "footerless")
		})
	}
	for _, kind := range []string{"checksum_damage", "wrong_commit_owner", "committed"} {
		t.Run(kind, func(t *testing.T) {
			fixture := gate0SavedNodeTailFixture(t, 1)
			gate0SetTailFault(t, fixture, kind)
			gate0ReadSavedNodeTail(t, fixture, 1, kind)
		})
	}
}

func gate0ReadSavedNodeTail(t *testing.T, fixture gate0SavedNodeTail, pending int, kind string) {
	t.Helper()
	canonical := partitionBytes(t, fixture.store, fixture.sid)
	root := fixture.store.BaseDir()
	checkpointPath := filepath.Join(root, fixture.sid, ChatHistoryIndexDirectory, "checkpoint.json")
	checkpointBefore, err := os.ReadFile(checkpointPath)
	require.NoError(t, err)
	fileBefore, err := os.ReadFile(fixture.path)
	require.NoError(t, err)
	require.Equal(t, fixture.prefix, fileBefore[:len(fixture.prefix)], "fixture fault never touches the saved published prefix")
	reopened, err := NewUnifiedStore(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	read := func() (ChatHistoryPage, error) {
		if pending > 64 {
			// Retargeted, oracle unchanged: selfheal-design.md §1 keeps
			// boot/sweep reconciliation bounded, while demand may rebuild.
			_, reconcileErr := reopened.reconcileChatHistoryIndex(context.Background(), fixture.sid)
			return ChatHistoryPage{}, reconcileErr
		}
		return reopened.ReadChatHistoryPage(context.Background(), fixture.sid, historyOptions(100))
	}
	page, readErr := read()
	refusal := ""
	if pending > 64 {
		refusal = "catchup_limit"
	} else if kind == "checksum_damage" || kind == "wrong_commit_owner" {
		refusal = "index_inconsistent"
	}
	if refusal != "" {
		historyRequireUnavailable(t, readErr, refusal)
		_, retryErr := read()
		historyRequireUnavailable(t, retryErr, refusal)
		after, checkpointReadErr := os.ReadFile(checkpointPath)
		require.NoError(t, checkpointReadErr)
		require.Equal(t, checkpointBefore, after, "no partial candidate root publication or repeated64-at-a-time progress")
		afterFile, refusedReadErr := os.ReadFile(fixture.path)
		require.NoError(t, refusedReadErr)
		require.Equal(t, fileBefore, afterFile, "corrupt/foreign-committed/excess candidate is not silently discarded")
	} else if assert.NoError(t, readErr, "M2: complete valid footerless nodes within64 must not force operator repair") {
		require.Len(t, page.Entries, len(fixture.addresses))
		require.EqualValues(t, 2+pending, page.PageInfo.RowCount)
		for i, address := range fixture.addresses {
			require.Equal(t, address.EntryID, page.Entries[i].Entry.ID, "exact canonical order, no duplicate replay")
			require.Equal(t, historyExpectedPosition(fixture.sid, address), page.Entries[i].Position, "independent D2 token remains stable")
		}
		stable := historyTree(t, historyIndexDir(reopened, fixture.sid))
		again := historyRead(t, reopened, fixture.sid, historyOptions(100))
		require.Equal(t, page.Entries, again.Entries)
		require.Equal(t, stable, historyTree(t, historyIndexDir(reopened, fixture.sid)), "next read is an exact no-write fixed point")
		if kind == "committed" || pending == 0 {
			afterFile, committedReadErr := os.ReadFile(fixture.path)
			require.NoError(t, committedReadErr)
			require.Equal(t, fileBefore, afterFile, "complete committed transaction/zero delta is reused intact, never trimmed/re-encoded")
		}
	}
	afterFile, err := os.ReadFile(fixture.path)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(afterFile), len(fixture.prefix))
	require.Equal(t, fixture.prefix, afterFile[:len(fixture.prefix)], "saved published prefix bytes never change")
	require.Equal(t, canonical, partitionBytes(t, reopened, fixture.sid), "no canonical byte trimmed or rewritten even on refusal")
}
