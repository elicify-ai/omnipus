package browser

import (
	"context"
	"fmt"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// activeTargetSnapshot is a read-only lookup; callers may already own the
// command gate. It must never launch or recreate a target.
func (m *BrowserManager) activeTargetSnapshot(sessionID string) (context.Context, target.ID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	se := m.sessions[sessionID]
	if se == nil || se.active() == nil || se.active().ctx == nil {
		return nil, "", fmt.Errorf("browser: no active target for session %q", sessionID)
	}
	tab := se.active()
	if err := tab.ctx.Err(); err != nil {
		return nil, "", fmt.Errorf("browser: active target for session %q unavailable: %w", sessionID, err)
	}
	return tab.ctx, tab.targetID, nil
}

// Legacy APIs have no caller context. Bound admission by their configured page
// timeout; after admission their existing operation budgets remain in force.
func (m *BrowserManager) acquireLegacyTabCommand(sessionID string) (func(), error) {
	timeout := m.PageTimeout()
	if timeout <= 0 {
		timeout = liveTabCommandTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return m.acquireLiveTabCommand(ctx, sessionID)
}

// queueTabNotificationLocked coalesces metadata while another target operation
// owns admission. The Chrome listener does not wait or invoke observers itself.
func (m *BrowserManager) queueTabNotificationLocked(sessionID string, se *sessionEntry) {
	if m.pendingTabNotifications == nil {
		m.pendingTabNotifications = make(map[string]*sessionEntry)
	}
	if m.pendingTabNotifications[sessionID] == se {
		return
	}
	m.pendingTabNotifications[sessionID] = se
	go m.publishCurrentTabSnapshot(sessionID, se)
}

func (m *BrowserManager) publishCurrentTabSnapshot(sessionID string, se *sessionEntry) {
	ctx := se.browserCtx
	if ctx == nil {
		ctx = context.Background()
	}
	release, err := m.acquireLiveTabCommand(ctx, sessionID)
	if err == nil {
		defer release()
	}
	m.mu.Lock()
	if m.pendingTabNotifications[sessionID] != se {
		m.mu.Unlock()
		return
	}
	delete(m.pendingTabNotifications, sessionID)
	if err != nil || m.sessions[sessionID] != se {
		m.mu.Unlock()
		return
	}
	tabs, active := snapshotTabsLocked(se), se.activeIdx
	m.mu.Unlock()
	m.notifyTabsChanged(sessionID, tabs, active)
}

// reconcileTargetSnapshot gates the bounded read phase. The caller releases it
// before waiting for individual adoptions, whose pending winner may itself be
// waiting for admission; retaining the gate across that wait would deadlock.
func (m *BrowserManager) reconcileTargetSnapshot(sessionID string) ([]*target.Info, map[target.ID]struct{}, error) {
	release, err := m.acquireLegacyTabCommand(sessionID)
	if err != nil {
		return nil, nil, err
	}
	defer release()

	m.mu.Lock()
	se, ok := m.sessions[sessionID]
	if !ok || len(se.tabs) == 0 {
		m.mu.Unlock()
		return nil, nil, nil
	}
	execCtx := se.active().ctx
	tracked := make(map[target.ID]struct{}, len(se.tabs))
	for _, t := range se.tabs {
		tracked[t.targetID] = struct{}{}
	}
	listTargets := m.listTargets
	m.mu.Unlock()

	if listTargets == nil {
		listTargets = chromedp.Targets
	}

	timeoutCtx, cancel := context.WithTimeout(execCtx, reconcileTargetListTimeout)
	infos, lerr := listTargets(timeoutCtx)
	cancel()
	if lerr != nil {
		return nil, nil, fmt.Errorf("browser: failed to list targets for reconcile: %w", lerr)
	}

	return infos, tracked, nil
}
