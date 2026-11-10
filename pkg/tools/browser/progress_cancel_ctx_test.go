package browser

import (
	"context"
	"sync/atomic"
	"testing"
)

// progressCancelCtx is a real cancellable context whose cancel fires the
// first time the code under test consults it (Err or Done) AFTER started()
// reports that the work has observably begun. It lets a test deliver the
// cancel in the middle of a loop — after one item is provably done and before
// the next one — without a hook in production code and without a sleep: the
// trigger is the work's own filesystem progress.
//
// A loop that consults its context only once, before the first item, sees a
// live context (nothing has started yet) and is never cancelled; the later
// items it then processes are what the in-flight tests catch.
type progressCancelCtx struct {
	context.Context
	cancel  context.CancelFunc
	started func() bool
	fired   atomic.Bool
}

func newProgressCancelCtx(t *testing.T, started func() bool) *progressCancelCtx {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &progressCancelCtx{Context: ctx, cancel: cancel, started: started}
}

func (c *progressCancelCtx) trigger() {
	if !c.fired.Load() && c.started() {
		c.fired.Store(true)
		c.cancel()
	}
}

func (c *progressCancelCtx) Err() error {
	c.trigger()
	return c.Context.Err()
}

func (c *progressCancelCtx) Done() <-chan struct{} {
	c.trigger()
	return c.Context.Done()
}
