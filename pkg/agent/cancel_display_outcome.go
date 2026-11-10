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
