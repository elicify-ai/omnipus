// Omnipus — post-success, memory-only session deletion notification (#1343)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"fmt"
	"log/slog"
)

// SetSessionDeletedObserver replaces this store's optional in-process cleanup
// observer. It must not read/write session storage, publish frames or resume
// work. DeleteSession and RetentionSweep invoke it only after successful whole-
// session removal and after releasing their session shard locks.
// Nil disables it. By design, cleanup cannot turn a successful deletion into
// a failure: a panic is recovered and logged, never returned to the caller.
// A fresh store on restart lists no deleted row, so its durable-state consumers
// cannot rehydrate that deleted session's pending interaction state.
func (us *UnifiedStore) SetSessionDeletedObserver(observer func(sessionID string)) {
	us.sessionDeletedMu.Lock()
	us.sessionDeletedObserver = observer
	us.sessionDeletedMu.Unlock()
}

func (us *UnifiedStore) notifySessionDeleted(sessionID string) {
	us.sessionDeletedMu.RLock()
	observer := us.sessionDeletedObserver
	us.sessionDeletedMu.RUnlock()
	if observer == nil {
		return
	}
	defer func() {
		if failure := recover(); failure != nil {
			// The session is already gone. Cleanup is failure-silent by the
			// explicit lifecycle contract, not an unreported durable failure.
			// Do not log the panic value: it may contain interaction content.
			slog.Error("session deletion observer panicked; durable deletion succeeded",
				"session_id", sessionID, "panic_type", fmt.Sprintf("%T", failure))
		}
	}()
	observer(sessionID)
}
