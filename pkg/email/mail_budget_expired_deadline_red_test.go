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
// has not fired yet looks like. The only wall-clock dependence is waiting
// until the reported deadline has genuinely passed.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type skewedDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c skewedDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }
func (c skewedDeadlineContext) Done() <-chan struct{}       { return nil }
func (c skewedDeadlineContext) Err() error                  { return nil }

func TestRunDialValue_PassedDeadlineNeverDials(t *testing.T) {
	t.Run("free slot, deadline already passed", func(t *testing.T) {
		g := NewMailBudget(t.TempDir()).gateFor("acct")
		var ran atomic.Int32
		ctx := skewedDeadlineContext{Context: context.Background(), deadline: time.Now().Add(-time.Millisecond)}
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
		deadline := time.Now().Add(50 * time.Millisecond)
		ctx := skewedDeadlineContext{Context: context.Background(), deadline: deadline}
		type outcome struct{ err error }
		out := make(chan outcome, 1)
		go func() {
			_, err := runDialValue(g, ctx, func(context.Context) (int, error) {
				ran.Add(1)
				return 1, nil
			})
			out <- outcome{err}
		}()
		time.Sleep(time.Until(deadline) + 10*time.Millisecond) // wall clock is now past the deadline
		<-g.slots                                              // a holder finishes: a slot frees
		select {
		case got := <-out:
			require.ErrorIs(t, got.err, ErrMailBusy)
			require.ErrorIs(t, got.err, context.DeadlineExceeded)
		case <-time.After(3 * time.Second):
			t.Fatal("expected the queued call to be refused; actual: no completion within harness safety bound")
		}
		require.Zero(t, ran.Load(), "fn must not run when the slot frees after the deadline")
		require.Equal(t, mailBudgetSlotsPerAccount-1, len(g.slots), "only the still-running holder keeps a slot")
	})
}
