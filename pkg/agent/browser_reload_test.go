package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// browser_reload_test.go — FR-026a and FR-026b, the two properties of
// registerSharedTools that the agent→key re-key made non-obvious.

// TestReload_OneCyclePerKeyNotPerAgent is FR-026b.
//
// N agents on ONE workspace resolve to ONE browsing key and share ONE browser.
// The per-agent registration loop must therefore do the build/Release/Shutdown
// cycle once per KEY, not once per agent — otherwise five agents on a workspace
// tear the same Chrome down and rebuild it five times on every Settings save,
// and the fifth pass Releases a manager the fourth had just installed.
//
// The observable property is the one that matters to a user: after a reload,
// every agent on the workspace resolves to the SAME manager instance, and that
// instance was built once.
func TestReload_OneCyclePerKeyNotPerAgent(t *testing.T) {
	cfg := minimalTestConfig(t)
	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

	ids := al.GetRegistry().ListAgentIDs()
	require.GreaterOrEqual(t, len(ids), 2,
		"this test needs at least two agents sharing the harness workspace")

	first, outcome := al.BrowserManagerForAgent(context.Background(), ids[0], "")
	require.Equal(t, BrowserResolveOK, outcome)
	require.NotNil(t, first)

	for _, id := range ids[1:] {
		mgr, teamOutcome := al.BrowserManagerForAgent(context.Background(), id, "")
		require.Equal(t, BrowserResolveOK, teamOutcome, "agent %q", id)
		require.Same(t, first, mgr,
			"agent %q is on the same workspace as %q and must share its browser — "+
				"a second manager here is a second Chrome and a second cookie jar", id, ids[0])
	}

	// One browser for the whole team, not one per agent.
	al.mu.RLock()
	live := len(al.browserMgrs)
	al.mu.RUnlock()
	require.Equal(t, 1, live,
		"the whole team shares one workspace, so exactly one browser must exist; got %d", live)

	// A reload must replace it exactly once, not once per agent.
	require.NoError(t, al.ReloadProviderAndConfig(context.Background(), provider, cfg))

	al.mu.RLock()
	liveAfter := len(al.browserMgrs)
	al.mu.RUnlock()
	require.Equal(t, 1, liveAfter, "a reload must leave one browser for the workspace, not one per agent")

	afterFirst, outcome := al.BrowserManagerForAgent(context.Background(), ids[0], "")
	require.Equal(t, BrowserResolveOK, outcome)
	for _, id := range ids[1:] {
		mgr, teamOutcome := al.BrowserManagerForAgent(context.Background(), id, "")
		require.Equal(t, BrowserResolveOK, teamOutcome)
		require.Same(t, afterFirst, mgr, "after the reload, agent %q must still share the team's browser", id)
	}
}

// TestReload_PruneUsesBrowsingKeys is FR-026a.
//
// The reload prune drops browsers no live agent is rooted in. Its liveness
// predicate must be the set of live BROWSING KEYS, never registry.ListAgentIDs()
// — that comparison was correct only while the map was keyed by agent id, and
// run unchanged against a key-keyed map it matches NOTHING: every browser looks
// removed, every workspace's Chrome context is disposed on the first Settings
// save, and every login is silently gone. It fails cheerfully, with an INFO line
// per workspace claiming it removed a manager for a "deleted agent".
//
// So this asserts the direction that would break: a reload with the agents
// UNCHANGED must keep the browser, and its key must still be present in the map
// under the key form (not an agent id).
func TestReload_PruneUsesBrowsingKeys(t *testing.T) {
	cfg := minimalTestConfig(t)
	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

	ids := al.GetRegistry().ListAgentIDs()
	require.NotEmpty(t, ids)

	al.mu.RLock()
	keysBefore := make([]string, 0, 2)
	for k := range al.browserMgrs {
		keysBefore = append(keysBefore, k)
	}
	al.mu.RUnlock()
	require.Len(t, keysBefore, 1, "the harness seeds one shared workspace, so one browser")

	// The map key must be a BROWSING KEY, not an agent id. If this ever
	// regresses to an agent id, the prune's key-based diff silently disposes
	// everything on the next reload.
	require.Contains(t, keysBefore[0], "ws:",
		"browserMgrs must be keyed by the browsing key (%q), not by an agent id", keysBefore[0])
	for _, id := range ids {
		require.NotEqual(t, id, keysBefore[0], "browserMgrs is still keyed by an agent id")
	}

	require.NoError(t, al.ReloadProviderAndConfig(context.Background(), provider, cfg))

	al.mu.RLock()
	_, survived := al.browserMgrs[keysBefore[0]]
	live := len(al.browserMgrs)
	al.mu.RUnlock()
	require.True(t, survived,
		"a reload that changed no agents must NOT prune the workspace's browser — "+
			"pruning it here disposes the browser context and every login in it")
	require.Equal(t, 1, live)
}

// TestReload_PrunesTheBrowserOfAWorkspaceNoAgentIsRootedIn is the other half of
// FR-026a: the prune must actually prune. A test that only asserted "nothing was
// removed" would pass against a prune that had been deleted outright.
func TestReload_PrunesTheBrowserOfAWorkspaceNoAgentIsRootedIn(t *testing.T) {
	cfg := minimalTestConfig(t)
	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

	// A browser for a workspace that no registered agent belongs to — the state
	// left behind when a workspace's last team member is removed.
	stray := browserTestKey(t, "workspace-with-no-agents")
	strayMgr, err := al.BrowserManagerForKey(context.Background(), stray)
	require.NoError(t, err)
	require.NotNil(t, strayMgr)

	al.mu.RLock()
	_, present := al.browserMgrs[stray.String()]
	al.mu.RUnlock()
	require.True(t, present, "test setup: the stray browser must be in the map before the reload")

	require.NoError(t, al.ReloadProviderAndConfig(context.Background(), provider, cfg))

	al.mu.RLock()
	_, stillThere := al.browserMgrs[stray.String()]
	al.mu.RUnlock()
	require.False(t, stillThere,
		"a browser whose workspace no live agent is rooted in must be pruned on reload — "+
			"otherwise its Chrome and its browser context leak for the life of the process")
}

// writeWorkspaceFile is a local helper for tests that need a SECOND workspace
// beyond the one the shared harness seeds.
func writeWorkspaceFile(t *testing.T, home, workspaceID string, coreTeam []string) {
	t.Helper()
	dir := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	body, err := json.Marshal(map[string]any{"id": workspaceID, "core_team": coreTeam})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, workspaceID+".json"), body, 0o600))
}

// TestBrowserManagerForAgent_AmbiguousMembershipRefuses is FR-033 at the
// gateway-facing boundary: an agent on two workspaces' core teams, with no
// preferred workspace supplied, must REFUSE rather than tie-break. Choosing
// silently picks which set of live logins the panel drives.
func TestBrowserManagerForAgent_AmbiguousMembershipRefuses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	writeWorkspaceFile(t, home, "workspace-one", []string{"roamer"})
	writeWorkspaceFile(t, home, "workspace-two", []string{"roamer"})

	var askedFor []string
	al := &AgentLoop{
		browserMgrs:             make(map[string]*browser.BrowserManager),
		browserRegisteredAgents: map[string]bool{"roamer": true},
		browserFactory: func(k browser.BrowsingKey) (*browser.BrowserManager, error) {
			askedFor = append(askedFor, k.String())
			return nil, errTestFactoryRefuses
		},
	}

	mgr, outcome := al.BrowserManagerForAgent(context.Background(), "roamer", "")
	require.Nil(t, mgr)
	require.Equal(t, BrowserResolveAmbiguous, outcome)
	require.Empty(t, askedFor,
		"an ambiguous membership must be refused BEFORE a browser is built — "+
			"building one means a workspace was silently chosen")

	// Naming one of them resolves it: the caller has said which it means, and
	// that is not the ambiguity FR-033 refuses.
	mgr, outcome = al.BrowserManagerForAgent(context.Background(), "roamer", "workspace-two")
	require.Nil(t, mgr)
	require.Equal(t, BrowserResolveLaunchFailed, outcome)
	require.Equal(t, []string{"ws:workspace-two"}, askedFor,
		"the NAMED workspace must be the one resolved, not the sorted-first one")
}

// errTestFactoryRefuses stands in for a launch failure, so a test can observe
// which key resolution reached the factory without building a real manager.
var errTestFactoryRefuses = errors.New("test factory: refusing to build a manager")
