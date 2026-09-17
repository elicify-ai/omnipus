// loop_browser.go: Resolve a browser manager for an agent

package agent

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// BrowserResolveOutcome is a closed enum naming WHY a browser could not be
// resolved. "not registered" and "no workspace" are DIFFERENT operator-facing
// problems and were indistinguishable before ADR-075 — browser_inspect.go
// reported the former for both, so an operator whose agent simply was not on a
// workspace team was told browser tools had failed to register.
//
// There is deliberately NO BrowserResolvePoolFull: ADR-075 D1.5a deleted every
// counter, so the panel never has a capacity reason to render.
type BrowserResolveOutcome int

// BrowserManagerForKey returns (creating on first use) the manager that owns
// key's browser. Exactly one manager and one Chrome per key, process-wide —
// FR-001. There is no cap: ADR-075 D1.5a made live memory the only limit, and
// it is enforced at each tab open inside the manager (FR-060).
func (al *AgentLoop) BrowserManagerForKey(
	_ context.Context, key browser.BrowsingKey,
) (*browser.BrowserManager, error) {
	if key.IsZero() {
		return nil, browser.ErrNoBrowsingContext
	}
	al.mu.Lock()
	if mgr, ok := al.browserMgrs[key.String()]; ok && mgr != nil {
		al.mu.Unlock()
		return mgr, nil
	}
	factory := al.browserFactory
	al.mu.Unlock()
	if factory == nil {
		return nil, fmt.Errorf("browser: browser tools are not registered on this gateway")
	}
	mgr, err := factory(key)
	if err != nil {
		return nil, err
	}
	// Re-check under the lock: two turns on one workspace can reach here
	// concurrently, and the loser must DISCARD its manager rather than install
	// a second Chrome for the same key.
	al.mu.Lock()
	if existing, ok := al.browserMgrs[key.String()]; ok && existing != nil {
		al.mu.Unlock()
		mgr.Shutdown()
		return existing, nil
	}
	al.browserMgrs[key.String()] = mgr
	al.mu.Unlock()
	return mgr, nil
}

// rewireBrowserManagerForKey installs a freshly-configured manager for key,
// tearing down the one it replaces. Called once per key per reload (FR-026b).
//
// The teardown discipline is ADR-043's, unchanged, with agentID replaced by the
// browsing key: coordinator.Release drops the old manager's connection and
// bookkeeping WITHOUT killing Chrome or disposing the browser context (CRIT-002
// — the context persists so the new manager re-adopts it and login survives the
// save), and prior.Shutdown() additionally covers the explicit-cdp_url manager
// that never registered with the coordinator at all and would otherwise leak
// its allocator on every reload.
func (al *AgentLoop) rewireBrowserManagerForKey(
	key browser.BrowsingKey,
	factory func(browser.BrowsingKey) (*browser.BrowserManager, error),
) {
	if factory == nil {
		return
	}
	mgr, err := factory(key)
	if err != nil {
		logger.ErrorCF("agent", "Failed to create the browser for this workspace — "+
			"ensure Chromium/Chrome is installed or set tools.browser.cdp_url",
			map[string]any{"error": err.Error(), "browsing_key": key.String()})
		return
	}
	al.mu.Lock()
	prior := al.browserMgrs[key.String()]
	pool := al.browserPool
	al.browserMgrs[key.String()] = mgr
	al.mu.Unlock()
	if pool != nil {
		// Reload: drop the OLD manager's registration only. The Chrome
		// process and its profile directory survive, which is what makes a
		// Settings save cost nobody their login (FR-043).
		pool.Release(key, prior)
	}
	if prior != nil {
		// KEEP THIS SHUTDOWN. coordinator.go's doc calls Release "a full
		// substitute for the old prior.Shutdown() reload call", and that
		// sentence is true only when the pool instance HAS a coordinator:
		// BrowserPool.Release does no teardown of its own — it deletes mgr
		// from inst.mgrs and then calls inst.coord.Release, and only that
		// reaches dropConnection -> m.Shutdown(). With inst.coord nil (no
		// shared Chrome stood up yet) nothing is torn down at all, and the
		// prior manager's Chromium allocator leaks on every hot reload.
		// TestRegisterSharedTools_HotReload_ShutsDownReplacedBrowserManager
		// pins exactly that case and caught this being deleted.
		//
		// The double-Shutdown that used to panic the gateway with "close of
		// closed channel" on a Settings save was never this call's fault —
		// it was LiveViewRegistry.Shutdown not being idempotent, which is
		// fixed at the source (see its comment in pkg/tools/browser/live.go).
		// Every other step of BrowserManager.Shutdown was already idempotent
		// and its doc comment says so, so with the registry fixed the
		// coordinator-present path's second call is a safe no-op and the
		// coordinator-absent path is no longer a leak.
		prior.Shutdown()
		prior.InvalidateExecPathCache()
	}
}

// BrowserPool returns the per-workspace browser pool (ADR-075 FR-037), or nil
// before the first registration pass has built it. The gateway needs it for
// boot preprovision (FR-016c), for the one-minute sweep's whole-Chrome idle
// close (FR-040a) and for workspace-deletion disposal (FR-026).
func (al *AgentLoop) BrowserPool() *browser.BrowserPool {
	al.mu.Lock()
	defer al.mu.Unlock()
	return al.browserPool
}

// agentLoopBrowserResolver implements browser.ManagerResolver over
// ResolveBrowsingKey + BrowserManagerForKey. The interface is declared in
// pkg/tools/browser and implemented here because the import direction forbids
// the reverse.
type agentLoopBrowserResolver struct{ al *AgentLoop }

// browserResolver returns the browser.ManagerResolver every browser tool
// resolves its manager through, per Execute (FR-002a).
func (al *AgentLoop) browserResolver() browser.ManagerResolver {
	return &agentLoopBrowserResolver{al: al}
}

func (r *agentLoopBrowserResolver) ManagerFor(
	ctx context.Context,
) (*browser.BrowserManager, browser.BrowsingKey, browser.TabOwner, error) {
	// omnipusHome(), not al.homePath: workspace membership is resolved from
	// $OMNIPUS_HOME everywhere else in this package (wireWorkingDirInjectors,
	// resolveTurnWorkDirOrRefuse), and the browser must not disagree with the
	// work dir about which workspace a turn is rooted in. al.homePath is the
	// coordinator's ownership-marker root, which is a different question.
	key, err := browser.ResolveBrowsingKey(ctx, omnipusHome())
	if err != nil {
		return nil, browser.BrowsingKey{}, browser.TabOwner{}, err
	}
	// FR-080: the tab set is the SESSION's, keyed on transcriptSessionID and
	// never on routingSessionID (which a whole delegation subtree shares, so it
	// would merge every descendant's tabs into the root's). An empty transcript
	// session is a NAMED FAILURE, never a fall-through to the operator's
	// workspace-owned set.
	//
	// This is the turn's HOME tab set, which is not always the set the call
	// ACTS on. An agent reaches the operator's workspace-owned tabs by acting
	// on one browser_list_tabs showed it (FR-070 — implicit acquisition, no
	// tool, no policy entry, no wire field), and pkg/tools/browser's
	// resolveTurn resolves that: it is a property of the call, not of the turn,
	// so there is nothing for this resolver to decide. Do NOT "fix" this to
	// return TabOwnerWorkspace() under any condition — a transcript-less or
	// misrouted turn silently landing on the operator's tabs is the implicit
	// merge ErrNoTabOwner exists to prevent.
	owner, err := browser.TabOwnerSession(tools.ToolTranscriptSessionID(ctx))
	if err != nil {
		return nil, browser.BrowsingKey{}, browser.TabOwner{}, err
	}
	mgr, err := r.al.BrowserManagerForKey(ctx, key)
	if err != nil {
		return nil, browser.BrowsingKey{}, browser.TabOwner{}, err
	}
	return mgr, key, owner, nil
}

const (
	BrowserResolveOK BrowserResolveOutcome = iota
	// BrowserResolveNoWorkspace is browser.ErrNoBrowsingContext: this agent is
	// not rooted in a workspace, so it has no browser of its own.
	BrowserResolveNoWorkspace
	// BrowserResolveAmbiguous is FR-033: more than one candidate workspace and
	// no preference supplied. The browser REFUSES rather than tie-breaking,
	// because choosing would silently pick which set of live logins to act with.
	BrowserResolveAmbiguous
	// BrowserResolveNotRegistered means browser tools genuinely are not
	// registered for this agent.
	BrowserResolveNotRegistered
	// BrowserResolveLaunchFailed means the browser is addressable but could not
	// be created (config or SSRF wiring failure).
	BrowserResolveLaunchFailed
)

// BrowserManagerForAgent is RETAINED for the gateway. It resolves
// agentID -> BrowsingKey server-side using preferredWorkspaceID (from the
// attaching chat session's meta, FR-017) and delegates to BrowserManagerForKey.
//
// The second return distinguishes the failure reasons the panel must show
// differently (FR-008a) — it is NOT a bare bool, because "browser tools are not
// registered for this agent" and "this agent is not on a workspace team" need
// different operator advice and used to render identically.
func (al *AgentLoop) BrowserManagerForAgent(
	ctx context.Context, agentID, preferredWorkspaceID string,
) (*browser.BrowserManager, BrowserResolveOutcome) {
	al.mu.RLock()
	registered := al.browserRegisteredAgents[agentID]
	al.mu.RUnlock()
	if !registered {
		return nil, BrowserResolveNotRegistered
	}
	key, err := browser.ResolveBrowsingKeyForAgent(omnipusHome(), agentID, preferredWorkspaceID)
	if err != nil {
		// ResolveBrowsingKeyForAgent reports both "no workspace" and FR-033's
		// ambiguous multi-membership as ErrNoBrowsingContext (they are the same
		// answer to the agent: this turn has no browser of its own). The panel
		// wants them apart, and the only thing that separates them is whether
		// more than one workspace claims the agent.
		if ids, _ := workspace.FindAllForAgent(omnipusHome(), agentID); len(ids) > 1 {
			return nil, BrowserResolveAmbiguous
		}
		return nil, BrowserResolveNoWorkspace
	}
	mgr, err := al.BrowserManagerForKey(ctx, key)
	if err != nil {
		return nil, BrowserResolveLaunchFailed
	}
	return mgr, BrowserResolveOK
}

// BrowserManagers returns a defensive-copy snapshot of every BrowserManager
// currently registered — one per agent that got browser tools during
// registerSharedTools' browser.RegisterTools call. Used by gateway boot
// (RunContextWithOptions) to kick off BrowserManager.Preprovision for each
// manager in the background right after NewAgentLoop returns, so a fresh
// install's managed Chromium download starts at boot instead of at an
// agent's first browser tool call. Thread-safe; the returned slice is safe
// to range over after this call returns even if browserMgrs is later
// mutated (e.g. by a hot reload's Shutdown()+replace in registerSharedTools).
func (al *AgentLoop) BrowserManagers() []*browser.BrowserManager {
	al.mu.RLock()
	defer al.mu.RUnlock()
	out := make([]*browser.BrowserManager, 0, len(al.browserMgrs))
	for _, mgr := range al.browserMgrs {
		out = append(out, mgr)
	}
	return out
}
