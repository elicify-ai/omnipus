package session

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 6a gate fix round (code W1/W2): deadline still counts toward the latch.
// Three expired attempts at least 60 s apart latch; the fourth eligible
// check makes no attempt. Control for the uncounted busy/source_changed.
func TestChatHistoryHeal_DeadlineAttemptsStillLatchAfterThree(t *testing.T) {
	logs := selfHealCaptureLogs(t)
	f := newSelfHealFixture(t)
	base := time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC)
	now := base
	oldClock, oldTimeout := f.store.historyHealNow, f.store.historyHealWithTimeout
	t.Cleanup(func() { f.store.historyHealNow, f.store.historyHealWithTimeout = oldClock, oldTimeout })
	f.store.historyHealNow = func() time.Time { return now }
	contexts := 0
	f.store.historyHealWithTimeout = func(parent context.Context, _ time.Duration) (context.Context, context.CancelFunc) {
		contexts++
		return context.WithDeadline(parent, time.Time{})
	}
	for i, elapsed := range []time.Duration{0, 60 * time.Second, 120 * time.Second, 180 * time.Second, 24 * time.Hour} {
		now = base.Add(elapsed)
		selfHealRequireUnavailable(t, f.store.CheckChatHistoryReadiness(context.Background(), f.sid), "catchup_limit")
		want := min(i+1, 3)
		require.Equal(t, want, contexts, "elapsed=%s", elapsed)
		require.Len(t, selfHealEvents(t, logs, f.sid, selfHealBadLog), want, "elapsed=%s", elapsed)
	}
	for i, record := range selfHealEvents(t, logs, f.sid, selfHealBadLog) {
		selfHealRequireLogBase(t, record, f.sid, int64(i+1))
		require.Equal(t, "deadline", record["class"])
		retry := "after_backoff"
		if i == 2 {
			retry = "after_restart"
		}
		require.Equal(t, retry, record["retry"], fmt.Sprintf("attempt %d", i+1))
	}
}
