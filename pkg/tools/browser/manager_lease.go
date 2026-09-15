// manager_lease.go: Workspace lease and ownership of a browser instance - attach to the pool/coordinator, the browsing key, the write lease, and which tab set a turn addresses.

package browser

import (
	"time"
)

// AttachSharedChrome wires this manager to the coordinator that owns its
// browsing key's Chrome (ADR-043, re-keyed by ADR-075 FR-001). When set,
// ensureStarted asks that coordinator to launch/provide the key's Chrome. A
// nil coordinator (the default for direct/test construction) keeps the legacy
// per-manager ExecAllocator behavior.
//
// key identifies this manager to the coordinator. It is the BROWSING KEY, not
// an agent id (ADR-075 FR-001): there is one manager, one Chrome and one
// profile directory per workspace, and N agents on that workspace share it, so
// the coordinator's Register/Release/RemoveAgent bookkeeping is keyed by the
// browser rather than by whichever agent happened to touch it first.
func (m *BrowserManager) AttachSharedChrome(coordinator *BrowserCoordinator, key BrowsingKey) {
	m.coordinator = coordinator
	m.key = key
	m.agentID = key.String()
}

// AttachPool wires this manager to the per-workspace browser pool (ADR-075
// FR-037) instead of to one coordinator directly. This is production's path.
//
// The difference that matters: with a pool, WHICH Chrome this manager drives
// is resolved on every ensureStarted rather than fixed at attach time. That is
// what lets the pool close an idle browser, or evict one under memory
// pressure, and have the next tool call quietly bring a fresh one up from the
// same profile directory — the agent sees a slower call, not an error.
func (m *BrowserManager) AttachPool(pool *BrowserPool, key BrowsingKey) {
	m.pool = pool
	m.key = key
	m.agentID = key.String()
}

// OperatorSessionID is the manager-level session id naming the WORKSPACE-OWNED
// tab set — the tabs the operator opened through the live panel, visible to
// every agent on this workspace (ADR-075 §0.2a).
//
// It is the exported seam the gateway addresses instead of the deleted
// shared session constant. Exported rather than exposing sessionKey because
// the gateway has no business minting an arbitrary (key, owner) pair.
//
// It is NOT the answer to "which tab set should the live panel drive" — that
// is PanelTabSetID below, and hardwiring this one there is issue #671. Use it
// where the caller genuinely has no chat context (the boot-time warm-up) or
// as the explicit no-chat fallback.
func (m *BrowserManager) OperatorSessionID() string {
	return sessionKey(m.key, TabOwnerWorkspace())
}

// PanelTabSetID resolves the manager-level session id the LIVE PANEL should
// drive for a viewer watching the given chat (issue #671).
//
// It is the deliberate MIRROR IMAGE of focusedTabSet, and that is the whole
// point: panel and agent must always resolve to the same tab set, so an
// operator watching a chat sees the tab the agent in that chat is actually
// driving. The two rules read as one:
//
//	agent (focusedTabSet):  own set has no tabs AND the operator's has some
//	                        -> address the operator's; otherwise its own.
//	panel (here):           this chat's set HAS tabs -> drive that;
//	                        otherwise the operator's.
//
// Walk the two states they produce together and they never diverge. Operator
// set EMPTY: the agent's first browse stays in its own set (focusedTabSet has
// nothing to divert onto), so the chat HAS tabs and the panel follows it here.
// Operator set NON-EMPTY: the agent diverts onto the operator's set, so the
// chat still has no tabs of its own and the panel resolves to the operator
// too.
//
// #671 is what the missing half cost: with an empty operator set the panel
// asked for OperatorSessionID() anyway, which LAZILY CREATED a workspace-owned
// tab parked on /browser-start and captured that — while the agent browsed,
// successfully and truthfully, in the chat's own set. Nothing failed. The
// operator was simply shown a different tab, with no error anywhere.
//
// chatSessionID is the CHAT (transcript) session id the client is watching.
// Empty — or anything TabOwnerSession refuses — resolves to the operator's
// set, which is exactly today's behaviour for a caller with no chat context.
//
// Holds m.mu only across the map lookup (hasTabsLocked), never across I/O, and
// takes no other lock while holding it.
func (m *BrowserManager) PanelTabSetID(chatSessionID string) string {
	owner, err := TabOwnerSession(chatSessionID)
	if err != nil {
		return m.OperatorSessionID()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hasTabsLocked(owner) {
		return sessionKey(m.key, owner)
	}
	return sessionKey(m.key, TabOwnerWorkspace())
}

// BrowsingKey reports which browser this manager IS.
func (m *BrowserManager) BrowsingKey() BrowsingKey { return m.key }

// focusedTabSet reports which tab set a turn whose OWN set is `home` currently
// addresses — its own, or the operator's workspace-owned set it has taken over
// (ADR-075 D1.9b ruling 1, FR-070).
//
// Every browser tool resolves through this, on the ONE path resolveTurn takes,
// which is why "take over the operator's browsing" is a property of the turn
// rather than of eleven separate tools each having to remember it.
//
// The liveness check is load-bearing, not defensive. Without it a session that
// took over an operator tab set which was later reaped keeps pointing at a
// dead key, and the next call LAZILY RECREATES a workspace-owned set with a
// blank tab in it — an agent silently browsing in the operator's name, in a
// window the operator is not looking at.
func (m *BrowserManager) focusedTabSet(home TabOwner) TabOwner {
	if home.IsZero() {
		return home
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	focused, ok := m.tabFocus[home.String()]
	if !ok || focused.IsZero() || focused == home {
		// No explicit take-over recorded. If this session has NO tabs of its
		// own and the operator's set HAS tabs, address the operator's set.
		//
		// This is what "take over" means to the person asking. UAT found the
		// headline scenario still failing without it: the operator browses to
		// a login form, asks the agent to type into it, and the agent types
		// into a blank tab of its own while the panel — the operator's only
		// window onto that browser — shows the field still empty. The agent
		// then reports success. Reproduced 4/4. That is the silent-wrong-action
		// failure this whole design exists to remove, surviving inside the fix
		// for it.
		//
		// The mechanism was already right: resolveTabIndex could reach the
		// operator's set, and browser_switch_tab could take it over. Only the
		// DEFAULT was wrong, and a default that requires the model to know it
		// must switch first is not a default a user can rely on.
		//
		// Deliberately narrow. It applies only when this session has nothing
		// of its own, so an agent that has been browsing is never diverted
		// mid-task, and it grants no new reach: the human-control lock still
		// runs on the resolved owner, so an agent still stands down while a
		// human is actually driving.
		if home != TabOwnerWorkspace() && !m.hasTabsLocked(home) && m.hasTabsLocked(TabOwnerWorkspace()) {
			return TabOwnerWorkspace()
		}
		return home
	}
	if _, live := m.sessions[sessionKey(m.key, focused)]; !live {
		delete(m.tabFocus, home.String())
		return home
	}
	return focused
}

// hasTabsLocked reports whether owner's set exists AND holds at least one tab.
// Caller must hold m.mu.
//
// "Exists but is empty" and "does not exist" are deliberately the same answer
// here: neither is somewhere a call can usefully land.
func (m *BrowserManager) hasTabsLocked(owner TabOwner) bool {
	se, ok := m.sessions[sessionKey(m.key, owner)]
	if !ok {
		return false
	}
	return len(snapshotTabsLocked(se)) > 0
}

// focusTabSet points `home`'s next call at `target`. Called by
// browser_switch_tab AFTER a successful switch — acquisition is by ACTING on
// the tab, so there is nothing to record until the action has happened
// (FR-070).
//
// Pointing a session back at its own set DELETES the entry rather than storing
// it, so the map holds only the sessions that have actually taken something
// over.
func (m *BrowserManager) focusTabSet(home, target TabOwner) {
	if home.IsZero() || target.IsZero() {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if target == home {
		delete(m.tabFocus, home.String())
		return
	}
	if m.tabFocus == nil {
		m.tabFocus = make(map[string]TabOwner)
	}
	m.tabFocus[home.String()] = target
}

// writeLeases returns this browser's write-lease table (§14). Not guarded by
// m.mu — the table owns its own mutex, and the lock order is
// writeLease -> pool.mu -> m.mu, never the reverse.
func (m *BrowserManager) writeLeases() *writeLeaseTable { return &m.leases }

// leaseWait is the bound acquireWrite retries within. Reads the
// operator-configured, already-CLAMPED value (FR-023a) and falls back to the
// package default when unset.
func (m *BrowserManager) leaseWait() time.Duration {
	m.mu.Lock()
	d := m.cfg.LeaseWait
	m.mu.Unlock()
	if d <= 0 {
		return leaseWaitTimeout
	}
	return d
}

// AgentID returns the agent identifier this manager was attached to via
// AttachSharedChrome (empty in remote-CDP-override mode or before
// attachment). WebRTC build (W2-A): the capture session needs it for
// logging/audit context without reaching into an unexported field from
// outside this package.
func (m *BrowserManager) AgentID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.agentID
}

// Coordinator returns the shared-Chrome coordinator this manager is attached
// to (nil in remote-CDP-override mode or the no-coordinator test fallback —
// see AttachSharedChrome). WebRTC build (W2-A): the capture session needs it
// to load/verify the capture extension (BrowserCoordinator.LoadExtension)
// before creating the encoder page.
func (m *BrowserManager) Coordinator() *BrowserCoordinator {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.coordinator
}

// attachedToSharedChrome reports whether this manager is wired to the
// workspace's Chrome AT ALL — which is a different question from "has that
// Chrome been launched yet", and the one CaptureVideoCapability means to ask.
//
// Both attachment forms count:
//
//   - AttachSharedChrome pins one coordinator at attach time, so m.coordinator
//     is non-nil immediately, before any Chrome exists.
//   - AttachPool (production's only path, pkg/agent/loop.go's browserFactory)
//     pins a POOL instead, and deliberately leaves m.coordinator nil: WHICH
//     Chrome this manager drives is resolved on every ensureStarted, so the
//     coordinator field is a cache that is only populated once the pool has
//     actually launched (or re-launched) the workspace's browser.
//
// Reading m.coordinator alone therefore stopped meaning "attached" the moment
// production moved to the pool — it started meaning "Chrome has already been
// launched at least once", which made a perfectly capable, pool-attached
// manager classify not_capable until something else happened to start it.
func (m *BrowserManager) attachedToSharedChrome() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.coordinator != nil || m.pool != nil
}
