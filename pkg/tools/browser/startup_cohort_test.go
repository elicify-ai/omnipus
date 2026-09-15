package browser

import (
	"context"
	"errors"
	"testing"
)

// Cancellation callbacks are allowed to run later than Context.Err changes.
// This fixture retains the callback without running it until test cleanup.
type startupDeferredCancellation struct {
	context.Context
	callback func()
}

func (c *startupDeferredCancellation) Value(any) any { return nil }
func (c *startupDeferredCancellation) AfterFunc(fn func()) func() bool {
	c.callback = fn
	return func() bool { return true }
}

func TestStartupCohortPublicationReadsOriginalCaller(t *testing.T) {
	caller, cancel := context.WithCancel(context.Background())
	original := &startupDeferredCancellation{Context: caller}
	flight := newStartupCohort()
	leave, ok := flight.join(original)
	if !ok {
		t.Fatal("live request refused")
	}
	defer leave()
	defer flight.cancel()
	if !flight.live() {
		t.Fatal("live original request did not authorize startup")
	}
	cancel()
	if flight.ctx.Err() != nil {
		t.Fatal("fixture delivered asynchronous cancellation too soon")
	}
	if flight.live() {
		t.Error("canceled original caller still authorized launch publication")
	}
	if join, ok := flight.join(context.Background()); ok {
		join()
		t.Error("new request revived abandoned startup")
	}
	if original.callback != nil {
		original.callback()
	}
}

func TestStartupOldCompletionCannotClearReplacement(t *testing.T) {
	pool := NewBrowserPool(t.TempDir(), BrowserConfig{})
	old, next := newStartupCohort(), newStartupCohort()
	defer old.cancel()
	defer next.cancel()
	id := browserTestKey("replacement").String()
	pool.launching[id] = next
	failed := errors.New("old launch failed")
	pool.finishLaunch(id, old, failed)
	if pool.launching[id] != next {
		t.Error("old completion removed replacement launch")
	}
	select {
	case <-next.done:
		t.Error("old completion resolved replacement waiters")
	default:
	}
	if err := old.wait(context.Background()); !errors.Is(err, failed) {
		t.Errorf("old waiter lost its exact failure: %v", err)
	}
}

func TestSessionStartupContextReadsRetiredGateBeforeCallback(t *testing.T) {
	lifetime, retire := context.WithCancelCause(context.Background())
	original := &startupDeferredCancellation{Context: lifetime}
	operation, stop := sessionStartupContext(context.Background(), original)
	defer stop()
	retire(errBrowserSessionChanged)
	if !errors.Is(operation.Err(), context.Canceled) {
		t.Errorf("retired original gate still admits startup: %v", operation.Err())
	}
	if original.callback != nil {
		original.callback()
	}
}
