package task

import "sync/atomic"

// promoteInboxHook is the test-only barrier at the entry of
// PromoteInboxIfReleased, before the task's lock is taken: it holds the plan
// engine's promotion pass between its member snapshot and its promotion write
// (tool-parity spec r17 scenario 18.24, test 12d). nil in production.
var promoteInboxHook atomic.Pointer[func(taskID string)]

// SetPromoteInboxTestHook installs fn as the barrier between a promotion
// pass's snapshot and its promotion write, and returns a function restoring
// the previous hook. Test-only; production never sets one.
func SetPromoteInboxTestHook(fn func(taskID string)) (restore func()) {
	var next *func(string)
	if fn != nil {
		next = &fn
	}
	prev := promoteInboxHook.Swap(next)
	return func() { promoteInboxHook.Store(prev) }
}
