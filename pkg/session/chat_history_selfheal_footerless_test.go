package session

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Design §§1,2,4: valid canonical history beyond the catch-up cap heals on
// demand even if the disposable index has complete unpublished nodes without
// a transaction footer. This is not corruption of the saved conversation.
func TestChatHistoryRecovery_DemandHealsCompleteFooterlessNodesBeyondCatchupLimit(t *testing.T) {
	const pending = 65 // first suffix above the design's 64-fact bounded reconciler
	const total = 2 + pending
	logs := selfHealCaptureLogs(t)
	fixture := gate0SavedNodeTailFixture(t, pending)
	require.Len(t, fixture.addresses, total, "two saved rows plus exactly 65 accepted pending rows")
	gate0SetTailFault(t, fixture, "footerless")
	canonical := partitionBytes(t, fixture.store, fixture.sid)
	before := historyTree(t, historyIndexDir(fixture.store, fixture.sid))
	reopened, err := NewUnifiedStore(fixture.store.BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	reopened.stopStatsFlusher()
	require.Equal(t, before, historyTree(t, historyIndexDir(reopened, fixture.sid)), "boot must leave the saved checkpoint and footerless residue untouched")

	page, err := reopened.ReadChatHistoryPage(context.Background(), fixture.sid, historyOptions(100))
	require.NoError(t, err, "the first demand read must rebuild beyond the cap, not return catchup_limit for benign footerless residue")
	require.Len(t, page.Entries, total)
	require.Equal(t, total, page.PageInfo.RowCount)
	wantIDs := make([]string, total)
	for i, address := range fixture.addresses {
		wantIDs[i] = address.EntryID
		require.Equal(t, historyExpectedPosition(fixture.sid, address), page.Entries[i].Position, "every original canonical address retains its independent position")
	}
	require.Equal(t, wantIDs, historyIDs(page), "all 67 canonical rows in order, none lost or duplicated by replay")
	require.False(t, page.HasEarlier)
	require.False(t, page.HasLater)
	after := historyCheckpointOf(t, reopened, fixture.sid)
	require.NotEqual(t, fixture.checkpoint.File, after.File, "demand must perform a real rebuild, not silently widen the bounded reconciler")
	require.Equal(t, canonical, partitionBytes(t, reopened, fixture.sid), "healing footerless derived residue never rewrites canonical bytes")
	records := selfHealEvents(t, logs, fixture.sid, selfHealOKLog)
	require.Len(t, records, 1)
	selfHealRequireLogBase(t, records[0], fixture.sid, 1)
	require.Equal(t, "rebuild", records[0]["action"])
	require.Equal(t, int64(total), selfHealLogNumber(t, records[0], "records"))
	require.Empty(t, selfHealEvents(t, logs, fixture.sid, selfHealBadLog))
	for _, content := range []string{"row 1", "row 2", "durable suffix", "durable overflow item"} {
		require.NotContains(t, logs.text(), content, "the real fixture's canonical content must not appear in warnings")
	}

	stable := historyTree(t, historyIndexDir(reopened, fixture.sid))
	again := historyRead(t, reopened, fixture.sid, historyOptions(100))
	require.Equal(t, page.Entries, again.Entries)
	require.Equal(t, stable, historyTree(t, historyIndexDir(reopened, fixture.sid)), "a repeat demand read is an exact no-write fixed point")
	require.Len(t, selfHealEvents(t, logs, fixture.sid, selfHealOKLog), 1, "the same footerless residue must not trigger repeated rebuilds")
}
