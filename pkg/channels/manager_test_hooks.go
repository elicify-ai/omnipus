package channels

// Test-only observation points of the start check and Reload
// (channels-spec-r8 T-29/S-24, T-24b/S-89). Nil is the production default
// and changes nothing; each hook is a passive acknowledgement. Tests install
// a hook before the work and clear it (nil) after joining every participant.

import "github.com/elicify-ai/omnipus/pkg/bus"

// The stages a reload write-lock hook receives.
const (
	reloadLockAcquired   = "acquired"   // Reload took m.mu for writing
	reloadLockReacquired = "reacquired" // Reload took m.mu again after the dispatcher handoff
)

// The phases a worker-join hook receives.
const (
	workerJoinJoining = "joining" // the old generation is cancelled; the join starts
	workerJoinJoined  = "joined"  // the old worker goroutines have exited
)

// SetBeforeDequeueStartCheckHookForTest installs fn, called by a worker for
// an Operation=start message immediately before the dequeue start check
// (FR-044), after the limiter wait. No manager lock is held while it runs,
// so it may block to pause the message right before the check.
func (m *Manager) SetBeforeDequeueStartCheckHookForTest(fn func(msg bus.OutboundMessage)) {
	if fn == nil {
		m.beforeDequeueStartHook.Store(nil)
		return
	}
	m.beforeDequeueStartHook.Store(&fn)
}

// SetReloadWriteLockHookForTest installs fn, called by Reload each time it
// holds the manager write lock: "acquired" right after Reload takes it, and
// "reacquired" after the dispatcher handoff. fn runs WITH the write lock
// held: it must only record or signal (for example a buffered channel send)
// and must never block or call a Manager method.
func (m *Manager) SetReloadWriteLockHookForTest(fn func(stage string)) {
	if fn == nil {
		m.reloadWriteLockHook.Store(nil)
		return
	}
	m.reloadWriteLockHook.Store(&fn)
}

// SetWorkerJoinHookForTest installs fn, called by Reload around the join of
// an existing instance's cancelled worker generation: "joining" before it
// waits for the old worker goroutines, "joined" once both exited. Reload
// holds the manager write lock here, so fn has the same limits as the
// reload write-lock hook.
func (m *Manager) SetWorkerJoinHookForTest(fn func(instance, phase string)) {
	if fn == nil {
		m.workerJoinHook.Store(nil)
		return
	}
	m.workerJoinHook.Store(&fn)
}

func (m *Manager) observeBeforeDequeueStart(msg bus.OutboundMessage) {
	if fn := m.beforeDequeueStartHook.Load(); fn != nil {
		(*fn)(msg)
	}
}

func (m *Manager) observeReloadWriteLock(stage string) {
	if fn := m.reloadWriteLockHook.Load(); fn != nil {
		(*fn)(stage)
	}
}

func (m *Manager) observeWorkerJoin(instance, phase string) {
	if fn := m.workerJoinHook.Load(); fn != nil {
		(*fn)(instance, phase)
	}
}
