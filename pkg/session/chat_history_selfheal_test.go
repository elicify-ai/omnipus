package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Design §5.1 (including its demand-page variant) and §4 warning contract.
func TestChatHistorySelfHeal_RenataRecoversOnDemandAndNextAppendStaysIndexed(t *testing.T) {
	t.Run("readiness_and_next_append", func(t *testing.T) {
		logs := selfHealCaptureLogs(t)
		f := newSelfHealFixture(t)
		canonical := partitionBytes(t, f.store, f.sid)
		require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid),
			"Renata's 213-record backlog must heal on this one readiness call, not remain catchup_limit")
		selfHealRequireIndexed(t, f)
		require.Equal(t, canonical, partitionBytes(t, f.store, f.sid), "healing only replaces derived files")
		before := historyCheckpointOf(t, f.store, f.sid)
		a, err := f.store.AppendTranscriptAddressed(f.sid, historyUserEntry("selfheal-after", selfHealBody, 0))
		require.NoError(t, err)
		f.addresses = append(f.addresses, a)
		require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "a successful heal must not immediately re-break")
		after := historyCheckpointOf(t, f.store, f.sid)
		require.Greater(t, after.ArchiveEnds[historyLaterDay], before.ArchiveEnds[historyLaterDay])
		selfHealRequireIndexed(t, f)
		require.Len(t, selfHealEvents(t, logs, f.sid, selfHealOKLog), 1, "one rebuild, not a rebuild on every read or append")
		selfHealRequirePrivateLogs(t, logs, f)
	})
	t.Run("page_demand_without_prior_readiness", func(t *testing.T) {
		f := newSelfHealFixture(t)
		selfHealRequirePages(t, f)
		selfHealRequireIndexed(t, f)
	})
	t.Run("warning_fields_and_no_content", func(t *testing.T) {
		logs := selfHealCaptureLogs(t)
		f := newSelfHealFixture(t)
		bytes := selfHealSourceSize(t, f)
		assert.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "the warning must describe a completed heal")
		records := selfHealEvents(t, logs, f.sid, selfHealOKLog)
		require.Len(t, records, 1, "successful demand heal must emit exactly one warning-level rebuild record")
		record := records[0]
		selfHealRequireLogBase(t, record, f.sid, 1)
		require.Equal(t, "rebuild", record["action"])
		require.Equal(t, int64(1+selfHealBacklog), selfHealLogNumber(t, record, "records"), "records is all 214 archive records, not just the unindexed 213")
		require.Equal(t, int64(1+selfHealBacklog), selfHealLogNumber(t, record, "source_groups"))
		require.Equal(t, int64(2), selfHealLogNumber(t, record, "partitions"), "current and the empty earlier day are both inventoried")
		require.Equal(t, bytes, selfHealLogNumber(t, record, "bytes"))
		require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealBadLog))
		selfHealRequirePrivateLogs(t, logs, f)
	})
}

// Design §5.2: this is the NewUnifiedStore boot-reconcile boundary named by
// the design, not an assertion that a complete gateway process was exercised.
func TestChatHistorySelfHeal_BootDoesNotRebuildButFirstDemandDoes(t *testing.T) {
	logs := selfHealCaptureLogs(t)
	f := newSelfHealFixture(t)
	require.NoError(t, f.store.Close())
	before := historyTree(t, historyIndexDir(f.store, f.sid))
	builds := selfHealObserveBuilds(t, f.sid)
	restarted, err := NewUnifiedStore(f.store.BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restarted.Close()) })
	restarted.stopStatsFlusher()
	f.store = restarted
	require.Equal(t, before, historyTree(t, historyIndexDir(restarted, f.sid)), "store/gateway startup must not rebuild any index")
	require.Zero(t, builds.Load(), "boot is not a full-build trigger")
	require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealOKLog))
	require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealBadLog))
	t.Log("control: boot left the index byte-identical and performed zero rebuilds")
	require.NoError(t, restarted.CheckChatHistoryReadiness(context.Background(), f.sid), "the first demand after boot must heal the 213-record backlog")
	require.EqualValues(t, 1, builds.Load())
	selfHealRequireIndexed(t, f)
}

// Design §5.3: these are safety controls, legitimately already GREEN on the
// pre-heal base. Do not add an unrelated assertion to manufacture RED.
func TestChatHistorySelfHeal_TruncatedCanonicalStaysUnavailableAndUntouched(t *testing.T) {
	for _, cut := range []string{"empty", "inside_saved_record"} {
		t.Run(cut, func(t *testing.T) {
			logs := selfHealCaptureLogs(t)
			f := newSelfHealFixture(t)
			end := int64(0)
			if cut == "inside_saved_record" {
				end = f.addresses[1].ByteOffset / 2
			}
			require.NoError(t, os.Truncate(filepath.Join(f.store.BaseDir(), f.sid, archiveCurrentFile), end))
			before := historyTree(t, historyIndexDir(f.store, f.sid))
			canonical := partitionBytes(t, f.store, f.sid)
			builds := selfHealObserveBuilds(t, f.sid)
			err := f.store.CheckChatHistoryReadiness(context.Background(), f.sid)
			selfHealRequireUnavailable(t, err, "index_inconsistent")
			require.EqualError(t, errors.Unwrap(err), "canonical archive truncated")
			require.Equal(t, before, historyTree(t, historyIndexDir(f.store, f.sid)), "never bless shortened canonical data with a new baseline")
			require.Equal(t, canonical, partitionBytes(t, f.store, f.sid))
			require.Zero(t, builds.Load())
			require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealOKLog))
			require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealBadLog), "index_inconsistent must not enter the heal path")
		})
	}
}

// Design §5.4: nonempty earlier partitions are NOT the empty-partition case P1
// repaired. Their addresses may have been repointed and must stay refused.
func TestChatHistorySelfHeal_NonemptyUnknownEarlierDayStaysUnavailable(t *testing.T) {
	logs := selfHealCaptureLogs(t)
	f := newSelfHealFixture(t)
	cp := historyCheckpointOf(t, f.store, f.sid)
	require.NotContains(t, cp.ArchiveEnds, historyEarlierDay)
	current, err := os.ReadFile(filepath.Join(f.store.BaseDir(), f.sid, archiveCurrentFile))
	require.NoError(t, err)
	firstRecord := current[:f.addresses[1].ByteOffset]
	require.NotEmpty(t, firstRecord)
	require.NoError(t, os.WriteFile(filepath.Join(f.store.BaseDir(), f.sid, historyEarlierDay+".jsonl"), firstRecord, 0o600))
	before := historyTree(t, historyIndexDir(f.store, f.sid))
	canonical := partitionBytes(t, f.store, f.sid)
	builds := selfHealObserveBuilds(t, f.sid)
	err = f.store.CheckChatHistoryReadiness(context.Background(), f.sid)
	selfHealRequireUnavailable(t, err, "index_inconsistent")
	require.EqualError(t, errors.Unwrap(err), "archive catalog repointed")
	require.Equal(t, before, historyTree(t, historyIndexDir(f.store, f.sid)))
	require.Equal(t, canonical, partitionBytes(t, f.store, f.sid))
	require.Zero(t, builds.Load())
	require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealOKLog))
	require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealBadLog))
}

// Design §5.5: the corrupt row is beyond the bounded trigger's overflow probe,
// so only a real full rebuild discovers it. It must not destroy the old index.
func TestChatHistorySelfHeal_CorruptRecordBeyondOverflowFailsClosedWithWarning(t *testing.T) {
	logs := selfHealCaptureLogs(t)
	f := newSelfHealFixture(t)
	selfHealCorruptTail(t, f)
	before := historyTree(t, historyIndexDir(f.store, f.sid))
	canonical := partitionBytes(t, f.store, f.sid)
	builds := selfHealObserveBuilds(t, f.sid)
	err := f.store.CheckChatHistoryReadiness(context.Background(), f.sid)
	selfHealRequireUnavailable(t, err, "catchup_limit")
	require.Nil(t, errors.Unwrap(err), "failure returns the original catchup_limit unchanged")
	assert.EqualValues(t, 1, builds.Load(), "catchup_limit must attempt one real rebuild even when a later record is corrupt")
	require.Equal(t, before, historyTree(t, historyIndexDir(f.store, f.sid)))
	require.Equal(t, canonical, partitionBytes(t, f.store, f.sid))
	records := selfHealEvents(t, logs, f.sid, selfHealBadLog)
	require.Len(t, records, 1, "a corrupt rebuild must emit one warning, not fail silently")
	selfHealRequireLogBase(t, records[0], f.sid, 1)
	require.Equal(t, "corrupt_archive", records[0]["class"])
	require.Equal(t, "after_restart", records[0]["retry"])
	require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealOKLog))
	selfHealRequirePrivateLogs(t, logs, f)
}

// Design §5.6 and §1 Attempts: five demand checks in each store/process
// lifetime, one actual failed build per lifetime. A fresh store models restart.
func TestChatHistorySelfHeal_HardFailureLatchesUntilRestart(t *testing.T) {
	logs := selfHealCaptureLogs(t)
	f := newSelfHealFixture(t)
	selfHealCorruptTail(t, f)
	before := historyTree(t, historyIndexDir(f.store, f.sid))
	builds := selfHealObserveBuilds(t, f.sid)
	for i := 0; i < 5; i++ {
		err := f.store.CheckChatHistoryReadiness(context.Background(), f.sid)
		selfHealRequireUnavailable(t, err, "catchup_limit")
		require.Nil(t, errors.Unwrap(err))
	}
	assert.EqualValues(t, 1, builds.Load(), "five opens must cause exactly one hard-failed rebuild, never zero and never five")
	assert.Len(t, selfHealEvents(t, logs, f.sid, selfHealBadLog), 1, "one rebuild-failed warning per lifetime")
	require.NoError(t, f.store.Close())
	restarted, err := NewUnifiedStore(f.store.BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restarted.Close()) })
	restarted.stopStatsFlusher()
	f.store = restarted
	for i := 0; i < 5; i++ {
		err = restarted.CheckChatHistoryReadiness(context.Background(), f.sid)
		selfHealRequireUnavailable(t, err, "catchup_limit")
	}
	assert.EqualValues(t, 2, builds.Load(), "restart permits exactly one additional attempt")
	records := selfHealEvents(t, logs, f.sid, selfHealBadLog)
	require.Len(t, records, 2, "restart permits exactly one additional rebuild-failed warning")
	for _, record := range records {
		selfHealRequireLogBase(t, record, f.sid, 1)
		require.Equal(t, "corrupt_archive", record["class"])
		require.Equal(t, "after_restart", record["retry"])
	}
	require.Equal(t, before, historyTree(t, historyIndexDir(f.store, f.sid)))
	selfHealRequirePrivateLogs(t, logs, f)
}

// Design §§1,4,5.7, amended by the 6a gate fix round (code W1/W2): busy and
// source_changed retry at least 60 seconds apart and never latch; the
// counted deadline class keeps the 3-attempt latch
// (TestChatHistoryHeal_DeadlineAttemptsStillLatchAfterThree). No sleeps.
func TestChatHistorySelfHeal_BusyAndSourceChangedBackOff60SecondsWithoutLatching(t *testing.T) {
	for _, class := range []string{"busy", "source_changed"} {
		t.Run(class, func(t *testing.T) { selfHealAssertTransientBackoff(t, class) })
	}
}

// Design §5.10 / §2: the session-dir stat total excludes .chat-history.
func TestChatHistorySelfHeal_SizeCapRefusesOnlyAbove32MiBAndLatches(t *testing.T) {
	for _, delta := range []int64{-1, 0, 1} {
		t.Run(fmt.Sprintf("cap%+d", delta), func(t *testing.T) {
			logs := selfHealCaptureLogs(t)
			f := newSelfHealFixture(t)
			dir := filepath.Join(f.store.BaseDir(), f.sid)
			wantBytes := selfHealCap + delta
			selfHealPadFile(t, filepath.Join(dir, "selfheal-size-padding"), wantBytes-selfHealSourceSize(t, f))
			// An arbitrarily large derived file must NOT count toward the cap.
			selfHealPadFile(t, filepath.Join(historyIndexDir(f.store, f.sid), "selfheal-derived-padding"), selfHealCap+1)
			require.Equal(t, wantBytes, selfHealSourceSize(t, f), "exact cap-1 / cap / cap+1 input, not an approximate file size")
			before := selfHealFiles(t, historyIndexDir(f.store, f.sid), false)
			canonical := selfHealFiles(t, dir, true)
			builds := selfHealObserveBuilds(t, f.sid)
			if delta <= 0 {
				require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "32 MiB is inclusive; only a greater total may be refused")
				require.EqualValues(t, 1, builds.Load())
				selfHealRequireIndexed(t, f)
				records := selfHealEvents(t, logs, f.sid, selfHealOKLog)
				require.Len(t, records, 1)
				require.Equal(t, wantBytes, selfHealLogNumber(t, records[0], "bytes"))
			} else {
				for i := 0; i < 5; i++ {
					err := f.store.CheckChatHistoryReadiness(context.Background(), f.sid)
					selfHealRequireUnavailable(t, err, "catchup_limit")
					require.Nil(t, errors.Unwrap(err))
				}
				require.Zero(t, builds.Load(), "oversized sources are refused before the full read")
				require.Equal(t, before, selfHealFiles(t, historyIndexDir(f.store, f.sid), false))
				records := selfHealEvents(t, logs, f.sid, selfHealBadLog)
				require.Len(t, records, 1, "size refusal is warning-visible and latched, not silent or repeated on every open")
				selfHealRequireLogBase(t, records[0], f.sid, 1)
				require.Equal(t, "too_large", records[0]["class"])
				require.Equal(t, "after_restart", records[0]["retry"])
				require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealOKLog))
			}
			require.Equal(t, canonical, selfHealFiles(t, dir, true), "neither refusal nor successful heal writes canonical files")
			selfHealRequirePrivateLogs(t, logs, f)
		})
	}
}
