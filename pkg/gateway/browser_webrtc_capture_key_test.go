// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// Test 25 (ADR-075 FR-016a).
//
// Before this, the WebRTC capture registry was keyed by AGENT ID, and the
// ADR-048 condition-2 fence asked "does any other agent hold an
// actively-viewed capture?". After FR-001 those two facts combine into a bug
// rather than a safeguard: agents on one workspace share one BrowserManager,
// ensureCaptureSession memoizes on the manager, so both agents' registry
// entries pointed at one identical CaptureSession — and the second agent to
// open a panel found the FIRST agent's entry, saw viewers on it, and denied
// itself the very session it was about to join. Two colleagues on one
// workspace could not both watch their own browser.
//
// The registry is now keyed by browsing key: one entry per workspace browser.
// This test pins both halves of that — the one-session invariant and the
// collapsed fence — and it does so without Chrome, deliberately. The three
// pre-existing fence tests (TestHandleWebRTCOffer_OtherAgent*) all SKIP
// without a working Chromium, so on CI they guard nothing; a green suite
// there is not evidence about this behaviour at all.

func TestCaptureRegistry_OnePerBrowsingKey(t *testing.T) {
	const (
		wsShared  = "captkeyworkspaceshared"
		wsOther   = "captkeyworkspaceother"
		agentA    = "alice"
		agentB    = "bob"
		agentSolo = "carol"
	)

	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         home,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{ID: agentA, Home: home},
				{ID: agentB, Home: home},
				{ID: agentSolo, Home: home},
			},
		},
	}
	cfg.Tools.Browser.LiveViewEnabled = true
	// Remote-CDP mode: keeps every manager construction off the
	// launch-or-download path. Nothing here dials it — the whole test lives
	// upstream of any CDP round trip.
	cfg.Tools.Browser.CDPURL = "ws://127.0.0.1:1/devtools/browser/unused"

	// alice and bob share a workspace; carol has her own.
	seedBindingWorkspace(t, home, wsShared, agentA, agentB)
	seedBindingWorkspace(t, home, wsOther, agentSolo)

	al := mustAgentLoopNoWorkspaceSeed(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	handler := newBrowserWSHandler(al, "")

	mgrA, outA := al.BrowserManagerForAgent(context.Background(), agentA, wsShared)
	require.Equal(t, agent.BrowserResolveOK, outA)
	mgrB, outB := al.BrowserManagerForAgent(context.Background(), agentB, wsShared)
	require.Equal(t, agent.BrowserResolveOK, outB)
	mgrSolo, outSolo := al.BrowserManagerForAgent(context.Background(), agentSolo, wsOther)
	require.Equal(t, agent.BrowserResolveOK, outSolo)

	// FR-001 restated as the fact the rest of this test depends on: two agents
	// on one team are looking at ONE browser, so there is only one thing to
	// capture.
	require.Same(t, mgrA, mgrB, "two agents on one workspace must resolve to one BrowserManager")
	require.NotSame(t, mgrA, mgrSolo, "a different workspace must be a different browser")

	keyShared := mgrA.BrowsingKey().String()
	keyOther := mgrSolo.BrowsingKey().String()
	require.NotEqual(t, keyShared, keyOther)

	// --- One capture session for the workspace, not one per agent ----------
	csA, err := handler.ensureCaptureSession(mgrA, agentA, "", cfg)
	require.NoError(t, err)
	t.Cleanup(csA.Stop)
	csB, err := handler.ensureCaptureSession(mgrB, agentB, "", cfg)
	require.NoError(t, err)

	assert.Same(t, csA, csB,
		"bob must JOIN alice's capture, not get a second encoder pipeline on the same Chrome")

	// --- The registry key is the browser, not whoever asked first ---------
	gotKey, gotCS := handler.captures.findByToken(csA.TokenHex())
	assert.Same(t, csA, gotCS)
	assert.Equal(t, keyShared, gotKey,
		"the registry must key on the browsing key; keying on the requesting agent id is FR-016a's bug")
	assert.NotEqual(t, agentA, gotKey)

	// --- The fence no longer denies the second agent on the same browser ---
	// This is the collapsed ADR-048 condition 2. From bob's point of view
	// there is now no OTHER capture at all, so the deny branch is unreachable
	// for him however many viewers alice's panel has.
	assert.Empty(t, handler.captures.otherSessions(keyShared),
		"a second agent on the same workspace must see no conflicting capture — "+
			"seeing one is how bob used to deny himself the session he was joining")

	// --- ...but a genuinely different browser is still a conflict ---------
	// The fence was collapsed, not deleted: one host still cannot usefully
	// serve two simultaneously-viewed tab captures, and those now live in
	// different Chromes.
	others := handler.captures.otherSessions(keyOther)
	require.Len(t, others, 1,
		"carol's workspace must still see the shared workspace's capture as a foreign one")
	assert.Contains(t, others, keyShared)

	// --- Teardown deregisters by key, not by agent ------------------------
	handler.captures.removeIfCurrent(agentA, csA)
	stillThere, _ := handler.captures.findByToken(csA.TokenHex())
	assert.Equal(t, keyShared, stillThere,
		"an agent-id-keyed removal must be a no-op now — if this drops the entry, "+
			"one agent detaching would deregister the whole workspace's capture")

	handler.captures.removeIfCurrent(keyShared, csA)
	goneKey, goneCS := handler.captures.findByToken(csA.TokenHex())
	assert.Empty(t, goneKey)
	assert.Nil(t, goneCS)
}
