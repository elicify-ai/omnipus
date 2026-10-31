package session

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 6a delta review, tests finding 3: the latch compares COUNTED failures, not
// total attempts (founder decision Q5 = A). busy and source_changed are real
// attempts (warning attempt number, 60 s spacing) that never count; deadline
// counts and retries until the third counted one; any other failure counts
// and latches at once. Pure single-class traces cannot tell "counted" from
// "attempts" apart, so these traces mix the classes in one owner's history.

// healTraceStep is one eligible demand check (each 60 s after the previous)
// with the fault active during it ("" = no fault: the heal may complete).
type healTraceStep struct {
	fault   string
	attempt int    // expected warning attempt number; 0 = no attempt made
	retry   string // expected retry field of that warning
	counted int    // expected counted failures after the step
	latched bool
}

type healTrace struct {
	t     *testing.T
	f     *selfHealFixture
	logs  *selfHealLogs
	base  time.Time
	now   time.Time
	fault string
}

func newHealTrace(t *testing.T) *healTrace {
	t.Helper()
	h := &healTrace{t: t, logs: selfHealCaptureLogs(t), f: newSelfHealFixture(t), base: time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC)}
	h.now = h.base
	store := h.f.store
	oldClock, oldTimeout, oldRead := store.historyHealNow, store.historyHealWithTimeout, chatHistoryReadHook
	t.Cleanup(func() {
		store.historyHealNow, store.historyHealWithTimeout, chatHistoryReadHook = oldClock, oldTimeout, oldRead
	})
	store.historyHealNow = func() time.Time { return h.now }
	store.historyHealWithTimeout = func(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
		if h.fault == "deadline" {
			return context.WithDeadline(parent, time.Time{}) // the real timeout edge, already passed
		}
		return context.WithTimeout(parent, d)
	}
	writes := 0
	chatHistoryReadHook = func(ctx context.Context, e chatHistoryReadEvent) error {
		if oldRead != nil {
			if err := oldRead(ctx, e); err != nil {
				return err
			}
		}
		if e.SessionID != h.f.sid || e.Purpose != "suffix" || e.Phase != "record_opened" || e.Address == nil || e.Address.ByteOffset != 0 {
			return nil
		}
		switch h.fault {
		case "source_changed":
			// A real canonical append between the builder's source inventories.
			writes++
			a, err := store.AppendTranscriptAddressed(h.f.sid, historyUserEntry(fmt.Sprintf("mixed-trace-write-%d", writes), selfHealBody, writes))
			if err != nil {
				return err
			}
			h.f.addresses = append(h.f.addresses, a)
		case "storage_error":
			return &fs.PathError{Op: "open", Path: filepath.Join(store.BaseDir(), h.f.sid, archiveCurrentFile), Err: fs.ErrPermission}
		}
		return ctx.Err()
	}
	return h
}

func (h *healTrace) run(steps []healTraceStep) {
	t := h.t
	t.Helper()
	attempts := 0
	for i, step := range steps {
		h.now = h.base.Add(time.Duration(i) * chatHistoryHealBackoff) // every step is eligible by the clock
		h.fault = step.fault
		if step.fault == "busy" {
			require.Zero(t, len(chatHistoryHealSlot), "positive isolation control: no other test owns the heal slot")
			chatHistoryHealSlot <- struct{}{} // another chat's rebuild holds the real slot
		}
		err := h.f.store.CheckChatHistoryReadiness(context.Background(), h.f.sid)
		if step.fault == "busy" {
			<-chatHistoryHealSlot
		}
		label := fmt.Sprintf("step %d (%s)", i+1, step.fault)
		if step.attempt > 0 {
			attempts++
			require.Equal(t, step.attempt, attempts, "%s: test table numbering", label)
		}
		if step.fault == "" && step.attempt > 0 {
			require.NoError(t, err, "%s: the heal must still be eligible and complete", label)
			ok := selfHealEvents(t, h.logs, h.f.sid, selfHealOKLog)
			require.Len(t, ok, 1, label)
			selfHealRequireLogBase(t, ok[0], h.f.sid, int64(step.attempt))
		} else {
			selfHealRequireUnavailable(t, err, "catchup_limit")
		}
		bad := selfHealEvents(t, h.logs, h.f.sid, selfHealBadLog)
		failures := 0
		for _, s := range steps[:i+1] {
			if s.attempt > 0 && s.fault != "" {
				failures++
			}
		}
		require.Len(t, bad, failures, "%s: one failure warning per real failed attempt, none for a refused check", label)
		if step.attempt > 0 && step.fault != "" {
			last := bad[len(bad)-1]
			selfHealRequireLogBase(t, last, h.f.sid, int64(step.attempt))
			require.Equal(t, step.fault, last["class"], label)
			require.Equal(t, step.retry, last["retry"], label)
		}
		owner := h.f.store.historyOwner(h.f.sid)
		owner.mu.Lock()
		state := owner.heal
		owner.mu.Unlock()
		require.Equal(t, step.counted, state.counted, "%s: counted failures", label)
		require.Equal(t, step.latched, state.latched, "%s: latch", label)
		if step.fault == "" && step.attempt > 0 {
			require.Equal(t, chatHistoryHealState{}, state, "%s: success clears the attempt state", label)
		} else {
			require.Equal(t, attempts, state.attempts, "%s: total attempts", label)
		}
	}
	require.Zero(t, len(chatHistoryHealSlot), "no step may leak the heal slot")
	selfHealRequirePrivateLogs(t, h.logs, h.f)
}

func TestChatHistoryHeal_LatchCountsOnlyCountedFailures(t *testing.T) {
	t.Run("busy_busy_deadline_stays_retryable_and_heals", func(t *testing.T) {
		// Total attempt three is only the FIRST counted failure: comparing
		// total attempts would latch here.
		h := newHealTrace(t)
		h.run([]healTraceStep{
			{fault: "busy", attempt: 1, retry: "after_backoff"},
			{fault: "busy", attempt: 2, retry: "after_backoff"},
			{fault: "deadline", attempt: 3, retry: "after_backoff", counted: 1},
			{fault: "", attempt: 4},
		})
		selfHealRequireIndexed(t, h.f)
	})
	t.Run("deadline_busy_source_changed_deadline_deadline_latches_only_on_the_last", func(t *testing.T) {
		newHealTrace(t).run([]healTraceStep{
			{fault: "deadline", attempt: 1, retry: "after_backoff", counted: 1},
			{fault: "busy", attempt: 2, retry: "after_backoff", counted: 1},
			{fault: "source_changed", attempt: 3, retry: "after_backoff", counted: 1},
			{fault: "deadline", attempt: 4, retry: "after_backoff", counted: 2},
			{fault: "deadline", attempt: 5, retry: "after_restart", counted: 3, latched: true},
			// Latched: an eligible healthy check makes no further attempt.
			{fault: "", attempt: 0, counted: 3, latched: true},
		})
	})
	t.Run("uncounted_failures_after_two_deadlines_still_leave_one_counted_attempt", func(t *testing.T) {
		newHealTrace(t).run([]healTraceStep{
			{fault: "deadline", attempt: 1, retry: "after_backoff", counted: 1},
			{fault: "deadline", attempt: 2, retry: "after_backoff", counted: 2},
			{fault: "busy", attempt: 3, retry: "after_backoff", counted: 2},
			{fault: "source_changed", attempt: 4, retry: "after_backoff", counted: 2},
			{fault: "busy", attempt: 5, retry: "after_backoff", counted: 2},
			{fault: "", attempt: 6},
		})
	})
	t.Run("other_failure_counts_and_latches_at_once_after_uncounted_ones", func(t *testing.T) {
		newHealTrace(t).run([]healTraceStep{
			{fault: "busy", attempt: 1, retry: "after_backoff"},
			{fault: "source_changed", attempt: 2, retry: "after_backoff"},
			{fault: "storage_error", attempt: 3, retry: "after_restart", counted: 1, latched: true},
			{fault: "", attempt: 0, counted: 1, latched: true},
		})
	})
}
