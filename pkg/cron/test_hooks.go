package cron

import "context"

// test_hooks.go — test-only barrier seams for the scheduler-ownership
// fixtures of tool-parity spec r17 §13.1 tests 12d/12e/12f (scenarios 1.15,
// 18.32, 18.34 and 18.40). Every hook is nil in production: an unset hook
// costs one nil check and changes no behaviour. Tests install a hook to hold a
// goroutine at an exact point (a barrier) or to fail the real store save, and
// restore the previous value when done.

// cronTestHooks holds the installed hooks. Guarded by CronService.mu.
type cronTestHooks struct {
	// dispatchEntry runs in a dispatched job's goroutine after collection has
	// unlocked and before the job waits for a lane slot: the job's
	// dispatch-pending reservation is already published (r17 TK-30, 18.34
	// single-job variant).
	dispatchEntry func(jobID string)
	// beforeRun runs once the job holds its lane slot, just before the job
	// runs. With a lane of one, it holds the later of two collected jobs
	// between the earlier job's run and its own (18.34 batch variant).
	beforeRun func(jobID string)
	// saveStore, when it returns an error, makes the store save fail with it
	// before anything is written (the real cron-save boundary: scenario 1.15
	// and the collection-save failure of 18.40).
	saveStore func() error
	// beforeDispatch runs like dispatchEntry, with the lane context the run
	// will receive: the job is collected and queued (JobDispatchInFlight)
	// and has not started (T10 E0, S-H.15).
	beforeDispatch func(ctx context.Context, jobID string)
	// firstCollection runs once, just before Activate's first due-collection
	// (T10 FR-03 boot observer, S-B.4).
	firstCollection func()
}

// SetBeforeDispatchTestHook installs fn as the before-dispatch barrier (see
// cronTestHooks.beforeDispatch); nil removes it. Test-only; production never
// sets one.
func (cs *CronService) SetBeforeDispatchTestHook(fn func(ctx context.Context, jobID string)) {
	cs.mu.Lock()
	cs.testHooks.beforeDispatch = fn
	cs.mu.Unlock()
}

// SetFirstCollectionTestHook installs fn as the first-collection observer
// (see cronTestHooks.firstCollection); nil removes it. Test-only; production
// never sets one.
func (cs *CronService) SetFirstCollectionTestHook(fn func()) {
	cs.mu.Lock()
	cs.testHooks.firstCollection = fn
	cs.mu.Unlock()
}

func (cs *CronService) beforeDispatchHook() func(context.Context, string) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.testHooks.beforeDispatch
}

func (cs *CronService) firstCollectionHook() func() {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.testHooks.firstCollection
}

// SetDispatchEntryTestHook installs fn as the dispatch-entry barrier (see
// cronTestHooks.dispatchEntry) and returns a function restoring the previous
// hook. Test-only; production never sets one.
func (cs *CronService) SetDispatchEntryTestHook(fn func(jobID string)) (restore func()) {
	cs.mu.Lock()
	prev := cs.testHooks.dispatchEntry
	cs.testHooks.dispatchEntry = fn
	cs.mu.Unlock()
	return func() {
		cs.mu.Lock()
		cs.testHooks.dispatchEntry = prev
		cs.mu.Unlock()
	}
}

// SetBeforeRunTestHook installs fn as the before-run barrier (see
// cronTestHooks.beforeRun) and returns a function restoring the previous
// hook. Test-only; production never sets one.
func (cs *CronService) SetBeforeRunTestHook(fn func(jobID string)) (restore func()) {
	cs.mu.Lock()
	prev := cs.testHooks.beforeRun
	cs.testHooks.beforeRun = fn
	cs.mu.Unlock()
	return func() {
		cs.mu.Lock()
		cs.testHooks.beforeRun = prev
		cs.mu.Unlock()
	}
}

// SetSaveStoreTestHook installs fn as the store-save fault (see
// cronTestHooks.saveStore) and returns a function restoring the previous
// hook. Test-only; production never sets one.
func (cs *CronService) SetSaveStoreTestHook(fn func() error) (restore func()) {
	cs.mu.Lock()
	prev := cs.testHooks.saveStore
	cs.testHooks.saveStore = fn
	cs.mu.Unlock()
	return func() {
		cs.mu.Lock()
		cs.testHooks.saveStore = prev
		cs.mu.Unlock()
	}
}

// dispatchEntryHook and beforeRunHook read a hook under the read lock; the
// caller runs it with no lock held.
func (cs *CronService) dispatchEntryHook() func(string) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.testHooks.dispatchEntry
}

func (cs *CronService) beforeRunHook() func(string) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.testHooks.beforeRun
}
