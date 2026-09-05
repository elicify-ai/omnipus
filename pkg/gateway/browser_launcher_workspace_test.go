// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
)

// U2 regression: "Open browser" refused with advice the operator had already
// followed.
//
// The panel's workspace binding (ADR-075 FR-017) was never broken — a chat
// session whose meta carries a workspace_id resolves that workspace's browser,
// as browser_ws_session_binding_test.go proves. What was broken is the ONE
// session the launcher itself makes: the SPA's "Open browser" button creates a
// session with POST /sessions {agent_id} and nothing else, so the session it
// hands the panel carries no workspace at all. The panel then had nothing to
// read, fell through to the plain membership ladder, and refused a
// two-workspace agent under FR-033 — while telling the operator to "open this
// panel from a chat that belongs to the workspace you mean", which is exactly
// what they had just done. The workspace was on screen, in the route, and in
// the SPA's own store; it simply never reached the session.
//
// The fix is a workspace_id on SessionCreateRequest, stamped onto the new
// session's meta. Nothing here loosens the refusal: an agent that genuinely
// cannot be placed is still refused (the second sub-test), and a preference
// the agent has no membership for is still not honoured (already covered by
// browser_ws_session_binding_test.go).
//
// Chrome-free: every assertion is about which browser is RESOLVED, and
// resolution completes before any Chrome process is asked for. The
// success-path sub-test therefore only asserts the outcome and the resolved
// workspace id, never a live tab.
func TestGateway_OpenBrowserLauncherSessionCarriesItsWorkspace(t *testing.T) {
	// createLauncherSession drives the REAL POST /sessions handler the SPA
	// launcher calls, and returns the created session id. body is the exact
	// JSON the SPA sends.
	createLauncherSession := func(t *testing.T, api *restAPI, body string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		api.createSessionHTTP(rec, req)
		require.Equal(t, http.StatusCreated, rec.Code,
			"the launcher's own create call must succeed: %s", rec.Body.String())
		var created struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
		require.NotEmpty(t, created.ID)
		return created.ID
	}

	t.Run("a session created with the chat's workspace resolves that workspace's browser", func(t *testing.T) {
		handler, al, home := newBindingTestLoop(t, nil)
		api := &restAPI{
			agentLoop:     al,
			onboardingMgr: onboarding.NewManager(home),
			homePath:      home,
		}

		// The launcher clicked from a chat at /workspaces/<B>/chat.
		sessID := createLauncherSession(t, api,
			`{"agent_id":"`+bindingAgentID+`","workspace_id":"`+bindingWorkspaceB+`"}`)

		// The stamp landed on the session's OWN meta — which is the only place
		// the gateway will look (FR-016: no workspace field on the wire frame).
		require.Equal(t, bindingWorkspaceB, handler.sessionWorkspaceID(sessID),
			"POST /sessions must stamp the workspace, or the panel has nothing to read")

		// And the panel now resolves instead of refusing.
		mgr, outcome := al.BrowserManagerForAgent(
			context.Background(), bindingAgentID, handler.sessionWorkspaceID(sessID))
		require.Equal(t, agent.BrowserResolveOK, outcome,
			"the very click the operator was told to make must now work")
		require.NotNil(t, mgr)
		assert.Equal(t, bindingWorkspaceB, mgr.BrowsingKey().WorkspaceID(),
			"and it must be the chat's OWN workspace, not whichever sorts first")

		// Asserted for A too, so a hard-coded or sorted-first answer fails:
		// bindingWorkspaceA sorts before bindingWorkspaceB.
		sessA := createLauncherSession(t, api,
			`{"agent_id":"`+bindingAgentID+`","workspace_id":"`+bindingWorkspaceA+`"}`)
		mgrA, outcomeA := al.BrowserManagerForAgent(
			context.Background(), bindingAgentID, handler.sessionWorkspaceID(sessA))
		require.Equal(t, agent.BrowserResolveOK, outcomeA)
		require.NotNil(t, mgrA)
		assert.Equal(t, bindingWorkspaceA, mgrA.BrowsingKey().WorkspaceID())
		assert.NotSame(t, mgrA, mgr,
			"two workspaces are two browsers — two cookie jars")
	})

	t.Run("a launcher click from no workspace at all is still refused, not guessed", func(t *testing.T) {
		handler, al, home := newBindingTestLoop(t, nil)
		api := &restAPI{
			agentLoop:     al,
			onboardingMgr: onboarding.NewManager(home),
			homePath:      home,
		}

		// The global/inbox chat: the SPA has no active workspace to send.
		sessID := createLauncherSession(t, api, `{"agent_id":"`+bindingAgentID+`"}`)

		assert.Empty(t, handler.sessionWorkspaceID(sessID),
			"no workspace supplied must stay no workspace — never a default or a guess")
		mgr, outcome := al.BrowserManagerForAgent(
			context.Background(), bindingAgentID, handler.sessionWorkspaceID(sessID))
		assert.Nil(t, mgr)
		assert.Equal(t, agent.BrowserResolveAmbiguous, outcome,
			"with genuinely nothing to disambiguate on, FR-033's refusal must stand")
	})

	// The default config has gateway.validate_inbound off, so the sub-tests
	// above decode the body without ever consulting the JSON Schema. An
	// operator who turns it on gets a DIFFERENT code path: the body is checked
	// against pkg/gateway/inboundschemas/SessionCreateRequest.yaml before it is
	// decoded, and a property the schema does not know about is a 400. Adding
	// workspace_id to contracts/components/schemas without the inboundschemas
	// copy (scripts/gen-contracts.sh step 5 syncs them) would therefore leave
	// the fix working for most installs and broken for the strict ones —
	// silently, because nothing else here would notice.
	t.Run("the strict inbound-validation path accepts the new field too", func(t *testing.T) {
		handler, al, home := newBindingTestLoop(t, func(cfg *config.Config) {
			cfg.Gateway.ValidateInbound = true
		})
		require.True(t, al.GetConfig().Gateway.ValidateInbound,
			"the strict path must really be on, or this test proves nothing")
		api := &restAPI{
			agentLoop:     al,
			onboardingMgr: onboarding.NewManager(home),
			homePath:      home,
		}

		sessID := createLauncherSession(t, api,
			`{"agent_id":"`+bindingAgentID+`","workspace_id":"`+bindingWorkspaceB+`"}`)
		assert.Equal(t, bindingWorkspaceB, handler.sessionWorkspaceID(sessID),
			"schema validation must let the workspace through, not strip or reject it")
	})

	t.Run("a workspace that does not exist is rejected, not stamped", func(t *testing.T) {
		_, al, home := newBindingTestLoop(t, nil)
		api := &restAPI{
			agentLoop:     al,
			onboardingMgr: onboarding.NewManager(home),
			homePath:      home,
		}

		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions",
			bytes.NewBufferString(`{"agent_id":"`+bindingAgentID+`","workspace_id":"nosuchworkspaceatall"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		api.createSessionHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code,
			"a session must never be stamped with a workspace that is not there")
	})
}
