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

// WorkerJoinObservation is what the worker-join observation hook receives
// at each join stage of Reload (spec S-24, B-01). Every flag is read from
// real state at the moment the stage fires, without blocking.
type WorkerJoinObservation struct {
	Instance string
	Phase    string // "joining" or "joined"
	// WriteLockHeld: a non-blocking m.mu.TryRLock failed, so a writer holds
	// m.mu (or waits for it). Reload is single-flight and RegisterChannel is
	// the only other writer, so in a test that registers nothing during the
	// reload this is true only while Reload really holds the write lock. A
	// successful probe releases its read lock at once.
	WriteLockHeld bool
	// OldWorkerDone / OldMediaWorkerDone: the OLD generation's w.done and
	// w.mediaDone, captured before Reload replaces them, are closed (a
	// non-blocking receive succeeds), i.e. that worker goroutine has exited.
	OldWorkerDone      bool
	OldMediaWorkerDone bool
}

// SetWorkerJoinObservationHookForTest installs fn, called at the same two
// points as the worker-join hook ("joining", "joined") with a
// WorkerJoinObservation. The probes run only while fn is installed. fn has
// the same limits as the worker-join hook: it runs where Reload may hold the
// write lock, so it must only record or signal, never block or call the
// Manager.
func (m *Manager) SetWorkerJoinObservationHookForTest(fn func(WorkerJoinObservation)) {
	if fn == nil {
		m.workerJoinObservationHook.Store(nil)
		return
	}
	m.workerJoinObservationHook.Store(&fn)
}

// writeLockHeldProbe reports whether a writer holds (or waits for) m.mu,
// without blocking.
func (m *Manager) writeLockHeldProbe() bool {
	if m.mu.TryRLock() {
		m.mu.RUnlock()
		return false
	}
	return true
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

// observeWorkerJoin reports one join stage; done and mediaDone are the old
// generation's completion channels (never replaced before "joined").
func (m *Manager) observeWorkerJoin(instance, phase string, done, mediaDone <-chan struct{}) {
	if fn := m.workerJoinHook.Load(); fn != nil {
		(*fn)(instance, phase)
	}
	if fn := m.workerJoinObservationHook.Load(); fn != nil {
		(*fn)(WorkerJoinObservation{
			Instance:           instance,
			Phase:              phase,
			WriteLockHeld:      m.writeLockHeldProbe(),
			OldWorkerDone:      isClosed(done),
			OldMediaWorkerDone: isClosed(mediaDone),
		})
	}
}

// isClosed reports, without blocking, whether ch is closed. The channels it
// is used on are only ever closed, never sent on.
func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
