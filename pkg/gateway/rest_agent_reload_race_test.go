// This test file uses //go:build !cgo so it compiles when CGO is disabled.
// When CGO is enabled, pkg/gateway imports pkg/channels/matrix which requires
// the libolm system library (olm/olm.h). If that library is installed,
// remove this build constraint and run tests normally.
//
// Regression coverage for the createAgent/updateAgent/updateAgentTools "reload
// race": POST /api/v1/agents (and its siblings that persist agent config and
// call TriggerReload) historically fired the config reload and returned
// success WITHOUT waiting for the in-memory AgentRegistry to actually pick up
// the change. In production, TriggerReload's underlying reloadFunc only
// enqueues work onto a buffered channel (runningServices.manualReloadChan,
// pkg/gateway/gateway.go) consumed by a separate goroutine that performs the
// real registry rebuild (executeReload -> handleConfigReload ->
// ReloadProviderAndConfig) — so "TriggerReload returned nil" and "the
// registry now contains the new/updated agent" are DIFFERENT events,
// separated by however long that consumer goroutine takes to run (observed
// live: up to ~300ms). A client that creates an agent and immediately opens a
// session against it (POST /api/v1/sessions -> GetAgentStore -> the SAME
// registry) could get a spurious 400 "agent not found" for an agent that had
// already been durably persisted and 201-Created.
//
// deleteAgent already closed this gap via triggerReloadAndWait (rest_auth.go),
// which polls IsReloadPending() until the SAME registry swap actually
// completes (or a 5s deadline). createAgent, updateAgent (its Soul-triggered
// reload branch), and updateAgentTools did not use it — this file proves the
// race on createAgent end-to-end via the real HTTP path, and proves the same
// "handler returns before the registry sync completes" defect on updateAgent
// and updateAgentTools via the IsReloadPending() observable.

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// wireAsyncReload wires api.agentLoop's reload function to mimic the shape of
// the REAL production reload pipeline: the function returns immediately
// (like runningServices.reloadTrigger's buffered channel send in
// pkg/gateway/gateway.go), while the actual registry rebuild — reading the
// just-persisted config.json and swapping the live registry via
// ReloadProviderAndConfig, then ClearReloadPending() — happens on a separate
// goroutine after reloadDelay. This reproduces the real gap (TriggerReload
// returning success is not the same event as "the registry observed the
// change") deterministically instead of relying on incidental goroutine
// scheduling: reloadDelay (a handful of milliseconds) is comfortably larger
// than the microseconds an httptest round trip through the handler takes, so
// an immediate follow-up call is guaranteed to run before the fake worker's
// registry swap — a bug in the handler under test (not a flaky race) is what
// makes the assertions below fail without the fix. This delay lives only in
// the test's stand-in for the real gateway's manualReloadChan consumer loop —
// it is not, and must not become, a time.Sleep in the production fix itself.
func wireAsyncReload(t *testing.T, api *restAPI, reloadDelay time.Duration) {
	t.Helper()
	api.agentLoop.SetReloadFunc(func() error {
		go func() {
			time.Sleep(reloadDelay)
			newCfg, err := config.LoadConfig(api.configPath())
			if err != nil {
				api.agentLoop.ClearReloadPending()
				return
			}
			// ReloadProviderAndConfig performs the same atomic registry swap
			// production's handleConfigReload does (pkg/agent/loop.go); the mock
			// provider mirrors what mustAgentLoop already wired the loop with.
			if rlErr := api.agentLoop.ReloadProviderAndConfig(
				context.Background(), &restMockProvider{}, newCfg,
			); rlErr != nil {
				api.agentLoop.ClearReloadPending()
				return
			}
			// Mirrors executeReload's deferred ClearReloadPending: fires only
			// after the registry swap above has fully completed.
			api.agentLoop.ClearReloadPending()
		}()
		return nil
	})
}

// TestCreateAgent_ImmediateSessionCreate_NoRace is the primary reproduction:
// create an agent, then — with no sleep at all — immediately try to open a
// chat session against it via the real POST /api/v1/sessions path
// (createSessionHTTP -> GetAgentStore -> GetRegistry -> the registry
// ReloadProviderAndConfig swaps). This must succeed: a 201 from POST
// /api/v1/agents is a durability+resolvability guarantee, not a "check back
// later" promise.
func TestCreateAgent_ImmediateSessionCreate_NoRace(t *testing.T) {
	api := buildExecutorTestAPI(t)
	wireAsyncReload(t, api, 30*time.Millisecond)

	body := `{"name":"RaceAgent","type":"Main","soul":"soul content"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader([]byte(body)))
	r.Header.Set("Content-Type", "application/json")
	api.createAgent(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "agent create failed: %s", w.Body.String())

	var created gen.Agent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	require.NotEmpty(t, created.Id, "created agent must have an id")

	// No sleep here — this is the exact sequence the bug report reproduced via
	// curl (create, then immediately use).
	sessBody, err := json.Marshal(gen.SessionCreateRequest{AgentId: &created.Id})
	require.NoError(t, err)
	sw := httptest.NewRecorder()
	sr := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", bytes.NewReader(sessBody))
	sr.Header.Set("Content-Type", "application/json")
	api.createSessionHTTP(sw, sr)

	assert.Equal(t, http.StatusCreated, sw.Code,
		"POST /api/v1/sessions immediately after POST /api/v1/agents returned %d (expected 201) — "+
			"the new agent must be resolvable via the runtime registry the instant createAgent "+
			"responds 201, not up to ~reloadDelay later: %s",
		sw.Code, sw.Body.String())
}

// TestUpdateAgent_SoulChange_RegistryReloadCompletesBeforeResponse proves the
// same "handler returns before the async reload actually lands" defect on the
// PUT /api/v1/agents/{id} Soul-change path (gen.AgentUpdateRequest.Soul:
// "Writing this triggers a config reload."). IsReloadPending() is the direct
// observable of the SAME registry-swap mechanism createAgent races on: if the
// handler returns while a reload is still pending, any other in-flight
// request depending on the reloaded registry (or on updateAgent's own
// post-reload reads of a.agentLoop.GetConfig(), see rest.go) can still
// observe stale state.
func TestUpdateAgent_SoulChange_RegistryReloadCompletesBeforeResponse(t *testing.T) {
	api := buildExecutorTestAPI(t)
	wireAsyncReload(t, api, 30*time.Millisecond)

	newSoul := "updated soul content"
	body, err := json.Marshal(gen.AgentUpdateRequest{Soul: &newSoul})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.updateAgent(w, r, "test-agent")

	require.Equal(t, http.StatusOK, w.Code, "agent update failed: %s", w.Body.String())
	assert.False(t, api.agentLoop.IsReloadPending(),
		"updateAgent (Soul change) returned 200 while a config reload was still pending — "+
			"callers relying on the registry immediately after this response can still see "+
			"pre-update state")
}

// TestUpdateAgentTools_RegistryReloadCompletesBeforeResponse proves the same
// defect class on PUT /api/v1/agents/{id}/tools: a security-relevant
// tool-policy tightening must be actually enforced by the time the write
// responds 200, not merely persisted-to-disk-and-reload-queued.
func TestUpdateAgentTools_RegistryReloadCompletesBeforeResponse(t *testing.T) {
	api := buildExecutorTestAPI(t)
	wireAsyncReload(t, api, 30*time.Millisecond)

	policies := coreagent.NewCustomAgentToolsCfg().Builtin.Policies
	reqBody := map[string]any{
		"builtin": map[string]any{"policies": policies},
	}
	raw, err := json.Marshal(reqBody)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent/tools", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)
	api.updateAgentTools(w, r, "test-agent")

	require.Equal(t, http.StatusOK, w.Code, "agent tools update failed: %s", w.Body.String())
	assert.False(t, api.agentLoop.IsReloadPending(),
		"updateAgentTools returned 200 while a config reload was still pending — the tightened "+
			"tool policy is not guaranteed to be enforced yet on the next tool call")
}

// TestUpdateAgentTools_ReloadTimeout_Returns503NotSilent200 is the UAT
// batch3 S67 (docs/internal/qa/uat-report-full-tool-catalog-batch3-2026-09-02.md,
// finding #4) hardening test: before this fix, when the reload confirmation
// poll window elapsed WITHOUT an error (triggerReloadAndWaitOutcome
// returning confirmed=false, err=nil — see waitForReloadOutcome's own doc
// comment), updateAgentTools logged only a server-side Warn and still
// responded 200. A caller had no way to know a tool-policy TIGHTENING
// (e.g. create_skill allow/deny -> ask) they just requested might not be
// enforced yet — a tool call dispatched immediately after could still run
// under the stale, more permissive snapshot. This mirrors putToolPolicies
// (the global tool-policy PUT), which already treats an unconfirmed reload
// as a hard failure via the stricter triggerReloadAndWait.
//
// The fix makes the unconfirmed branch return 503, matching the existing
// err!=nil branch's status and response shape, rather than falling through
// to the same 200 the confirmed path returns.
func TestUpdateAgentTools_ReloadTimeout_Returns503NotSilent200(t *testing.T) {
	api := buildExecutorTestAPI(t)

	prevDeadline := reloadWaitTimeout
	reloadWaitTimeout = 150 * time.Millisecond
	t.Cleanup(func() { reloadWaitTimeout = prevDeadline })

	// Wire a reload func that marks the reload pending (mirroring production
	// — TriggerReload itself deliberately does not, see its own doc comment)
	// and deliberately never clear the pending flag, so
	// triggerReloadAndWaitOutcome runs out the (shortened) poll window with
	// confirmed=false, err=nil — the exact "timed out, not errored" case
	// this fix closes.
	api.agentLoop.SetReloadFunc(func() error {
		api.agentLoop.MarkReloadPending()
		return nil
	})
	t.Cleanup(func() { api.agentLoop.ClearReloadPending() })

	policies := coreagent.NewCustomAgentToolsCfg().Builtin.Policies
	reqBody := map[string]any{
		"builtin": map[string]any{"policies": policies},
	}
	raw, err := json.Marshal(reqBody)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent/tools", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)
	api.updateAgentTools(w, r, "test-agent")

	assert.Equal(t, http.StatusServiceUnavailable, w.Code,
		"an unconfirmed reload must NOT be reported as a plain 200 success — the caller cannot tell "+
			"whether the tightened tool policy is actually enforced yet: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "did not confirm",
		"the response body must explain the reload did not confirm within the wait window")
}
