package browser

import "errors"

var errBrowserSessionChanged = errors.New("browser: session changed during target operation")

// retireTabCommandsLocked invalidates in-flight creation and admission without
// waiting for the operation that lifecycle removal is intended to interrupt.
func (m *BrowserManager) retireTabCommandsLocked(sessionID string) {
	delete(m.pending, sessionID)
	delete(m.pendingTabNotifications, sessionID)
	if gate := m.tabCommands[sessionID]; gate != nil {
		gate.retired = true
	}
}

func (m *BrowserManager) discardLifecycleTarget(sessionID string, tab *tabEntry) {
	if tab != nil && tab.cancel != nil {
		cancelBounded(tab.cancel, map[string]any{"session_id": sessionID, "origin": "retired_target_completion"})
	}
}

// tryAcquireTabCommandLocked is used by the idle sweep: a busy target is left
// for another sweep, never interrupted or queued behind from the reaper.
func (m *BrowserManager) tryAcquireTabCommandLocked(sessionID string) func() {
	if existing := m.tabCommands[sessionID]; existing != nil {
		return nil
	}
	if m.tabCommands == nil {
		m.tabCommands = make(map[string]*liveTabCommandGate)
	}
	gate := &liveTabCommandGate{gate: make(chan struct{}, 1), users: 1}
	gate.gate <- struct{}{}
	m.tabCommands[sessionID] = gate
	return func() {
		<-gate.gate
		m.mu.Lock()
		gate.users--
		if gate.users == 0 {
			delete(m.tabCommands, sessionID)
		}
		m.mu.Unlock()
	}
}

type reapedTabNotification struct { // not-wire-format: deferred manager observer work.
	sessionID string
	session   *sessionEntry
	release   func()
}

func (m *BrowserManager) publishReapedTabSnapshot(notification reapedTabNotification) {
	defer notification.release()
	m.mu.Lock()
	current := m.sessions[notification.sessionID]
	var tabs []Tab
	active := 0
	if current == notification.session {
		tabs = snapshotTabsLocked(current)
		active = current.activeIdx
	}
	m.mu.Unlock()
	m.notifyTabsChanged(notification.sessionID, tabs, active)
}
