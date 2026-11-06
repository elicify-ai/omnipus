// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import "sync/atomic"

// afterTaskPersistHook is the test-only barrier inside WriteTaskReconciled:
// it runs after the task write has persisted and before the paired goal's
// synchronisation and the scheduler reconciliation, with the task's
// coordination lock still held (tool-parity spec r17 scenarios 1.13, 18.21
// and 18.22, test 12e). nil in production.
var afterTaskPersistHook atomic.Pointer[func(taskID string)]

// SetAfterTaskPersistTestHook installs fn as the in-lock barrier between a
// reconciled task write's persistence and its reconciliation, and returns a
// function restoring the previous hook. A second writer of the same task
// queues on the coordination lock while fn blocks. Test-only; production
// never sets one.
func SetAfterTaskPersistTestHook(fn func(taskID string)) (restore func()) {
	var next *func(string)
	if fn != nil {
		next = &fn
	}
	prev := afterTaskPersistHook.Swap(next)
	return func() { afterTaskPersistHook.Store(prev) }
}

func runAfterTaskPersistHook(taskID string) {
	if hook := afterTaskPersistHook.Load(); hook != nil {
		(*hook)(taskID)
	}
}
