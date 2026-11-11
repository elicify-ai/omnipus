package agent

import "sync"

type cancelDisplayOutcome struct {
	once  sync.Once
	write func(string)
}

// setOnCancelPersist separates durable display metadata from the exit audit.
// A detached execution cannot be joined, but its confirmed cancellation facts
// must already exist when the controller publishes the detached boundary.
func (ts *turnState) setOnCancelPersist(fn func(string)) {
	ts.mu.Lock()
	ts.onCancelPersist = &cancelDisplayOutcome{write: fn}
	ts.mu.Unlock()
}

func (ts *turnState) persistCancelOutcome(method string) {
	ts.mu.RLock()
	persist := ts.onCancelPersist
	ts.mu.RUnlock()
	if persist != nil {
		// Do also joins an in-flight writer, rather than treating a claimed
		// callback as already durable while another goroutine is appending it.
		persist.once.Do(func() { persist.write(method) })
	}
}

// persistCancelBeforeDone writes a claimed cancel's turn_canceled record now,
// with the method Finish would report, so it is readable before the turn's
// done frame is published (#1524). The write is once-only: Finish's own
// callback later joins it instead of appending a second record. A turn no
// cancel claimed writes nothing.
func (ts *turnState) persistCancelBeforeDone() {
	if !ts.cancelFired.Load() {
		return
	}
	method := "graceful"
	if ts.hardAbortRequested() {
		method = "hard"
	}
	ts.persistCancelOutcome(method)
}
