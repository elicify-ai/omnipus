package browser

import (
	"context"
	"fmt"
)

// AttachContext limits tab admission by PageTimeout and the original caller.
// Startup uses only the caller's remaining lifetime. Once accepted, the viewer
// and target outlive that operation. onTabs must enqueue a bounded snapshot;
// it runs under target admission so its initial state precedes later mutations.
func (r *LiveViewRegistry) AttachContext(caller context.Context, sessionID, viewerID string, onStatus StatusSink, onControl ControlSink, onTabs TabsSink) (controlled bool, err error) {
	sessionID = r.resolveSessionID(sessionID)
	if viewerID == "" {
		return false, fmt.Errorf("browser live: viewer id is required")
	}
	timeout := r.mgr.PageTimeout()
	if timeout <= 0 {
		timeout = liveTabCommandTimeout
	}
	admission, cancelAdmission := context.WithTimeout(caller, timeout)
	release, err := r.mgr.acquireLiveTabCommand(admission, sessionID)
	cancelAdmission()
	if err != nil {
		return false, err
	}

	var lv *LiveView
	registered := false
	defer func() {
		// Count cleanup happens before another target/session can be created.
		// Input and control callbacks run only after releasing target admission.
		if err != nil && registered {
			r.mgr.ViewerDetached(sessionID)
		}
		release()
		if err != nil && registered {
			lv.detach(viewerID)
		}
	}()
	// This is the same startup body as SessionContext, with admission already
	// owned. Keeping one gate closes the resolution-to-registration target race.
	if _, err = r.mgr.sessionUnderGate(caller, sessionID); err != nil {
		return false, fmt.Errorf("browser live: cannot resolve session %q: %w", sessionID, err)
	}
	tabCtx, targetID, err := r.mgr.activeTargetSnapshot(sessionID)
	if err != nil {
		return false, err
	}
	if err = caller.Err(); err != nil {
		return false, err
	}
	lv = r.view(sessionID)
	controlled, err = lv.attach(tabCtx, viewerID, onStatus, onControl, onTabs)
	if err != nil {
		return false, err
	}
	r.mgr.ViewerAttached(sessionID)
	registered = true
	if onTabs != nil {
		if state, tabs, active, tabsErr := r.mgr.ListTabsState(sessionID); tabsErr == nil && state == TabStateOpen {
			onTabs(tabs, active)
		}
	}
	if err = caller.Err(); err != nil {
		return false, err
	}
	current, currentID, err := r.mgr.activeTargetSnapshot(sessionID)
	if err != nil {
		return false, err
	}
	lv.mu.Lock()
	bound := lv.tabCtx
	lv.mu.Unlock()
	if current != tabCtx || currentID != targetID || bound != tabCtx || tabCtx.Err() != nil {
		return false, errBrowserSessionChanged
	}
	return controlled, nil
}
