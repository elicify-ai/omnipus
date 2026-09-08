package browser

import (
	"os"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

type poolRetirement struct {
	done     chan struct{}
	instance *chromeInstance
	startup  *startupCohort
	managers []*BrowserManager
}

type poolCloseMode uint8

const (
	poolCloseExplicit poolCloseMode = iota
	poolCloseEviction
	poolCloseIdle
)

// claimInstanceLocked holds every registered manager's admission mutex through
// the final eligibility check and removal. No other path locks multiple managers;
// pool.mu serializes claims. Callers never hold a manager mutex on entry.
func (p *BrowserPool) claimInstanceLocked(inst *chromeInstance, mode poolCloseMode) *poolRetirement {
	id := inst.key.String()
	if p.instances[id] != inst || p.retiring[id] != nil {
		return nil
	}
	var managers []*BrowserManager
	for m := range inst.mgrs {
		if m != nil {
			managers = append(managers, m)
		}
	}
	for _, m := range managers {
		m.mu.Lock()
	}
	defer func() {
		for _, m := range managers {
			m.mu.Unlock()
		}
	}()
	if mode != poolCloseExplicit {
		for _, m := range managers {
			if m.inFlight > 0 || m.liveViewersLockedForPool() > 0 {
				return nil
			}
			if mode == poolCloseIdle && m.totalTabCountLocked() > 0 {
				return nil
			}
		}
	}
	r := &poolRetirement{done: make(chan struct{}), instance: inst, managers: managers, startup: p.launching[id]}
	if p.retiring == nil {
		p.retiring = make(map[string]*poolRetirement)
	}
	p.retiring[id] = r
	if r.startup != nil {
		r.startup.cancel()
	}
	delete(p.instances, id)
	for _, m := range managers {
		m.started = false
	}
	return r
}

func (m *BrowserManager) liveViewersLockedForPool() int {
	var count int
	now := m.now()
	for _, se := range m.sessions {
		count += m.liveViewersLocked(se, now)
	}
	return count
}

// finishRetirement owns cleanup for one claimed key. The key stays unavailable
// until process exit, stale-manager cleanup and profile trimming all finish.
func (p *BrowserPool) finishRetirement(key BrowsingKey, r *poolRetirement, why string) {
	if r.startup != nil {
		r.startup.cancel()
		<-r.startup.done
	}
	if r.instance != nil {
		r.instance.coord.Shutdown()
		for _, m := range r.managers {
			m.invalidateConnection()
		}
	}
	_ = os.Remove(p.markerPathFor(key))
	logger.InfoCF("browser", "closed this workspace's browser (its profile is kept)", map[string]any{
		"workspace": key.WorkspaceID(), "why": why,
	})
	p.logUnboundedContinuousDriveOnce()
	p.TrimProfile(key)
	p.mu.Lock()
	delete(p.retiring, key.String())
	close(r.done)
	p.mu.Unlock()
}
