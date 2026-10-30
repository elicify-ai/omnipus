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

// Design §1 as amended by the 6a gate fix round (code W1/W2): busy and
// source_changed are not failures of the heal itself, so they never count
// toward the 3-attempt latch, but every attempt still waits at least 60 s
// after the LAST attempt (not the first, not the last refused check). Count
// actual attempts, not demand calls or elapsed real time.
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
		{180*time.Second - time.Millisecond, 3},
		{180 * time.Second, 4}, // a fourth attempt: these classes never latch
		{240*time.Second - time.Millisecond, 4},
		{240 * time.Second, 5},
	}
	for _, step := range schedule {
		now = base.Add(step.elapsed)
		err := f.store.CheckChatHistoryReadiness(context.Background(), f.sid)
		selfHealRequireUnavailable(t, err, "catchup_limit")
		require.Nil(t, errors.Unwrap(err), "transient failure/backoff must preserve the original public error at elapsed=%s", step.elapsed)
		records := selfHealEvents(t, logs, f.sid, selfHealBadLog)
		require.Len(t, records, step.attempts, "class=%s elapsed=%s: one warning per actual attempt, none for a refused check", class, step.elapsed)
		for i, record := range records {
			selfHealRequireLogBase(t, record, f.sid, int64(i+1))
			require.Equal(t, class, record["class"], "class=%s elapsed=%s", class, step.elapsed)
			require.Equal(t, "after_backoff", record["retry"], "attempt=%d elapsed=%s: an uncounted class always retries", i+1, step.elapsed)
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
		owner := f.store.historyOwner(f.sid)
		owner.mu.Lock()
		latched := owner.heal.latched
		owner.mu.Unlock()
		require.False(t, latched, "class=%s elapsed=%s: an uncounted class must not latch", class, step.elapsed)
	}
	// Remove the external failure. The spacing still holds just before the
	// next eligible instant; at it, the heal completes without a restart.
	releaseFault()
	now = base.Add(300*time.Second - time.Millisecond)
	selfHealRequireUnavailable(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "catchup_limit")
	require.Len(t, selfHealEvents(t, logs, f.sid, selfHealBadLog), 5, "the 60 s spacing still applies after the fault clears")
	now = base.Add(300 * time.Second)
	require.NoError(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "class=%s: after five uncounted attempts the heal still recovers", class)
	require.Len(t, selfHealEvents(t, logs, f.sid, selfHealBadLog), 5)
	ok := selfHealEvents(t, logs, f.sid, selfHealOKLog)
	require.Len(t, ok, 1)
	selfHealRequireLogBase(t, ok[0], f.sid, 6)
	wantWrites := int64(5)
	if class == "busy" {
		wantWrites = 0
	}
	require.Equal(t, wantWrites, writes.Load())
	require.Zero(t, len(chatHistoryHealSlot), "neither failed builds nor the heal may leak a heal slot")
	owner := f.store.historyOwner(f.sid)
	owner.mu.Lock()
	state := owner.heal
	owner.mu.Unlock()
	require.Equal(t, chatHistoryHealState{}, state, "success clears the attempt state")
	selfHealRequireIndexed(t, f)
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
