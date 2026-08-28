// This test file uses //go:build !cgo so it compiles when CGO is disabled (see
// rest_agent_reload_race_test.go's header comment for the full explanation of
// why: pkg/gateway imports pkg/channels/matrix, which requires libolm when CGO
// is enabled).
//
// Regression coverage for the SAME "reload race" defect class documented in
// rest_agent_reload_race_test.go (createAgent / updateAgent / updateAgentTools),
// closed here at five more call sites that persist config and call
// a.agentLoop.TriggerReload():
//
//  1. setGodMode (rest_god_mode.go)      — POST /api/v1/gateway/god-mode
//  2. putToolPolicies (rest_tool_policies.go) — PUT /api/v1/security/tool-policies
//  3. setAgentMailbox (rest_mailbox.go)  — PUT /api/v1/agents/{id}/mailboxes/{ws}
//  4. deleteAgentMailbox (rest_mailbox.go) — DELETE /api/v1/agents/{id}/mailboxes/{ws}
//  5. the provider-update branch of HandleProviders (rest.go) — PUT /api/v1/providers/{id}
//
// TriggerReload's underlying reloadFunc only enqueues work onto a buffered
// channel in production (runningServices.reloadTrigger, pkg/gateway/gateway.go)
// — the real work (registry rebuild via ReloadProviderAndConfig, which swaps
// al.cfg AND rebuilds every AgentInstance, including each instance's
// boot-time-snapshotted ToolPolicyCfg — see agentToolsCfgToPolicy) runs later
// on a separate consumer goroutine. A handler that fires TriggerReload and
// returns 200 is telling the client "this is enforced now" before that swap
// has actually happened. All five sites below now use triggerReloadAndWait
// (rest_auth.go), which polls IsReloadPending() until the SAME registry swap
// completes (or a 5s deadline) — this file proves, per site, that the
// observable IsReloadPending() has cleared by the time the handler responds.
//
// wireAsyncReload (defined in rest_agent_reload_race_test.go, same package) is
// reused here unmodified: it wires a.agentLoop's reload function to return
// immediately while the real registry swap + ClearReloadPending happen on a
// delayed goroutine, deterministically reproducing the production gap without
// any time.Sleep in the production code under test.

package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// TestGodMode_POST_ReloadCompletesBeforeResponse proves site 1: setGodMode
// (O14, the highest-blast-radius runtime switch in the product) must not
// respond 200 until the registry rebuild that actually flips every agent
// instance's baked-in ToolPolicyCfg.GodMode flag (agentToolsCfgToPolicy) has
// completed. Before the fix, a tool call dispatched the instant this handler
// responded could still be evaluated under the PREVIOUS god-mode state.
func TestGodMode_POST_ReloadCompletesBeforeResponse(t *testing.T) {
	if !sandbox.GodModeAvailable {
		t.Skip("requires GodModeAvailable=true (default build)")
	}
	api := newTestRestAPIWithHome(t)
	api.allowGodMode = true
	wireAsyncReload(t, api, 30*time.Millisecond)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/gateway/god-mode", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	req = withReAuthAdmin(t, api, req)
	w := httptest.NewRecorder()
	api.HandleGodMode(w, req)

	require.Equal(t, http.StatusOK, w.Code, "god-mode enable must succeed; body: %s", w.Body.String())
	assert.False(t, api.agentLoop.IsReloadPending(),
		"setGodMode returned 200 while a config reload was still pending — a tool call dispatched "+
			"immediately after this response could still be evaluated under the PREVIOUS god-mode "+
			"state (each agent instance's ToolPolicyCfg.GodMode flag is only rebuilt by the async "+
			"registry swap, never by SwapConfig alone)")
}

// TestPutToolPolicies_ReloadCompletesBeforeResponse proves site 2:
// putToolPolicies must not respond 200 until the registry rebuild that
// actually swaps every agent instance's ToolPolicyCfg.GlobalPolicies has
// completed — otherwise a global tightening edit (e.g. exec: allow -> deny)
// would be persisted and shown enforced by a subsequent GET while a tool call
// racing the response could still execute under the previous, looser policy.
func TestPutToolPolicies_ReloadCompletesBeforeResponse(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wireAsyncReload(t, api, 30*time.Millisecond)

	body := `{"policies":{"exec":"deny","search_web":"allow"}}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/tool-policies", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withReAuthAdmin(t, api, r)
	w := httptest.NewRecorder()
	api.HandleToolPolicies(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.False(t, api.agentLoop.IsReloadPending(),
		"putToolPolicies returned 200 while a config reload was still pending — a tightened global "+
			"policy is not guaranteed to be enforced on the very next tool call")
}

// TestSetAgentMailbox_ReloadCompletesBeforeResponse proves site 3:
// setAgentMailbox must not respond 200 until the registry rebuild that
// actually (de)registers the M11 email tools on the agent instance
// (registerEmailToolsForAgent, pkg/agent/email_tools.go — only runs during
// NewAgentRegistry construction) has completed. Before the fix, a client that
// enabled a mailbox and immediately asked the agent to send an email could hit
// a "tool not registered" gap for as long as the async rebuild took to run.
func TestSetAgentMailbox_ReloadCompletesBeforeResponse(t *testing.T) {
	api := newMailboxTestAPI(t, nil)
	seedWorkspaceFile(t, api.homePath, "ws_my")
	wireAsyncReload(t, api, 30*time.Millisecond)

	body := `{"enabled":true,"imap_host":"imap.x.com","smtp_host":"smtp.x.com","username":"me@x.com","password":"app-pass-123"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/mia/mailboxes/ws_my", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.setAgentMailbox(w, r, "mia", "ws_my")

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.False(t, api.agentLoop.IsReloadPending(),
		"setAgentMailbox returned 200 while a config reload was still pending — the newly enabled "+
			"mailbox's email tools are not guaranteed to be registered on the running agent instance yet")
}

// TestDeleteAgentMailbox_ReloadCompletesBeforeResponse proves site 4:
// deleteAgentMailbox must not report success until the registry rebuild that
// actually deregisters the removed mailbox's email tools has completed.
// Before the fix, the running agent instance could keep the email tools live
// (registered at the PRIOR construction, before the mailbox was removed) for
// as long as the async rebuild took to run, even though the handler already
// reported the mailbox gone.
func TestDeleteAgentMailbox_ReloadCompletesBeforeResponse(t *testing.T) {
	api := newMailboxTestAPI(t, map[string]map[string]config.MailboxConfig{
		"mia": {"ws_my": {
			Enabled: true, WorkspaceID: "ws_my", IMAPHost: "i", SMTPHost: "s",
			Username: "u", PasswordRef: "mailbox_mia_ws_my_password",
		}},
	})
	require.NoError(t, api.credStore.Set("mailbox_mia_ws_my_password", "secret"))
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		m["mailboxes"] = map[string]any{
			"mia": map[string]any{"ws_my": map[string]any{"enabled": true, "workspace_id": "ws_my"}},
		}
		return nil
	}))
	wireAsyncReload(t, api, 30*time.Millisecond)

	w := httptest.NewRecorder()
	api.deleteAgentMailbox(w, "mia", "ws_my")

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.False(t, api.agentLoop.IsReloadPending(),
		"deleteAgentMailbox reported success while a config reload was still pending — the removed "+
			"mailbox's email tools could remain registered on the running agent instance")
}

// TestProvidersPUT_ReloadCompletesBeforeResponse proves site 5: the
// provider-update branch of HandleProviders must not respond 200 until the
// registry rebuild that actually replaces each already-constructed agent
// instance's cached provider/model client has completed (mirrors updateAgent's
// documented "SwapConfig alone does not touch an instance's cached provider
// client" gap). Before the fix, a client that fixed a revoked API key and
// immediately sent a chat message could still be served by the stale,
// previously-cached client for as long as the async rebuild took to run.
func TestProvidersPUT_ReloadCompletesBeforeResponse(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	require.NoError(t, api.safeUpdateConfigJSON(func(m map[string]any) error {
		m["providers"] = []any{map[string]any{"model_name": "mygw", "provider": "mygw", "model": "mygw/a"}}
		return nil
	}))
	wireAsyncReload(t, api, 30*time.Millisecond)

	body := `{"models":["mygw/a","mygw/b"]}`
	r := httptest.NewRequest(http.MethodPut, "/api/v1/providers/mygw", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/providers/mygw"
	w := httptest.NewRecorder()
	api.HandleProviders(w, isolateRateLimit(t, r))

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.False(t, api.agentLoop.IsReloadPending(),
		"provider PUT returned 200 while a config reload was still pending — an already-constructed "+
			"agent instance's cached provider/model client is not guaranteed to be rebuilt yet")
}
