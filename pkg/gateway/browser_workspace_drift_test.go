// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// browser_workspace_drift_test.go — the live panel and the agent's own tools
// must name the SAME browser, and neither may name one on a workspace whose
// team the agent has left.
//
// THE DEFECT. A chat remembers the workspace it was opened in: pkg/agent's
// runTurn stamps it onto the turn context (tools.WithWorkspaceID), and
// pkg/gateway reads it back off the session's meta (sessionWorkspaceID). That
// label is written once and never revisited, so it outlives the team roster it
// was written from. Remove the agent from that workspace's team mid-conversation
// and the two sides diverged:
//
//   - the PANEL re-checked membership (ResolveBrowsingKeyForAgent honours a
//     preferred workspace only when FindForAgentPreferring confirms the agent is
//     on that specific team) and fell back to a workspace the agent really is on;
//   - the agent's TOOLS did not. ResolveBrowsingKey's rung 1 returned
//     ws:<whatever the context said> with no membership check at all.
//
// The confusing half is that the operator then watched one browser while the
// agent drove another, with nothing on either side reporting a problem. The
// serious half is that the agent's tools got a workspace's browser — and
// therefore the operator's live logins and cookies for every site that workspace
// has visited — while not being on its team. Under ADR-072 every agent on a
// workspace shares those logins, which is exactly why membership is a security
// boundary and not bookkeeping.
//
// THE FIX IS ONE CHECK, NOT TWO. Both sides now enter
// browser.resolveBrowsingKeyForAgent with the same preference and the same
// rules — the panel via ResolveBrowsingKeyForAgent, the tools via
// ResolveBrowsingKey. A second membership check bolted onto the gateway would
// have left this project with two answers to "whose logins does this act with",
// which is the drift these tests exist to forbid.
//
// Chrome-free: every assertion is about which browser is RESOLVED, and
// resolution completes before any Chrome process is asked for.

// removeFromTeam rewrites a workspace's stored core team, which is what
// removing a member through the Workspace → Team tab leaves on disk (PUT
// /api/v1/workspaces/{id} replaces core_team wholesale — see
// rest_workspaces.go's "core_team is a full-replacement field" note). Uses the
// production writeWorkspaceFile so the bytes both resolvers read back are the
// real ones.
func removeFromTeam(t *testing.T, home, wsID string, remaining ...string) {
	t.Helper()
	seedBindingWorkspace(t, home, wsID, remaining...)

	// A guard, not decoration: if the rewrite silently failed, every assertion
	// below would pass for the wrong reason.
	raw, err := os.ReadFile(filepath.Join(home, "workspaces", wsID+".json"))
	require.NoError(t, err)
	require.NotContains(t, string(raw), `"`+bindingAgentID+`"`,
		"the agent must really be off %s's team, or this test proves nothing", wsID)
}

// toolTurnCtx builds the context an agent's browser tools actually run under.
// The two values are the ones pkg/agent/loop.go's runTurn stamps —
// tools.WithAgentID(turnCtx, ts.agent.ID) and
// tools.WithWorkspaceID(turnCtx, ts.opts.WorkspaceID) — and they are the ONLY
// inputs browser.ResolveBrowsingKey reads.
func toolTurnCtx(agentID, chatWorkspaceID string) context.Context {
	ctx := tools.WithAgentID(context.Background(), agentID)
	if chatWorkspaceID != "" {
		ctx = tools.WithWorkspaceID(ctx, chatWorkspaceID)
	}
	return ctx
}

// panelWorkspaceFor is the PANEL's answer: what the gateway resolves for a
// browser_attach naming this chat session. It is handleAttach's own two steps
// (sessionWorkspaceID, then BrowserManagerForAgent) with nothing in between.
func panelWorkspaceFor(
	t *testing.T, h *BrowserWSHandler, al *agent.AgentLoop, sessionID string,
) (string, agent.BrowserResolveOutcome) {
	t.Helper()
	mgr, outcome := al.BrowserManagerForAgent(
		context.Background(), bindingAgentID, h.sessionWorkspaceID(sessionID))
	if outcome != agent.BrowserResolveOK {
		return "", outcome
	}
	require.NotNil(t, mgr)
	return mgr.BrowsingKey().WorkspaceID(), outcome
}

// toolWorkspaceFor is the TOOLS' answer, resolved exactly as
// agentLoopBrowserResolver.ManagerFor resolves it.
func toolWorkspaceFor(t *testing.T, home, agentID, chatWorkspaceID string) (string, error) {
	t.Helper()
	key, err := browser.ResolveBrowsingKey(toolTurnCtx(agentID, chatWorkspaceID), home)
	if err != nil {
		return "", err
	}
	return key.WorkspaceID(), nil
}

// TestBrowserWorkspace_PanelAndToolsAgreeAfterATeamRemoval is the drift itself.
//
// The discriminating step is the removal. Before it, both sides answer A and
// agree for an uninteresting reason — the label and the membership say the same
// thing. After it they can only still agree if BOTH re-check membership, because
// the label on disk still says A and only the roster changed.
func TestBrowserWorkspace_PanelAndToolsAgreeAfterATeamRemoval(t *testing.T) {
	handler, al, home := newBindingTestLoop(t, nil)

	// The chat was opened in workspace A and carries A's label from here on.
	sessA := newChatSessionInWorkspace(t, al, bindingWorkspaceA)
	require.Equal(t, bindingWorkspaceA, handler.sessionWorkspaceID(sessA))

	// Baseline — the agent is on A, so A is the right answer on both sides.
	panelBefore, outcomeBefore := panelWorkspaceFor(t, handler, al, sessA)
	require.Equal(t, agent.BrowserResolveOK, outcomeBefore)
	toolBefore, err := toolWorkspaceFor(t, home, bindingAgentID, bindingWorkspaceA)
	require.NoError(t, err)
	require.Equal(t, bindingWorkspaceA, panelBefore)
	require.Equal(t, bindingWorkspaceA, toolBefore,
		"before the removal both sides must say A, or the change below proves nothing")

	// --- the team edit, mid-conversation. The chat is untouched and still
	// labelled A; only A's roster changed. ---
	removeFromTeam(t, home, bindingWorkspaceA, "someone-else")

	remaining, _ := workspace.FindAllForAgent(home, bindingAgentID)
	require.Equal(t, []string{bindingWorkspaceB}, remaining,
		"after the removal the agent must be on B and only B")
	require.Equal(t, bindingWorkspaceA, handler.sessionWorkspaceID(sessA),
		"the SESSION's label is deliberately unchanged — that stale label is the whole defect")

	panelAfter, outcomeAfter := panelWorkspaceFor(t, handler, al, sessA)
	require.Equal(t, agent.BrowserResolveOK, outcomeAfter)
	toolAfter, err := toolWorkspaceFor(t, home, bindingAgentID, bindingWorkspaceA)
	require.NoError(t, err)

	assert.Equal(t, panelAfter, toolAfter,
		"the panel and the agent's own tools must name the SAME browser. They did not: the panel "+
			"re-checked membership and fell back to B while the tools took the chat's stale A "+
			"label at face value, so the operator watched one browser and the agent drove another")
	assert.Equal(t, bindingWorkspaceB, toolAfter,
		"the agent is no longer on workspace A's team, so A's browser — and A's live logins — "+
			"must not be what its tools address")
	assert.NotEqual(t, bindingWorkspaceA, toolAfter,
		"a workspace label written before the removal must not survive it as an access grant")
}

// TestBrowserWorkspace_OffTeamAgentIsRefused is the security half on its own,
// with no fallback available to soften it.
//
// The agent is on NO workspace at all, and the chat is labelled with a workspace
// that really exists and really has a browser. Before the fix, rung 1 minted
// ws:<that label> and the agent drove that workspace's logged-in Chrome. There
// is no "which of its own workspaces did it mean" question here — the only
// correct answer is a refusal, on both sides.
func TestBrowserWorkspace_OffTeamAgentIsRefused(t *testing.T) {
	handler, al, home := newBindingTestLoop(t, nil)
	sess := newChatSessionInWorkspace(t, al, bindingWorkspaceA)

	// Strip the agent from BOTH workspaces: it is now on no team anywhere.
	removeFromTeam(t, home, bindingWorkspaceA, "someone-else")
	removeFromTeam(t, home, bindingWorkspaceB, "someone-else")

	ids, _ := workspace.FindAllForAgent(home, bindingAgentID)
	require.Empty(t, ids, "the agent must be on no workspace, or this is not the case under test")

	// The label is still readable and still names a live workspace — the
	// refusal has to come from membership, not from the read failing.
	require.Equal(t, bindingWorkspaceA, handler.sessionWorkspaceID(sess))
	require.FileExists(t, filepath.Join(home, "workspaces", bindingWorkspaceA+".json"))

	_, outcome := panelWorkspaceFor(t, handler, al, sess)
	assert.Equal(t, agent.BrowserResolveNoWorkspace, outcome,
		"the panel must refuse an agent that is on no workspace team")

	gotWS, err := toolWorkspaceFor(t, home, bindingAgentID, bindingWorkspaceA)
	assert.ErrorIs(t, err, browser.ErrNoBrowsingContext,
		"an agent that is on NO workspace team must be refused a browser by its own tools too — "+
			"a chat's workspace label is not a membership grant")
	assert.Empty(t, gotWS,
		"a refused resolution must not hand back a workspace id")
}

// TestBrowserWorkspace_NamingAnotherTeamsWorkspaceGrantsNothing covers the
// cross-workspace reach directly: the chat names a workspace that exists, has a
// team, and does not include this agent — while the agent does have a workspace
// of its own to fall back to.
//
// It is the same shape as an operator moving a chat, or a session predating a
// re-organisation. The answer must be the agent's OWN workspace, never the one
// the label points at.
func TestBrowserWorkspace_NamingAnotherTeamsWorkspaceGrantsNothing(t *testing.T) {
	handler, al, home := newBindingTestLoop(t, nil)

	const outsiderWS = "bindingtestworkspacedelta"
	seedBindingWorkspace(t, home, outsiderWS, "someone-else")

	// The agent keeps exactly one membership of its own, so the fallback is
	// unambiguous and a refusal cannot be mistaken for the fix working.
	removeFromTeam(t, home, bindingWorkspaceA, "someone-else")

	sess := newChatSessionInWorkspace(t, al, outsiderWS)
	require.Equal(t, outsiderWS, handler.sessionWorkspaceID(sess))

	panelWS, outcome := panelWorkspaceFor(t, handler, al, sess)
	require.Equal(t, agent.BrowserResolveOK, outcome)

	toolWS, err := toolWorkspaceFor(t, home, bindingAgentID, outsiderWS)
	require.NoError(t, err)

	assert.Equal(t, panelWS, toolWS, "both sides must still name the same browser")
	assert.Equal(t, bindingWorkspaceB, toolWS,
		"the agent's own workspace is the answer; the workspace the chat names has a team this "+
			"agent is not on, and naming it must not reach its browser")
	assert.NotEqual(t, outsiderWS, toolWS,
		"another team's workspace must never be resolvable from a chat label alone")
}

// TestBrowserWorkspace_ToolResolutionIsWiredToTheRealTurnContext is the hop the
// behavioural tests above cannot execute from here.
//
// They call browser.ResolveBrowsingKey with a context they build themselves. If
// production stopped routing the tools through that function, or stopped putting
// the chat's workspace on the turn context, every assertion above would keep
// passing while describing code nothing runs. Both call sites live in pkg/agent,
// which imports this package's dependencies rather than the other way round, so
// they are asserted at the source — the same compromise, for the same reason, as
// TestBrowserTTLConfigKeys_HaveAWriter in pkg/tools/browser.
func TestBrowserWorkspace_ToolResolutionIsWiredToTheRealTurnContext(t *testing.T) {
	loopSrc, err := os.ReadFile(filepath.Join("..", "agent", "loop.go"))
	require.NoError(t, err)

	assert.Contains(t, string(loopSrc), "browser.ResolveBrowsingKey(ctx, omnipusHome())",
		"agentLoopBrowserResolver.ManagerFor must still resolve every browser tool call through "+
			"ResolveBrowsingKey — if it moved, the tests above stopped describing the tools' real path")
	assert.Contains(t, string(loopSrc), "turnCtx = tools.WithWorkspaceID(turnCtx, ts.opts.WorkspaceID)",
		"the chat's workspace must still reach the turn context, or the stale-label case these "+
			"tests exist for is no longer the case production is in")
	assert.Contains(t, string(loopSrc), "browser.ResolveBrowsingKeyForAgent(omnipusHome(), agentID, preferredWorkspaceID)",
		"BrowserManagerForAgent must still be the panel's resolution, through the same package "+
			"function — two resolvers is the drift this file forbids")
}
