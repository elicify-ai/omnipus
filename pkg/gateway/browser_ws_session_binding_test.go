// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// Test 46 (ADR-072 SC-007 condition 3, FR-016/FR-017/FR-018).
//
// This is the ONE condition of SC-007 that a semantic reversal cannot pass
// cleanly. Conditions (1) "verify-contracts exits 0" and (2) "the contracts/
// diff touches only description text" are SHAPE checks: reversing what
// BrowserAttachFrame.session_id MEANS changes not a single byte of shape, so
// both would stay green against a server that still ignores session_id
// entirely. Only a behavioural test can tell the two apart, and that is what
// this file is.
//
// The property under test: for an agent that is on BOTH workspace A and
// workspace B, an attach naming a chat session that belongs to B must resolve
// to B's browser — not A's, and not "whichever sorts first". The sorted-first
// tie-break is exactly the failure mode ADR-072 FR-033 refuses to commit,
// because the two workspaces hold two different sets of live logins and
// picking one silently decides which of them the panel drives.
//
// Deliberately no Chrome anywhere in this file: every assertion is about which
// browser is RESOLVED, and resolution completes before any Chrome process is
// asked for. See the sub-tests' own comments for how each stays off that path.

const (
	bindingWorkspaceA = "bindingtestworkspacealpha"
	bindingWorkspaceB = "bindingtestworkspacebravo"
	bindingAgentID    = "roamer"
)

// seedBindingWorkspace writes one workspace file whose CoreTeam is exactly
// members. Uses the gateway's own production writeWorkspaceFile so the bytes
// on disk are the ones workspace.FindAllForAgent actually reads back.
func seedBindingWorkspace(t *testing.T, home, id string, members ...string) {
	t.Helper()
	require.NoError(t, writeWorkspaceFile(home, storedWorkspace{
		ID:       id,
		Name:     id,
		Status:   "active",
		CoreTeam: members,
	}), "seeding workspace %s must succeed", id)
}

// newBindingTestLoop builds an AgentLoop whose single agent (bindingAgentID)
// is on BOTH bindingWorkspaceA and bindingWorkspaceB, and returns the loop
// plus the OMNIPUS_HOME it was built against.
//
// mutate runs against the config before the loop is constructed, so a caller
// can (for example) point the browser at a dead remote CDP endpoint.
func newBindingTestLoop(t *testing.T, mutate func(cfg *config.Config)) (*BrowserWSHandler, *agent.AgentLoop, string) {
	t.Helper()
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
			List: []config.AgentConfig{{ID: bindingAgentID, Home: home}},
		},
	}
	cfg.Tools.Browser.LiveViewEnabled = true
	if mutate != nil {
		mutate(cfg)
	}

	// Seeded BEFORE NewAgentLoop and WITHOUT mustAgentLoop's shared-harness
	// auto-seed: that helper unions every agent into one fixed workspace, and
	// a third membership would make this agent ambiguous for a reason the test
	// did not choose.
	seedBindingWorkspace(t, home, bindingWorkspaceA, bindingAgentID)
	seedBindingWorkspace(t, home, bindingWorkspaceB, bindingAgentID)

	al := mustAgentLoopNoWorkspaceSeed(t, cfg, nil, &restMockProvider{})

	// Guard, not decoration: if the two seeds did not both take, every
	// assertion below about "ambiguous" would pass for the wrong reason.
	ids, _ := workspace.FindAllForAgent(home, bindingAgentID)
	require.ElementsMatch(t, []string{bindingWorkspaceA, bindingWorkspaceB}, ids,
		"the test agent must really be on BOTH workspaces, or nothing below tests ambiguity")

	return newBrowserWSHandler(al, ""), al, home
}

// newChatSessionInWorkspace creates a real chat session in the loop's shared
// store and stamps workspace_id on it, returning the session id.
func newChatSessionInWorkspace(t *testing.T, al *agent.AgentLoop, workspaceID string) string {
	t.Helper()
	store := al.GetSessionStore()
	require.NotNil(t, store, "the shared session store must exist")
	meta, err := store.NewSession(session.SessionTypeChat, "web", bindingAgentID)
	require.NoError(t, err)
	require.NoError(t, store.SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &workspaceID}))
	return meta.ID
}

func TestGateway_SessionIDIsBinding(t *testing.T) {
	t.Run("the session's workspace decides, and no session at all still refuses", func(t *testing.T) {
		handler, al, _ := newBindingTestLoop(t, nil)

		sessB := newChatSessionInWorkspace(t, al, bindingWorkspaceB)
		sessA := newChatSessionInWorkspace(t, al, bindingWorkspaceA)

		// Step 1 — the gateway reads the workspace off the SESSION, server
		// side. Nothing on the wire carried it (FR-016: no wire field was
		// added), so if this returns "" the whole binding is unimplemented.
		require.Equal(t, bindingWorkspaceB, handler.sessionWorkspaceID(sessB),
			"the attaching chat session's own meta is where the workspace comes from")
		require.Equal(t, bindingWorkspaceA, handler.sessionWorkspaceID(sessA))

		// Step 2 — with NO preference the agent is genuinely ambiguous and the
		// gateway REFUSES. This is the control: it proves the two memberships
		// really do collide, so step 3's success cannot be explained by the
		// agent only ever having had one workspace.
		mgr, outcome := al.BrowserManagerForAgent(context.Background(), bindingAgentID, "")
		assert.Nil(t, mgr)
		assert.Equal(t, agent.BrowserResolveAmbiguous, outcome,
			"an agent on two workspaces, attaching from no session, must be refused (FR-033) — never tie-broken")

		// Step 3 — the same agent, resolved with the session's workspace,
		// lands on THAT workspace's browser. Asserted for both A and B so a
		// hard-coded or sorted-first answer fails: bindingWorkspaceA sorts
		// before bindingWorkspaceB, so an implementation that quietly picks
		// the first candidate passes the A case and fails the B one.
		mgrB, outcomeB := al.BrowserManagerForAgent(
			context.Background(), bindingAgentID, handler.sessionWorkspaceID(sessB))
		require.Equal(t, agent.BrowserResolveOK, outcomeB)
		require.NotNil(t, mgrB)
		assert.Equal(t, bindingWorkspaceB, mgrB.BrowsingKey().WorkspaceID(),
			"a session in workspace B must resolve to B's browser")

		mgrA, outcomeA := al.BrowserManagerForAgent(
			context.Background(), bindingAgentID, handler.sessionWorkspaceID(sessA))
		require.Equal(t, agent.BrowserResolveOK, outcomeA)
		require.NotNil(t, mgrA)
		assert.Equal(t, bindingWorkspaceA, mgrA.BrowsingKey().WorkspaceID(),
			"a session in workspace A must resolve to A's browser")

		// FR-001, stated as the consequence a reader cares about: two
		// workspaces are two browsers, so they are two cookie jars.
		assert.NotSame(t, mgrA, mgrB,
			"A and B must not share one browser — sharing one is sharing one set of live logins")
	})

	t.Run("a session naming a workspace the agent is not on is not honoured", func(t *testing.T) {
		handler, al, home := newBindingTestLoop(t, nil)

		// A third workspace this agent is NOT a member of. The session names
		// it; the gateway must ignore that rather than treat the client's
		// choice of session as authority over membership.
		const outsider = "bindingtestworkspacecharlie"
		seedBindingWorkspace(t, home, outsider, "someone-else")
		sess := newChatSessionInWorkspace(t, al, outsider)

		require.Equal(t, outsider, handler.sessionWorkspaceID(sess),
			"the read itself is honest — the refusal happens downstream, in the resolver")

		mgr, outcome := al.BrowserManagerForAgent(
			context.Background(), bindingAgentID, handler.sessionWorkspaceID(sess))
		assert.Nil(t, mgr)
		assert.Equal(t, agent.BrowserResolveAmbiguous, outcome,
			"a preference the agent has no membership for must fall through to the plain ladder, "+
				"not grant access to a workspace's browser the agent is not on")
	})

	t.Run("an unreadable or absent session degrades to empty, never to a guess", func(t *testing.T) {
		handler, _, _ := newBindingTestLoop(t, nil)

		assert.Empty(t, handler.sessionWorkspaceID(""),
			"no session id is not a workspace")
		assert.Empty(t, handler.sessionWorkspaceID("01JZZZZZZZZZZZZZZZZZZZZZZZ"),
			"a session nothing owns is not a workspace")
	})

	// The wiring half: everything above proves the resolver honours the
	// preference, but not that handleAttach actually PASSES it. Before FR-017
	// the attach call site passed a literal "" — which is exactly the input
	// the first sub-test shows is refused — so a multi-workspace agent was
	// refused on every attach, from every chat, forever. This drives a real
	// browser_attach frame and asserts the refusal flips.
	//
	// Chrome-free by construction: cdp_url points at a closed loopback port,
	// so the successful branch reaches remote-CDP mode and fails on the dial
	// instead of launching or downloading a browser. That failure is not the
	// assertion — the assertion is that the message is no longer the FR-033
	// ambiguity refusal.
	t.Run("attach stops refusing once the session names a workspace", func(t *testing.T) {
		handler, al, _ := newBindingTestLoop(t, func(cfg *config.Config) {
			cfg.Tools.Browser.CDPURL = "ws://127.0.0.1:1/devtools/browser/closed-on-purpose"
		})
		t.Cleanup(handler.Wait)
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)

		const ambiguityMarker = "more than one workspace"

		// (a) A session id the store knows nothing about — no workspace to
		// read — must still produce FR-033's refusal.
		unknown := attachAndReadMessage(t, srv, "01JZZZZZZZZZZZZZZZZZZZZZZZ")
		assert.Contains(t, unknown, ambiguityMarker,
			"with no workspace to read off the session, a two-workspace agent must be refused")

		// (b) The same agent, same handler, from a session that IS in a
		// workspace: the ambiguity refusal must be gone.
		bound := attachAndReadMessage(t, srv, newChatSessionInWorkspace(t, al, bindingWorkspaceB))
		assert.NotContains(t, bound, ambiguityMarker,
			"handleAttach must pass the session's workspace as the preference — "+
				"a still-ambiguous refusal here means the call site is still sending \"\"")
	})
}

// attachAndReadMessage opens one browser WS connection, sends a browser_attach
// for bindingAgentID against sessionID, and returns the message text of the
// first frame that carries one. Returns "" if the frame carried no message.
func attachAndReadMessage(t *testing.T, srv *httptest.Server, sessionID string) string {
	t.Helper()
	conn := dialBrowserTestWS(t, srv)
	defer func() { _ = conn.Close() }()
	sendWSAuthFrameDevMode(t, conn)

	data, err := json.Marshal(generated.BrowserAttachFrame{
		Type:      string(generated.WsFrameTypeBrowserAttach),
		AgentId:   bindingAgentID,
		SessionId: sessionID,
	})
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	// A generous but bounded budget: the refusal path answers immediately, and
	// the dial-failure path is a connection-refused on loopback.
	resp := readBrowserFrame(t, conn, 20*time.Second)
	return resp.Message
}
