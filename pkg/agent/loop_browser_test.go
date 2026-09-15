// loop_browser_test.go: tests for resolve a browser manager for an agent

package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/stretchr/testify/require"
)

// --- moved from loop.go tests 2026-09-15 ---

// TestBrowserManagerForAgent_DistinguishesNotRegisteredFromNoWorkspace is
// FR-008a. "Browser tools are not registered for this agent" and "this agent is
// not on a workspace team" are different operator problems with different
// remedies, and browser_inspect.go reported the former for BOTH — so an
// operator whose agent simply had no workspace was sent to check tool
// registration, which was fine.
func TestBrowserManagerForAgent_DistinguishesNotRegisteredFromNoWorkspace(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir()) // no workspaces on disk
	al := &AgentLoop{
		browserMgrs:             make(map[string]*browser.BrowserManager),
		browserRegisteredAgents: map[string]bool{"has-tools": true},
	}

	_, outcome := al.BrowserManagerForAgent(context.Background(), "no-tools-at-all", "")
	require.Equal(t, BrowserResolveNotRegistered, outcome)

	_, outcome = al.BrowserManagerForAgent(context.Background(), "has-tools", "")
	require.Equal(t, BrowserResolveNoWorkspace, outcome,
		"an agent WITH browser tools but no workspace must not be reported as unregistered")
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
