package email

// A queued mail-budget operation must never dial once its context bound has
// passed. The deadline timer of a derived context (the coalesced flight's
// detached context carries the first caller's deadline on a timer of its own)
// can fire later than the caller's, so a slot freed in that gap used to win
// the select against a not-yet-delivered Done and run the dial for a caller
// that had already been told ErrMailBusy. Observed in CI as a stale flight
// dialing through the process-wide imapDial seam during the NEXT test
// (TestBudget_JoinerCancellationIsolated: dials expected 1, actual 2).
//
// skewedDeadlineContext reproduces that state without timing luck: it reports
// a deadline while never delivering Done, exactly what a context whose timer
// has not fired yet looks like. The deadline is a value the test moves into
// the past on an observed event, so no wait is wall-clock based.

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// skewedDeadlineContext reports a settable deadline and never delivers Done.
// Done() is called by runDialValue's select once the call is parked in (or
// about to park in) the queue, so it doubles as the "queued" event.
type skewedDeadlineContext struct {
	context.Context
	deadlineNanos atomic.Int64
	queued        chan struct{}
	queuedOnce    sync.Once
}

func newSkewedDeadlineContext(deadline time.Time) *skewedDeadlineContext {
	c := &skewedDeadlineContext{Context: context.Background(), queued: make(chan struct{})}
	c.deadlineNanos.Store(deadline.UnixNano())
	return c
}

func (c *skewedDeadlineContext) setDeadline(d time.Time) { c.deadlineNanos.Store(d.UnixNano()) }
func (c *skewedDeadlineContext) Deadline() (time.Time, bool) {
	return time.Unix(0, c.deadlineNanos.Load()), true
}
func (c *skewedDeadlineContext) Done() <-chan struct{} {
	c.queuedOnce.Do(func() { close(c.queued) })
	return nil
}
func (c *skewedDeadlineContext) Err() error { return nil }

func TestRunDialValue_PassedDeadlineNeverDials(t *testing.T) {
	t.Run("free slot, deadline already passed", func(t *testing.T) {
		g := NewMailBudget(t.TempDir()).gateFor("acct")
		var ran atomic.Int32
		ctx := newSkewedDeadlineContext(time.Now().Add(-time.Millisecond))
		_, err := runDialValue(g, ctx, func(context.Context) (int, error) {
			ran.Add(1)
			return 1, nil
		})
		require.ErrorIs(t, err, ErrMailBusy)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Zero(t, ran.Load(), "fn must not run after the deadline passed")
		require.Zero(t, len(g.slots), "the refused call must hand its slot back")
	})

	t.Run("queued, slot frees after the deadline passed", func(t *testing.T) {
		g := NewMailBudget(t.TempDir()).gateFor("acct")
		for range mailBudgetSlotsPerAccount {
			g.slots <- struct{}{}
		}
		var ran atomic.Int32
		ctx := newSkewedDeadlineContext(time.Now().Add(time.Hour)) // live while it queues
		out := make(chan error, 1)
		go func() {
			_, err := runDialValue(g, ctx, func(context.Context) (int, error) {
				ran.Add(1)
				return 1, nil
			})
			out <- err
		}()
		select {
		case <-ctx.queued: // the call reached its queue wait
		case <-time.After(3 * time.Second):
			t.Fatal("expected the call to reach the slot queue; actual: no queue wait within harness safety bound")
		}
		ctx.setDeadline(time.Now().Add(-time.Millisecond)) // the deadline passes while queued; Done is never delivered
		<-g.slots                                          // a holder finishes: a slot frees
		select {
		case err := <-out:
			require.ErrorIs(t, err, ErrMailBusy)
			require.ErrorIs(t, err, context.DeadlineExceeded)
		case <-time.After(3 * time.Second):
			t.Fatal("expected the queued call to be refused; actual: no completion within harness safety bound")
		}
		require.Zero(t, ran.Load(), "fn must not run when the slot frees after the deadline")
		require.Equal(t, mailBudgetSlotsPerAccount-1, len(g.slots), "only the still-running holder keeps a slot")
	})
}

// A call whose bound still holds, or that has none, must keep running fn: the
// post-slot re-check only refuses what is genuinely past its bound.
func TestRunDialValue_LiveOrUnboundedContextStillDials(t *testing.T) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Hour)
	t.Cleanup(cancel)
	cases := []struct {
		name string
		ctx  context.Context
	}{
		{"no deadline", context.Background()},
		{"live real deadline", timeoutCtx},
		{"live reported deadline, Done undelivered", newSkewedDeadlineContext(time.Now().Add(time.Hour))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewMailBudget(t.TempDir()).gateFor("acct")
			var ran atomic.Int32
			got, err := runDialValue(g, tc.ctx, func(context.Context) (int, error) {
				ran.Add(1)
				return 7, nil
			})
			require.NoError(t, err)
			require.Equal(t, 7, got)
			require.Equal(t, int32(1), ran.Load())
			require.Zero(t, len(g.slots), "slot handed back after the run")
		})
	}
}
