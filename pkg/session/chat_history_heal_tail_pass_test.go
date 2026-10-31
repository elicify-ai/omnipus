package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Architect tail ruling + detect-only clarification: an overflow refusal may
// detect damage, but must not trim benign residue before the 64-fact bound.
func TestChatHistoryHeal_DetectOnlyTailCheckPreservesOverflowResidue(t *testing.T) {
	logs := selfHealCaptureLogs(t)
	f := newSelfHealFixture(t)
	cp := historyCheckpointOf(t, f.store, f.sid)
	path := filepath.Join(historyIndexDir(f.store, f.sid), "generations", cp.File)
	prefix, err := os.ReadFile(path)
	require.NoError(t, err)
	require.EqualValues(t, cp.MutationEnd, len(prefix))
	selfHealPlantDerivedTail(t, path, cp.MutationEnd, "torn_unpublished_tail")
	torn, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Greater(t, len(torn), len(prefix), "positive control: the torn suffix really exists")
	require.Equal(t, prefix, torn[:len(prefix)])
	before := historyTree(t, historyIndexDir(f.store, f.sid))
	canonical := partitionBytes(t, f.store, f.sid)
	oldPublication := chatHistoryPublicationHook
	trimCalls := 0
	chatHistoryPublicationHook = func(e chatHistoryPublicationEvent) error {
		if e.SessionID == f.sid && (e.Point == "before_torn_tail_trim" || e.Point == "torn_tail_trimmed") {
			trimCalls++
		}
		if oldPublication != nil {
			return oldPublication(e)
		}
		return nil
	}
	t.Cleanup(func() { chatHistoryPublicationHook = oldPublication })
	for check := 0; check < 2; check++ {
		_, err = f.store.reconcileChatHistoryIndex(context.Background(), f.sid)
		selfHealRequireUnavailable(t, err, "catchup_limit")
		require.Nil(t, errors.Unwrap(err))
		after, readErr := os.ReadFile(path)
		require.NoError(t, readErr)
		require.Equal(t, torn, after, "detect-only overflow must leave every .chi byte intact, including crash residue")
		require.Equal(t, before, historyTree(t, historyIndexDir(f.store, f.sid)), "no writes before the bound, even on repeat")
		require.Zero(t, trimCalls)
		require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealOKLog))
		require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealBadLog))
	}
	builds := selfHealObserveBuilds(t, f.sid)
	oldRead := chatHistoryReadHook
	inspected := 0
	chatHistoryReadHook = func(ctx context.Context, e chatHistoryReadEvent) error {
		if e.SessionID == f.sid && e.Stream == "archive" && e.Purpose == "suffix" && e.Phase == "record_opened" && e.Address != nil && e.Address.ByteOffset == 0 {
			body, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			require.Equal(t, torn, body, "the demand-path detect pass must also leave the old .chi intact before rebuilding")
			inspected++
		}
		return oldRead(ctx, e)
	}
	t.Cleanup(func() { chatHistoryReadHook = oldRead })
	require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid))
	require.Equal(t, 1, inspected, "positive control: inspected actual old bytes at the full-builder I/O boundary")
	require.EqualValues(t, 1, builds.Load())
	require.Zero(t, trimCalls, "a full rebuild replaces the generation; no eager trim of its old residue")
	selfHealRequireIndexed(t, f)
	selfHealRequirePages(t, f)
	require.Equal(t, canonical, partitionBytes(t, f.store, f.sid))
	records := selfHealEvents(t, logs, f.sid, selfHealOKLog)
	require.Len(t, records, 1)
	selfHealRequireLogBase(t, records[0], f.sid, 1)
	require.Equal(t, "rebuild", records[0]["action"])
	require.Equal(t, int64(1+selfHealBacklog), selfHealLogNumber(t, records[0], "records"))
	require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealBadLog))
	stable := historyTree(t, historyIndexDir(f.store, f.sid))
	require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid))
	require.Equal(t, stable, historyTree(t, historyIndexDir(f.store, f.sid)))
	selfHealRequirePrivateLogs(t, logs, f)
}

// A valid saved footer exists at the first pass. A real suffix read then adds
// torn residue. The later discarding pass must clear hasSaved after trimming;
// reusing the first pass's saved footer would reference nodes it just removed.
func TestChatHistoryHeal_WithinBoundTailPassUsesFreshSavedState(t *testing.T) {
	for _, pending := range []int{1, 64} { // first fact and the inclusive online bound
		for _, torn := range []bool{false, true} {
			t.Run(fmt.Sprintf("pending_%d/torn_between_passes_%t", pending, torn), func(t *testing.T) {
				healFixFreshTailPass(t, pending, torn)
			})
		}
	}
}

func healFixFreshTailPass(t *testing.T, pending int, torn bool) {
	t.Helper()
	f := healFixLiveSavedFooter(t, pending)
	before, err := os.ReadFile(f.path)
	require.NoError(t, err)
	canonical := partitionBytes(t, f.store, f.sid)
	oldRead, oldPublication := chatHistoryReadHook, chatHistoryPublicationHook
	planted, beforeTrim, afterTrim := 0, 0, 0
	chatHistoryReadHook = func(ctx context.Context, e chatHistoryReadEvent) error {
		if torn && planted == 0 && e.SessionID == f.sid && e.Scope == "recovery" && e.Stream == "archive" && e.Purpose == "suffix" && e.Phase == "record_read" && e.Address != nil && e.Address.PartitionKey == f.addresses[2].PartitionKey && e.Address.ByteOffset == f.addresses[2].ByteOffset {
			// record_opened has only partition/offset. The real record_read
			// event carries the decoded ID as well, still before the tail pass.
			require.Equal(t, f.addresses[2], *e.Address, "positive control: the barrier read the exact first pending canonical record")
			body, readErr := os.ReadFile(f.path)
			require.NoError(t, readErr)
			require.Equal(t, before, body, "the initially valid saved transaction is still physically present")
			selfHealPlantDerivedTail(t, f.path, int64(len(before)), "torn_unpublished_tail")
			planted++
		}
		if oldRead != nil {
			return oldRead(ctx, e)
		}
		return nil
	}
	chatHistoryPublicationHook = func(e chatHistoryPublicationEvent) error {
		if e.SessionID == f.sid {
			switch e.Point {
			case "before_torn_tail_trim":
				beforeTrim++
				require.Equal(t, 1, planted, "only residue added after the early pass may need discarding")
			case "torn_tail_trimmed":
				afterTrim++
				body, readErr := os.ReadFile(f.path)
				require.NoError(t, readErr)
				require.Equal(t, f.prefix, body, "the saved footer's nodes were really removed; stale saved-state reuse is unsafe")
			}
		}
		if oldPublication != nil {
			return oldPublication(e)
		}
		return nil
	}
	t.Cleanup(func() { chatHistoryReadHook, chatHistoryPublicationHook = oldRead, oldPublication })
	result, err := f.store.reconcileChatHistoryIndex(context.Background(), f.sid)
	require.NoError(t, err, "bounded recovery must use the later pass's own saved/hasSaved after truncating the real tail")
	require.Equal(t, pending, result.ObservedItems)
	require.Equal(t, pending, result.AppliedItems)
	require.True(t, result.Published)
	wantTrim := 0
	if torn {
		wantTrim = 1
	}
	require.Equal(t, wantTrim, planted)
	require.Equal(t, wantTrim, beforeTrim, "one discarding pass, not an eager detection trim")
	require.Equal(t, wantTrim, afterTrim)
	after, err := os.ReadFile(f.path)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(after), len(f.prefix))
	require.Equal(t, f.prefix, after[:len(f.prefix)], "published prefix must never change")
	cp := historyCheckpointOf(t, f.store, f.sid)
	require.Equal(t, f.checkpoint.File, cp.File, "within-bound recovery uses the original generation file, not a rebuild")
	if !torn {
		require.Equal(t, before, after, "an intact saved transaction is reused byte-for-byte, not re-encoded")
		require.Equal(t, f.footer.Generation, cp.Generation)
	}
	page := historyRead(t, f.store, f.sid, historyOptions(100))
	require.Len(t, page.Entries, 2+pending)
	require.Equal(t, 2+pending, page.PageInfo.RowCount)
	for i, address := range f.addresses {
		require.Equal(t, address.EntryID, page.Entries[i].Entry.ID, "exact canonical order; no stale-footer loss or duplicate replay")
		require.Equal(t, historyExpectedPosition(f.sid, address), page.Entries[i].Position)
	}
	healFixRequireAddresses(t, f.store, f.sid, f.addresses)
	require.Equal(t, canonical, partitionBytes(t, f.store, f.sid))
	stable := historyTree(t, historyIndexDir(f.store, f.sid))
	again := historyRead(t, f.store, f.sid, historyOptions(100))
	require.Equal(t, page.Entries, again.Entries)
	require.Equal(t, stable, historyTree(t, historyIndexDir(f.store, f.sid)), "the correctly recovered tail is a no-write fixed point")
}

// Same real node/footer producer as the existing crash fixture, kept LIVE so a
// constructor's boot reconcile cannot consume the saved footer before the test.
func healFixLiveSavedFooter(t *testing.T, pending int) gate0SavedNodeTail {
	t.Helper()
	s, sid := newEffectsSession(t)
	s.stopStatsFlusher()
	addresses := historyRows(t, s, sid, "base", 2)
	cp := historyCheckpointOf(t, s, sid)
	path := filepath.Join(historyIndexDir(s, sid), "generations", cp.File)
	prefix, err := os.ReadFile(path)
	require.NoError(t, err)
	require.EqualValues(t, cp.MutationEnd, len(prefix))
	addresses = append(addresses, historyUnpublishedArchive(t, s, sid, pending)...)
	old := chatHistoryPublicationHook
	injected := errors.New("test: stop real saved-footer transaction before checkpoint")
	calls := 0
	chatHistoryPublicationHook = func(e chatHistoryPublicationEvent) error {
		if e.SessionID == sid && e.Point == "mutation_written" {
			calls++
			require.Equal(t, pending, e.Item)
			return injected
		}
		if old != nil {
			return old(e)
		}
		return nil
	}
	t.Cleanup(func() { chatHistoryPublicationHook = old })
	_, err = s.reconcileChatHistoryIndex(context.Background(), sid)
	chatHistoryPublicationHook = old
	require.ErrorIs(t, err, injected)
	require.Equal(t, 1, calls, "positive control: common writer reached the real complete-node/footer boundary")
	require.Equal(t, cp, historyCheckpointOf(t, s, sid), "the saved checkpoint has not been installed")
	footerAt, footer := gate0InspectRealNodeTail(t, path, cp.MutationEnd)
	return gate0SavedNodeTail{store: s, sid: sid, path: path, checkpoint: cp, prefix: prefix, addresses: addresses, footerAt: footerAt, footer: footer}
}
