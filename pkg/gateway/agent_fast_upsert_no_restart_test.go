// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Issue #571 — "property not mechanism" proof.
//
// The CI llm-conformance shard's Conformance_t3_PlanningReplanningE2E test
// failed deterministically (3/3: initial + both retries) with a severed
// socket on its very first setup step, POST /api/v1/agents. The gateway log
// showed the full-service restart cascade (channels, cron, the plan engine,
// task trigger scheduler, loop scheduler, queued-task drain, mailbox drain —
// all restarting) repeating on every create under shard load, severing the
// request before it completed.
//
// A test asserting only "the created agent is usable" does not prove the
// cascade is gone — a full reload that happens to complete fast enough would
// pass that assertion too. These tests assert the CASCADE ITSELF never runs
// for a plain create/update (no concurrent reload in flight): the reload
// trigger — the ONLY thing that can invoke handleConfigReload's
// restartServices — must be called ZERO times.
// ---------------------------------------------------------------------------

// TestCreateAgent_DoesNotTriggerFullReload proves a plain POST /agents (no
// concurrent reload in flight) never invokes the reload trigger at all, and
// therefore can never reach the restartServices cascade that severed
// requests under CI load. The agent must still be immediately resolvable via
// the fast path alone (AgentLoop.UpsertAgentFast), with zero reloads.
func TestCreateAgent_DoesNotTriggerFullReload(t *testing.T) {
	api := buildExecutorTestAPI(t)

	var reloadCalls atomic.Int32
	api.agentLoop.SetReloadFunc(func() error {
		reloadCalls.Add(1)
		return nil
	})

	body := `{"name":"NoCascadeTest","type":"Main","soul":"soul content"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	resp := decodeAgentResp(t, w.Body.Bytes())
	assert.Nil(t, resp.Warning, "a plain create must not carry a warning: %+v", resp.Warning)

	assert.Equal(t, int32(0), reloadCalls.Load(),
		"a plain POST /agents create (no concurrent reload in flight) must NEVER invoke the reload "+
			"trigger — invoking it is what reaches handleConfigReload's restartServices cascade "+
			"(channels/cron/plan-engine/task-trigger/loop-scheduler/task-drain/mailbox-drain all "+
			"restart), the exact mechanism that severed POST /agents requests under CI load "+
			"(Conformance_t3_PlanningReplanningE2E, llm-conformance shard, socket hang up)")

	_, ok := api.agentLoop.GetRegistry().GetAgent(resp.Id)
	require.True(t, ok, "the new agent must be resolvable in the registry via the fast path alone, with zero reloads")
}

// TestUpdateAgent_SoulChange_DoesNotTriggerFullReload is the update-path
// counterpart: a soul change (one of the two updateAgent field changes that
// used to force a full reload — the other being a default-agent-ID flip)
// must also complete via the fast path with zero reload-trigger invocations
// when no reload is already in flight.
func TestUpdateAgent_SoulChange_DoesNotTriggerFullReload(t *testing.T) {
	api := buildExecutorTestAPI(t)

	var reloadCalls atomic.Int32
	api.agentLoop.SetReloadFunc(func() error {
		reloadCalls.Add(1)
		return nil
	})

	body := `{"soul":"an updated soul, no cascade expected"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	resp := decodeAgentResp(t, w.Body.Bytes())
	assert.Nil(t, resp.Warning, "a plain soul update must not carry a warning: %+v", resp.Warning)

	assert.Equal(t, int32(0), reloadCalls.Load(),
		"a soul-only PUT /agents/{id} (no concurrent reload in flight) must NEVER invoke the reload "+
			"trigger — see TestCreateAgent_DoesNotTriggerFullReload for the full restartServices-cascade "+
			"rationale")

	inst, ok := api.agentLoop.GetRegistry().GetAgent("test-agent")
	require.True(t, ok, "the updated agent must remain resolvable via the fast path alone, with zero reloads")
	require.NotNil(t, inst)
}
