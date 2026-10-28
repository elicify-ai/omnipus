package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Design §1: count actual failed attempts, not demand calls or elapsed real
// time. A check just after attempt two still backs off from that LAST attempt,
// not from the first attempt or the last refused check.
func selfHealAssertTransientBackoff(t *testing.T, class string) {
	t.Helper()
	logs := selfHealCaptureLogs(t)
	f := newSelfHealFixture(t)
	before := historyTree(t, historyIndexDir(f.store, f.sid))
	base := time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC)
	now := base
	oldClock := f.store.historyHealNow
	f.store.historyHealNow = func() time.Time { return now }
	t.Cleanup(func() { f.store.historyHealNow = oldClock })
	builds := selfHealObserveBuilds(t, f.sid)
	releaseFault, writes := selfHealTransientFault(t, f, class)
	schedule := []struct {
		elapsed  time.Duration
		attempts int
	}{
		{0, 1}, // first failure
		{0, 1}, // same instant cannot spin another attempt
		{60*time.Second - time.Millisecond, 1},
		{60 * time.Second, 2}, // equality is eligible
		{60*time.Second + time.Millisecond, 2},
		{120*time.Second - time.Millisecond, 2},
		{120 * time.Second, 3}, // exactly 60 seconds since attempt two
		{120*time.Second + time.Millisecond, 3},
		{180 * time.Second, 3}, // no fourth attempt after another full interval
		{24 * time.Hour, 3},    // the latch does not expire with a longer backoff
	}
	for _, step := range schedule {
		now = base.Add(step.elapsed)
		err := f.store.CheckChatHistoryReadiness(context.Background(), f.sid)
		selfHealRequireUnavailable(t, err, "catchup_limit")
		require.Nil(t, errors.Unwrap(err), "transient failure/backoff/latch must preserve the original public error at elapsed=%s", step.elapsed)
		records := selfHealEvents(t, logs, f.sid, selfHealBadLog)
		require.Len(t, records, step.attempts, "class=%s elapsed=%s: one warning per actual attempt, none for a refused check", class, step.elapsed)
		for i, record := range records {
			selfHealRequireLogBase(t, record, f.sid, int64(i+1))
			require.Equal(t, class, record["class"], "class=%s elapsed=%s", class, step.elapsed)
			retry := "after_backoff"
			if i == 2 {
				// Design §§1,4: with three attempts spent, another in-process
				// retry is impossible. The warning must not promise one.
				retry = "after_restart"
			}
			require.Equal(t, retry, record["retry"], "attempt=%d elapsed=%s", i+1, step.elapsed)
		}
		wantBuilds := int64(step.attempts)
		if class == "busy" {
			wantBuilds = 0
			require.Equal(t, 1, len(chatHistoryHealSlot), "a busy refusal must not consume another holder's slot")
		}
		require.Equal(t, wantBuilds, builds.Load(), "class=%s elapsed=%s: real builder I/O count", class, step.elapsed)
		require.Equal(t, wantBuilds, writes.Load(), "only actual source_changed attempts append a canonical record")
		require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealOKLog))
		require.Equal(t, before, historyTree(t, historyIndexDir(f.store, f.sid)), "a failed/backing-off attempt must not publish a new baseline")
	}
	// Remove the external failure. Continued refusal now proves a real attempt
	// cap/latch, not merely a slot that remained busy or a source still changing.
	releaseFault()
	err := f.store.CheckChatHistoryReadiness(context.Background(), f.sid)
	selfHealRequireUnavailable(t, err, "catchup_limit")
	require.Nil(t, errors.Unwrap(err))
	require.Len(t, selfHealEvents(t, logs, f.sid, selfHealBadLog), 3, "freeing the slot/removing source churn cannot unlock attempt four")
	wantBuilds := int64(3)
	if class == "busy" {
		wantBuilds = 0
	}
	require.Equal(t, wantBuilds, builds.Load())
	require.Equal(t, wantBuilds, writes.Load())
	require.Zero(t, len(chatHistoryHealSlot), "neither failed builds nor latch checks may leak a heal slot")
	require.Empty(t, selfHealEvents(t, logs, f.sid, selfHealOKLog))
	require.Equal(t, before, historyTree(t, historyIndexDir(f.store, f.sid)))
	selfHealRequirePrivateLogs(t, logs, f)
}

// Faults occur at real process edges: the actual capacity-one slot, or a real
// canonical append between the builder's source inventories. The reconciler,
// eligibility decision and rebuild remain real. No production function is
// replaced, and no 60-second wall-clock sleep is used.
func selfHealTransientFault(t *testing.T, f *selfHealFixture, class string) (func(), *atomic.Int64) {
	t.Helper()
	writes := new(atomic.Int64)
	var once sync.Once
	if class == "busy" {
		require.Equal(t, 1, cap(chatHistoryHealSlot), "design §2 permits only one process-wide rebuild")
		require.Zero(t, len(chatHistoryHealSlot), "positive isolation control: no other test owns the heal slot")
		select {
		case chatHistoryHealSlot <- struct{}{}:
		default:
			t.Fatal("test could not acquire the real process-wide heal slot")
		}
		release := func() {
			once.Do(func() {
				select {
				case <-chatHistoryHealSlot:
				default:
					t.Error("busy refusal stole the test-owned heal slot")
				}
			})
		}
		t.Cleanup(release)
		return release, writes
	}
	require.Equal(t, "source_changed", class)
	old := chatHistoryReadHook
	chatHistoryReadHook = func(ctx context.Context, e chatHistoryReadEvent) error {
		if old != nil {
			if err := old(ctx, e); err != nil {
				return err
			}
		}
		if e.SessionID == f.sid && e.Stream == "archive" && e.Purpose == "suffix" && e.Phase == "record_opened" && e.Address != nil && e.Address.ByteOffset == 0 {
			n := writes.Add(1)
			a, err := f.store.AppendTranscriptAddressed(f.sid, historyUserEntry(fmt.Sprintf("source-changed-attempt-%d", n), selfHealBody, int(n)))
			if err != nil {
				return err
			}
			f.addresses = append(f.addresses, a)
		}
		return ctx.Err()
	}
	release := func() { once.Do(func() { chatHistoryReadHook = old }) }
	t.Cleanup(release)
	return release, writes
}
