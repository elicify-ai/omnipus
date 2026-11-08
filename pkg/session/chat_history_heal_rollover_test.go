package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FIX-ROUND-1 row 3 / cr CR-1 / sfh F2: proven normal source movement is
// transient, but a stable-source I/O failure must remain a permanent failure.
func TestChatHistoryHeal_DayRolloverIsTransientAndRetryable(t *testing.T) {
	t.Run("real_day_rollover_after_first_build_record", func(t *testing.T) {
		logs := selfHealCaptureLogs(t)
		f := newSelfHealFixture(t)
		before := selfHealFiles(t, historyIndexDir(f.store, f.sid), false)
		current := filepath.Join(f.store.BaseDir(), f.sid, archiveCurrentFile)
		oldBytes, err := os.ReadFile(current)
		require.NoError(t, err)
		base := time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC)
		now := base
		f.store.historyHealNow = func() time.Time { return now }
		builds := selfHealObserveBuilds(t, f.sid)
		old := chatHistoryReadHook
		var once sync.Once
		rolled := false
		chatHistoryReadHook = func(ctx context.Context, e chatHistoryReadEvent) error {
			if old != nil {
				if hookErr := old(ctx, e); hookErr != nil {
					return hookErr
				}
			}
			// record_closed is after real content I/O and closes the handle
			// before the rename, including on platforms that forbid renaming an
			// open file. The next builder iteration still uses the captured path.
			if e.SessionID == f.sid && e.Scope == "recovery" && e.Purpose == "suffix" && e.Phase == "record_closed" && e.Address != nil && e.Address.ByteOffset == 0 {
				once.Do(func() {
					historyArchiveDay(t, f.store, f.sid, "2026-01-04")
					a, appendErr := f.store.AppendTranscriptAddressed(f.sid, historyUserEntry("rollover-during-heal", "x", 0))
					require.NoError(t, appendErr, "a canonical rollover cannot queue behind the builder")
					require.Equal(t, "2026-01-04", a.PartitionKey)
					f.addresses = append(f.addresses, a)
					rolled = true
				})
			}
			return ctx.Err()
		}
		t.Cleanup(func() { chatHistoryReadHook = old })
		err = f.store.CheckChatHistoryReadiness(context.Background(), f.sid)
		selfHealRequireUnavailable(t, err, "catchup_limit")
		require.Nil(t, errors.Unwrap(err))
		require.True(t, rolled, "positive control: the actual full builder reached the rollover scheduling hook")
		rolledBytes, err := os.ReadFile(filepath.Join(f.store.BaseDir(), f.sid, historyLaterDay+".jsonl"))
		require.NoError(t, err)
		require.Equal(t, oldBytes, rolledBytes, "the old canonical partition moved intact, not damaged")
		records := selfHealEvents(t, logs, f.sid, selfHealBadLog)
		require.Len(t, records, 1)
		selfHealRequireLogBase(t, records[0], f.sid, 1)
		assert.Equal(t, "source_changed", records[0]["class"], "normal day rollover is not a permanent storage/corruption failure")
		assert.Equal(t, "after_backoff", records[0]["retry"])
		require.Equal(t, before, selfHealFiles(t, historyIndexDir(f.store, f.sid), false))
		require.Zero(t, len(chatHistoryHealSlot))
		for _, elapsed := range []time.Duration{0, 60*time.Second - time.Millisecond} {
			now = base.Add(elapsed)
			selfHealRequireUnavailable(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "catchup_limit")
			require.EqualValues(t, 1, builds.Load(), "checks during backoff cannot consume an attempt")
		}
		chatHistoryReadHook = old // the source is stable for the eligible retry
		canonical := partitionBytes(t, f.store, f.sid)
		now = base.Add(60 * time.Second)
		require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "the eligible retry must recover after normal day rollover without a restart")
		// The observer counts offset-zero reads, not attempts: the failed build
		// read one stream; the successful retry reads both nonempty days.
		require.EqualValues(t, 3, builds.Load())
		healFixRequireAddresses(t, f.store, f.sid, f.addresses)
		require.Equal(t, canonical, partitionBytes(t, f.store, f.sid))
		rebuilt := selfHealEvents(t, logs, f.sid, selfHealOKLog)
		require.Len(t, rebuilt, 1)
		selfHealRequireLogBase(t, rebuilt[0], f.sid, 2)
		selfHealRequirePrivateLogs(t, logs, f)
	})
	t.Run("stable_source_io_error_stays_permanent", healFixStableSourceIOFailure)
}

func healFixStableSourceIOFailure(t *testing.T) {
	logs := selfHealCaptureLogs(t)
	f := newSelfHealFixture(t)
	before := selfHealFiles(t, historyIndexDir(f.store, f.sid), false)
	canonical := partitionBytes(t, f.store, f.sid)
	base := time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC)
	now := base
	f.store.historyHealNow = func() time.Time { return now }
	builds := selfHealObserveBuilds(t, f.sid)
	old := chatHistoryReadHook
	chatHistoryReadHook = func(ctx context.Context, e chatHistoryReadEvent) error {
		if old != nil {
			if err := old(ctx, e); err != nil {
				return err
			}
		}
		if e.SessionID == f.sid && e.Purpose == "suffix" && e.Phase == "record_opened" && e.Address != nil && e.Address.ByteOffset == 0 {
			return os.ErrPermission // real I/O edge, with no canonical source change
		}
		return nil
	}
	t.Cleanup(func() { chatHistoryReadHook = old })
	selfHealRequireUnavailable(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "catchup_limit")
	chatHistoryReadHook = old
	now = base.Add(24 * time.Hour)
	selfHealRequireUnavailable(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "catchup_limit")
	require.EqualValues(t, 1, builds.Load())
	records := selfHealEvents(t, logs, f.sid, selfHealBadLog)
	require.Len(t, records, 1)
	require.Equal(t, "storage_error", records[0]["class"])
	require.Equal(t, "after_restart", records[0]["retry"])
	require.Equal(t, before, selfHealFiles(t, historyIndexDir(f.store, f.sid), false))
	require.Equal(t, canonical, partitionBytes(t, f.store, f.sid))
	require.Zero(t, len(chatHistoryHealSlot))
}
