package browser

import (
	"context"

	"github.com/chromedp/cdproto/target"
)

// clickTabSnapshot retains the original session and known targets before input;
// it is internal operation state, never a browser protocol or tool response.
type clickTabSnapshot struct {
	owner   *sessionEntry
	opener  target.ID
	tracked map[target.ID]struct{}
}

func (m *BrowserManager) snapshotClickTabs(sessionID string, clicked context.Context) (*clickTabSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	owner := m.sessions[sessionID]
	if owner == nil || owner.active() == nil || owner.active().ctx != clicked || !m.adoptionSessionCurrentLocked(sessionID, owner) {
		return nil, errBrowserSessionChanged
	}
	snapshot := &clickTabSnapshot{owner: owner, opener: owner.active().targetID, tracked: make(map[target.ID]struct{}, len(owner.tabs))}
	for _, tab := range owner.tabs {
		snapshot.tracked[tab.targetID] = struct{}{}
	}
	// A target already being attached before input is not caused by this click.
	// These IDs affect reporting only, never adoption ownership.
	for id := range m.pendingAdopt {
		snapshot.tracked[id] = struct{}{}
	}
	return snapshot, nil
}

func (m *BrowserManager) reconcileClickTabs(sessionID string, before *clickTabSnapshot) (ReconcileOutcome, error) {
	return m.reconcileTabs(sessionID, before)
}

func (before *clickTabSnapshot) reports(info *target.Info) bool {
	_, existed := before.tracked[info.TargetID]
	return !existed && info.OpenerID == before.opener
}

// The passive listener may finish before either the target-list snapshot or
// this pass's adoption call. Read its result without attaching a second time.
func (m *BrowserManager) completedClickAdoption(sessionID string, before *clickTabSnapshot, id target.ID) (tabAdoptResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.adoptionSessionCurrentLocked(sessionID, before.owner) {
		return tabAdoptResult{}, errBrowserSessionChanged
	}
	index := before.owner.indexOfTarget(id)
	if index < 0 {
		return tabAdoptResult{}, nil
	}
	tab := before.owner.tabs[index]
	return tabAdoptResult{Adopted: &Tab{Index: index, Title: tab.title, URL: tab.url, Active: index == before.owner.activeIdx}}, nil
}
