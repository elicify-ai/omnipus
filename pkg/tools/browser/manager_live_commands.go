package browser

import (
	"context"
	"fmt"
	"time"
)

const liveTabCommandTimeout = 5 * time.Second

type liveTabCommandGate struct {
	gate  chan struct{}
	users int
}

// acquireLiveTabCommand serializes an attached tab set's UI operations, including
// their callbacks. It is separate from the live input gate: callbacks can acquire
// that gate to publish a new target. Never wait while holding BrowserManager.mu.
func (m *BrowserManager) acquireLiveTabCommand(ctx context.Context, sessionID string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.tabCommands == nil {
		m.tabCommands = make(map[string]*liveTabCommandGate)
	}
	gate := m.tabCommands[sessionID]
	if gate == nil {
		gate = &liveTabCommandGate{gate: make(chan struct{}, 1)}
		m.tabCommands[sessionID] = gate
	}
	gate.users++
	m.mu.Unlock()
	drop := func() {
		m.mu.Lock()
		gate.users--
		if gate.users == 0 {
			delete(m.tabCommands, sessionID)
		}
		m.mu.Unlock()
	}
	select {
	case gate.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-gate.gate
			drop()
			return nil, err
		}
		return func() { <-gate.gate; drop() }, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}

// liveTabSessionLocked never starts or recreates a browsing context. These APIs
// are for an already-attached viewer; legacy OpenTab retains lazy startup.
func (m *BrowserManager) liveTabSessionLocked(ctx context.Context, sessionID string) (*sessionEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	se := m.sessions[sessionID]
	if !m.started || se == nil || len(se.tabs) == 0 || se.browserCtx == nil || se.browserCtx.Err() != nil {
		return nil, fmt.Errorf("browser: no active session for attached tab set %q", sessionID)
	}
	return se, nil
}

// liveTabFocus applies the remaining command lifetime to existing targets;
// canceling these child contexts never cancels a healthy target's lifetime.
func (m *BrowserManager) liveTabFocus(ctx context.Context, sessionID string, current, previous context.Context) error {
	var foregroundErr error
	focus := func(target context.Context, activate bool) {
		if target == nil {
			return
		}
		child, cancel := context.WithCancel(target)
		stop := context.AfterFunc(ctx, cancel)
		if ctx.Err() == nil {
			if activate {
				foregroundErr = m.runTabFocusCDP(child, foregroundTabActions()...)
			} else {
				m.releaseTabFocusInChrome(child, sessionID)
			}
		}
		stop()
		cancel()
	}
	focus(current, true)
	focus(previous, false)
	if err := ctx.Err(); err != nil {
		return err
	}
	return foregroundErr
}

// createLiveTab ties only creation to the caller. After successful creation the
// disposable parent scope belongs to the tab and outlives the command deadline.
func (m *BrowserManager) createLiveTab(ctx, parent context.Context) (*tabEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scope, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(ctx, cancel)
	tab, err := m.createTab(scope, "")
	stop()
	if callerErr := ctx.Err(); callerErr != nil {
		err = callerErr
	}
	if err != nil {
		if tab != nil {
			tab.cancel()
		}
		cancel()
		return nil, err
	}
	originalCancel := tab.cancel
	tab.cancel = func() { originalCancel(); cancel() }
	return tab, nil
}

// SwitchTabContext changes an existing attached tab set, honoring caller
// cancellation before mutation and during foreground CDP work. Once committed,
// the captured model snapshot is broadcast even if its foreground call expires.
func (m *BrowserManager) SwitchTabContext(caller context.Context, sessionID string, index int) (Tab, error) {
	ctx, cancel := context.WithTimeout(caller, liveTabCommandTimeout)
	defer cancel()
	release, err := m.acquireLiveTabCommand(ctx, sessionID)
	if err != nil {
		return Tab{}, err
	}
	defer release()
	m.mu.Lock()
	se, err := m.liveTabSessionLocked(ctx, sessionID)
	if err == nil {
		se, err = m.lookupTabLocked(sessionID, index)
	}
	if err != nil {
		m.mu.Unlock()
		return Tab{}, err
	}
	moved := se.activeIdx != index
	var previous context.Context
	if moved && se.activeIdx >= 0 && se.activeIdx < len(se.tabs) {
		previous = se.tabs[se.activeIdx].ctx
	}
	se.activeIdx = index
	m.touchTabLocked(se.tabs[index])
	tabs := snapshotTabsLocked(se)
	current := se.tabs[index].ctx
	m.mu.Unlock()
	err = m.liveTabFocus(ctx, sessionID, current, previous)
	m.notifyTabsChanged(sessionID, tabs, index)
	if !moved {
		m.recaptureForTabChange()
	}
	return tabs[index], err
}

// OpenTabContext opens a target only within an existing attached tab set. A
// stale attachment fails rather than launching/recreating a different browser.
func (m *BrowserManager) OpenTabContext(caller context.Context, sessionID string) (Tab, error) {
	ctx, cancel := context.WithTimeout(caller, liveTabCommandTimeout)
	defer cancel()
	release, err := m.acquireLiveTabCommand(ctx, sessionID)
	if err != nil {
		return Tab{}, err
	}
	defer release()
	m.mu.Lock()
	expected, err := m.liveTabSessionLocked(ctx, sessionID)
	if err == nil && m.memoryRefusesTabOpenLocked() {
		err = errMemoryPressureTabOpen
	}
	if err != nil {
		m.mu.Unlock()
		return Tab{}, err
	}
	parent := expected.browserCtx
	m.mu.Unlock()
	tab, err := m.createLiveTab(ctx, parent)
	if err != nil {
		return Tab{}, err
	}
	m.mu.Lock()
	se, err := m.liveTabSessionLocked(ctx, sessionID)
	if err == nil && se != expected {
		err = fmt.Errorf("browser: attachment changed while opening tab")
	}
	if err == nil && m.memoryRefusesTabOpenLocked() {
		err = errMemoryPressureTabOpen
	}
	if err != nil {
		m.mu.Unlock()
		tab.cancel()
		return Tab{}, err
	}
	var previous context.Context
	if se.activeIdx >= 0 && se.activeIdx < len(se.tabs) {
		previous = se.tabs[se.activeIdx].ctx
	}
	se.tabs = append(se.tabs, tab)
	se.activeIdx = len(se.tabs) - 1
	m.installTargetListenerLocked(sessionID, se)
	m.syncDialogListenersLocked(sessionID, se)
	tabs := snapshotTabsLocked(se)
	index := se.activeIdx
	m.mu.Unlock()
	err = m.liveTabFocus(ctx, sessionID, tab.ctx, previous)
	m.notifyTabsChanged(sessionID, tabs, index)
	return tabs[index], err
}

// CloseTabContext preserves the final target until a replacement is ready to
// commit. A canceled creation never destroys that last usable target. Already
// committed closes are not replayed or rolled back after a foreground timeout.
func (m *BrowserManager) CloseTabContext(caller context.Context, sessionID string, index int) ([]Tab, int, error) {
	ctx, cancel := context.WithTimeout(caller, liveTabCommandTimeout)
	defer cancel()
	release, err := m.acquireLiveTabCommand(ctx, sessionID)
	if err != nil {
		return nil, 0, err
	}
	defer release()
	m.mu.Lock()
	se, err := m.liveTabSessionLocked(ctx, sessionID)
	if err == nil {
		se, err = m.lookupTabLocked(sessionID, index)
	}
	if err != nil {
		m.mu.Unlock()
		return nil, 0, err
	}
	closing := se.tabs[index]
	if len(se.tabs) == 1 {
		if m.memoryRefusesTabOpenLocked() {
			m.mu.Unlock()
			return nil, 0, errMemoryPressureTabOpen
		}
		expected, parent := se, se.browserCtx
		m.mu.Unlock()
		replacement, createErr := m.createLiveTab(ctx, parent)
		if createErr != nil {
			return nil, 0, createErr
		}
		m.mu.Lock()
		se, err = m.liveTabSessionLocked(ctx, sessionID)
		if err == nil && (se != expected || len(se.tabs) != 1 || se.tabs[0] != closing) {
			err = fmt.Errorf("browser: tab set changed while preparing final-tab replacement")
		}
		if err != nil {
			m.mu.Unlock()
			replacement.cancel()
			return nil, 0, err
		}
		se.tabs = []*tabEntry{replacement}
		se.activeIdx = 0
	} else {
		se.tabs = append(se.tabs[:index], se.tabs[index+1:]...)
		switch {
		case se.activeIdx == index && index >= len(se.tabs):
			se.activeIdx = len(se.tabs) - 1
		case se.activeIdx > index:
			se.activeIdx--
		}
	}
	m.installTargetListenerLocked(sessionID, se)
	m.syncDialogListenersLocked(sessionID, se)
	tabs := snapshotTabsLocked(se)
	active := se.activeIdx
	current := se.tabs[active].ctx
	m.mu.Unlock()
	closing.cancel()
	err = m.liveTabFocus(ctx, sessionID, current, nil)
	m.notifyTabsChanged(sessionID, tabs, active)
	return tabs, active, err
}
