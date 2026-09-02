// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// Tests 21, 22 and 23 — the gateway's half of ADR-072's server-side
// agent→workspace resolution.
//
// The shared premise: an operator staring at a dead browser panel needs to
// know WHICH of three unrelated problems they have, and before FR-008a all
// three said "browser tools may not be registered for this agent" — advice
// that is correct for exactly one of them and sends the other two hunting
// through config for a registration that was never missing.

// Test 21. The three failure reasons must be genuinely different sentences,
// and the no-workspace one must offer the SAME remedy, in the SAME words, as
// the error an agent's own tool call reads back (round-2 MIN-107). Two
// phrasings of one fix is how an operator ends up believing they have two
// problems.
func TestGateway_ResolveOutcomes_AreDistinct(t *testing.T) {
	const agentID = "resolveoutcomesagent"

	reasons := map[agent.BrowserResolveOutcome]string{
		agent.BrowserResolveNoWorkspace:   browserResolveReason(agent.BrowserResolveNoWorkspace, agentID),
		agent.BrowserResolveAmbiguous:     browserResolveReason(agent.BrowserResolveAmbiguous, agentID),
		agent.BrowserResolveNotRegistered: browserResolveReason(agent.BrowserResolveNotRegistered, agentID),
	}

	seen := map[string]agent.BrowserResolveOutcome{}
	for outcome, reason := range reasons {
		require.NotEmpty(t, reason, "every failure outcome must render a reason")
		assert.Contains(t, reason, agentID, "a reason must name the agent it is about")
		if prior, dup := seen[reason]; dup {
			t.Fatalf("outcomes %v and %v render the SAME sentence (%q) — "+
				"an operator cannot tell the two problems apart", prior, outcome, reason)
		}
		seen[reason] = outcome
	}

	// The specific conflation FR-008a exists to end: "not on a workspace team"
	// must not be reported as a registration failure.
	noWorkspace := reasons[agent.BrowserResolveNoWorkspace]
	assert.NotContains(t, strings.ToLower(noWorkspace), "not registered",
		"an agent that is simply not on a workspace team has not failed to register anything")
	assert.NotContains(t, strings.ToLower(noWorkspace), "may not be registered",
		"this is the pre-ADR-072 sentence every outcome used to render")

	// Word-for-word agreement with the error the AGENT sees. Asserted in both
	// directions on purpose: that the panel carries the remedy, AND that the
	// remedy really is a substring of ErrNoBrowsingContext rather than a
	// constant the two have drifted away from together.
	assert.Contains(t, noWorkspace, browserNoWorkspaceRemedy,
		"the panel must offer the remedy verbatim")
	assert.Contains(t, browser.ErrNoBrowsingContext.Error(), browserNoWorkspaceRemedy,
		"browserNoWorkspaceRemedy must be quoted OUT of ErrNoBrowsingContext — "+
			"if this fails the two have been re-worded independently and now disagree")

	// The ambiguity reason has to say what is actually at stake. "Ambiguous"
	// alone reads like a display bug; the real consequence is that choosing
	// would decide which set of live logins the panel drives.
	ambiguous := reasons[agent.BrowserResolveAmbiguous]
	assert.Contains(t, strings.ToLower(ambiguous), "more than one workspace")
	assert.Contains(t, strings.ToLower(ambiguous), "logins",
		"the ambiguity refusal must name the consequence, not just the condition")

	// No fourth, capacity-shaped reason. ADR-072 D1.5a deleted every tab and
	// browser counter, so the panel can never truthfully blame a cap — and a
	// reason naming one would send an operator looking for a limit to raise
	// that does not exist.
	for outcome, reason := range reasons {
		low := strings.ToLower(reason)
		for _, forbidden := range []string{"pool", "capacity", "limit", "max_tabs", "too many"} {
			assert.NotContains(t, low, forbidden,
				"outcome %v names a capacity that no longer exists: %q", outcome, reason)
		}
	}

	// OK renders nothing: a success must never leave a stale sentence for the
	// panel to display next to a working browser.
	assert.Empty(t, browserResolveReason(agent.BrowserResolveOK, agentID))
}

// Test 22. The attaching chat session's workspace_id BEATS the plain
// membership ladder — and the assertion is made against what that ladder
// would actually have returned, not against a guess about it.
func TestGateway_PrefersSessionWorkspaceID(t *testing.T) {
	handler, al, home := newBindingTestLoop(t, nil)

	// What the plain ladder does on its own. FindForAgentPreferring with no
	// preference is the sorted-first tie-break the FILESYSTEM re-rooting uses;
	// bindingWorkspaceA sorts before bindingWorkspaceB, so it is the answer
	// the browser must NOT copy.
	ladder, ok := workspace.FindForAgentPreferring(home, bindingAgentID, "")
	require.True(t, ok, "the plain ladder must resolve something, or this test proves nothing")
	require.Equal(t, bindingWorkspaceA, ladder,
		"precondition: the plain ladder picks A, so preferring B is a real difference")

	sessB := newChatSessionInWorkspace(t, al, bindingWorkspaceB)
	preferred := handler.sessionWorkspaceID(sessB)
	require.Equal(t, bindingWorkspaceB, preferred)

	mgr, outcome := al.BrowserManagerForAgent(context.Background(), bindingAgentID, preferred)
	require.Equal(t, agent.BrowserResolveOK, outcome)
	require.NotNil(t, mgr)
	assert.Equal(t, bindingWorkspaceB, mgr.BrowsingKey().WorkspaceID(),
		"the session's workspace must win over the sorted-first ladder")
	assert.NotEqual(t, ladder, mgr.BrowsingKey().WorkspaceID())

	// And degrading to no preference does NOT fall back to the ladder's
	// answer — it refuses. The browser deliberately does not inherit the
	// filesystem's tie-break, because here it would choose whose live logins
	// the panel acts with (FR-033).
	degraded, degradedOutcome := al.BrowserManagerForAgent(context.Background(), bindingAgentID, "")
	assert.Nil(t, degraded)
	assert.Equal(t, agent.BrowserResolveAmbiguous, degradedOutcome,
		"no preference must refuse, never silently reuse the filesystem's sorted-first pick")
}

// Test 23. A multi-workspace agent's TURN and its live PANEL must resolve to
// the same browser — including agreeing to refuse. They are two different code
// paths (browser.ResolveBrowsingKey off the turn context; BrowserManagerForAgent
// off an attach frame) and a disagreement is invisible from either side: the
// agent would drive one workspace's tabs while the operator watched another's,
// with both screens looking entirely normal.
func TestMultiWorkspaceAgent_TurnAndPanelAgree(t *testing.T) {
	handler, al, home := newBindingTestLoop(t, nil)

	turnKeyFor := func(workspaceID string) (browser.BrowsingKey, error) {
		ctx := tools.WithAgentID(context.Background(), bindingAgentID)
		if workspaceID != "" {
			ctx = tools.WithWorkspaceID(ctx, workspaceID)
		}
		return browser.ResolveBrowsingKey(ctx, home)
	}

	for _, ws := range []string{bindingWorkspaceA, bindingWorkspaceB} {
		t.Run("both resolve to "+ws, func(t *testing.T) {
			turnKey, err := turnKeyFor(ws)
			require.NoError(t, err, "the turn must resolve when its context names a workspace")

			sess := newChatSessionInWorkspace(t, al, ws)
			mgr, outcome := al.BrowserManagerForAgent(
				context.Background(), bindingAgentID, handler.sessionWorkspaceID(sess))
			require.Equal(t, agent.BrowserResolveOK, outcome)
			require.NotNil(t, mgr)

			assert.Equal(t, turnKey.String(), mgr.BrowsingKey().String(),
				"the agent's own tools and the operator's panel must address ONE browser; "+
					"disagreeing here means the operator watches a tab the agent is not driving")
		})
	}

	// The refusal has to agree too, and this is the half that is easy to get
	// wrong: a panel that quietly tie-breaks while the turn refuses looks like
	// a working feature until somebody notices the agent cannot see the page
	// the human is looking at.
	t.Run("and they agree to refuse", func(t *testing.T) {
		_, turnErr := turnKeyFor("")
		require.ErrorIs(t, turnErr, browser.ErrNoBrowsingContext,
			"a turn with no workspace on its context must refuse for a two-workspace agent")

		mgr, outcome := al.BrowserManagerForAgent(context.Background(), bindingAgentID, "")
		assert.Nil(t, mgr)
		assert.Equal(t, agent.BrowserResolveAmbiguous, outcome,
			"the panel must refuse wherever the turn refuses — never both silently pick "+
				"the sorted-first workspace, and never one of them alone")
	})
}
