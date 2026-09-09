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
// ADR-072 Finding A — stale skill-grant cache on a Skills-only config edit.
//
// ContextBuilder.skillAllowed (pkg/agent/context.go) is enforced from a
// skillAllowlist snapshot installed ONCE at agent-instance construction
// (instance.go's contextBuilder.WithSkillAllowlist(agentCfg.Skills)). Before
// this fix, a PUT /api/v1/agents/{id} request that changed ONLY the `skills`
// field was not in updateAgent's needsReload set, so it persisted to config
// (agentRec.Skills is written unconditionally) but never rebuilt the running
// AgentInstance — the Skill tool and the /<skill> command (both gated via
// skillAllowed) kept enforcing the OLD grant list until some unrelated field
// (e.g. soul) forced a reload. grantPredicateFor (pkg/sysagent/tools/skill.go)
// masked this because it re-reads config live per call, so list_skills looked
// correct while Skill/`/<skill>` silently disagreed.
//
// These tests prove a Skills-only edit (a) takes effect on the live,
// already-constructed AgentInstance without requiring any other field to
// change or the process to restart, and (b) does so via the SAME fast,
// no-cascade path (fastAgentUpsert/AgentLoop.UpsertAgentFast) that a soul
// change already used — see agent_fast_upsert_no_restart_test.go for why the
// full restartServices cascade must not fire on a plain update.
// ---------------------------------------------------------------------------

// TestUpdateAgent_SkillsOnlyChange_UpdatesLiveAllowlistWithoutRestart is the
// direct regression test for Finding A: editing only `skills` must refresh
// the live instance's skill grant (mirrored on AgentInstance.SkillsFilter,
// the same source ContextBuilder.WithSkillAllowlist was built from) via the
// fast upsert path, with zero full-reload-trigger invocations.
func TestUpdateAgent_SkillsOnlyChange_UpdatesLiveAllowlistWithoutRestart(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// Sanity: the fixture agent starts with no skill grants at all.
	inst, ok := api.agentLoop.GetRegistry().GetAgent("test-agent")
	require.True(t, ok)
	require.Empty(t, inst.SkillsFilter, "fixture agent must start with no skill grants")

	var reloadCalls atomic.Int32
	api.agentLoop.SetReloadFunc(func() error {
		reloadCalls.Add(1)
		return nil
	})

	body := `{"skills":["summarize","daily-briefing"]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	resp := decodeAgentResp(t, w.Body.Bytes())
	assert.Nil(t, resp.Warning, "a plain skills-only update must not carry a warning: %+v", resp.Warning)

	// The fix: this must be zero, exactly like the soul-change case in
	// agent_fast_upsert_no_restart_test.go — a skills-only edit is now folded
	// into needsReload and goes through fastAgentUpsert, never the cascading
	// full reload trigger.
	assert.Equal(t, int32(0), reloadCalls.Load(),
		"a skills-only PUT /agents/{id} (no concurrent reload in flight) must NEVER invoke the reload "+
			"trigger — see TestCreateAgent_DoesNotTriggerFullReload for the full restartServices-cascade "+
			"rationale")

	// The regression itself: without the fix, this instance would still be the
	// STALE one constructed before the PUT, with SkillsFilter empty and its
	// ContextBuilder's skillAllowlist unrefreshed — Skill/`/<skill>` would keep
	// denying "summarize" and "daily-briefing" despite the grant having just
	// been persisted.
	inst, ok = api.agentLoop.GetRegistry().GetAgent("test-agent")
	require.True(t, ok, "agent must remain resolvable via the fast path alone")
	require.NotNil(t, inst)
	assert.ElementsMatch(t, []string{"summarize", "daily-briefing"}, inst.SkillsFilter,
		"the live AgentInstance's skill grant (SkillsFilter, mirroring the source "+
			"ContextBuilder.skillAllowlist was built from) must reflect the just-persisted grant "+
			"immediately, with no other field changed and no restart")
}

// TestUpdateAgent_SkillsOnlyChange_ClearingGrantsTakesEffectImmediately
// covers the opposite direction: revoking every skill grant (an explicit
// empty array, per updateAgent's "Skills: replace the agent's skill list"
// comment) must also reach the live instance immediately, not just a config
// file nobody re-reads until the next unrelated reload.
func TestUpdateAgent_SkillsOnlyChange_ClearingGrantsTakesEffectImmediately(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// First grant a skill (also exercises the fix on the way up).
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent",
		strings.NewReader(`{"skills":["summarize"]}`))
	r1.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w1, r1)
	require.Equal(t, http.StatusOK, w1.Code, "body: %s", w1.Body.String())

	inst, ok := api.agentLoop.GetRegistry().GetAgent("test-agent")
	require.True(t, ok)
	require.ElementsMatch(t, []string{"summarize"}, inst.SkillsFilter)

	var reloadCalls atomic.Int32
	api.agentLoop.SetReloadFunc(func() error {
		reloadCalls.Add(1)
		return nil
	})

	// Now revoke it with an explicit empty array.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent",
		strings.NewReader(`{"skills":[]}`))
	r2.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code, "body: %s", w2.Body.String())

	assert.Equal(t, int32(0), reloadCalls.Load(),
		"a skills-only revocation must also go through the fast path, never the full reload trigger")

	inst, ok = api.agentLoop.GetRegistry().GetAgent("test-agent")
	require.True(t, ok)
	assert.Empty(t, inst.SkillsFilter,
		"revoking all skill grants must clear the live instance's grant immediately, with no restart")
}
