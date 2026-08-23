// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RELEASE BLOCKER regression test: the Agents-screen ★ "default agent" toggle
// persisted a value, returned 200, and toasted "Default agent updated" — but
// changed NO routing. PUT /api/v1/agents/{id} {"default":true} wrote the
// per-entity AgentConfig.Default bool (and, before this fix, ran a racy N-write
// fan-out clearing it on every other agent's entity record), but NOTHING
// assigned config.Agents.Defaults.DefaultAgentID — the settings singleton that
// ADR-054 D6.4 made the ONLY thing pkg/agent.AgentRegistry.GetDefaultAgent and
// pkg/routing.RouteResolver.resolveDefaultAgentID consult. With that singleton
// permanently empty, the two ladders fell back to DIFFERENT Priority-2
// defaults: GetDefaultAgent to the unconditionally-registered "main" sentinel,
// resolveDefaultAgentID to the first chat-target agent in cfg.Agents.List
// order — disagreeing on every install, exactly the ADR-037 anti-pattern
// CLAUDE.md bans ("the global screen looked functional ... but had zero effect").
//
// This file exercises the REAL write path (updateAgent's PUT handler) and then
// checks agreement using the same two production entry points a fresh
// boot/reload would use (pkg/agent.NewAgentRegistry + GetDefaultAgent +
// ResolveRoute), independent of whether this lightweight test harness's
// AgentLoop has a reload function wired (mustAgentLoop never calls
// SetReloadFunc — that only happens in pkg/gateway's real boot sequence).
package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/routing"
)

// TestUpdateAgent_DefaultToggle_RegistryAndRoutingAgree is the DoD test for the
// ★ default-agent release blocker: it FAILS before the fix (registry and
// routing disagree, as described above) and PASSES after it (both ladders
// resolve the just-starred agent).
//
// BDD: Given two ordinary (non-worker) agents and NO configured default
// (agents.defaults.default_agent_id is empty — the exact precondition every
// fresh/existing install has before this fix, since nothing ever wrote it),
//
//	When PUT /api/v1/agents/agent-b with {"default": true} (the ★ toggle),
//	Then the settings singleton (agents.defaults.default_agent_id) is
//	persisted as "agent-b" — the write half of the bug — and a fresh registry
//	built from that persisted config resolves BOTH
//	pkg/agent.AgentRegistry.GetDefaultAgent() (the webchat ladder) AND
//	pkg/agent.AgentRegistry.ResolveRoute()'s fallback tier (the channel-routing
//	ladder, mirroring pkg/routing.RouteResolver.resolveDefaultAgentID) to the
//	SAME agent — agent-b.
func TestUpdateAgent_DefaultToggle_RegistryAndRoutingAgree(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 20,
				// Deliberately no DefaultAgentID: this is the exact
				// precondition of the bug — no operator has ever starred an
				// agent, and (pre-fix) coreagent.SeedConfig never wrote this
				// either, so it is empty on every affected install.
			},
			// List order is deliberately "agent-b" THEN "agent-a" — the
			// opposite of lexicographic order. The retired "main" sentinel
			// used to be GetDefaultAgent's own Priority-2 fallback (disagreeing
			// with resolveDefaultAgentID's first-in-list-order fallback on
			// EVERY install, regardless of naming); with the sentinel gone,
			// GetDefaultAgent's Priority 2 is now "lexicographically first
			// registered non-worker agent" while resolveDefaultAgentID's is
			// still "first chat-target agent in cfg.Agents.List order" (see
			// both doc comments — pkg/agent/registry.go::GetDefaultAgent and
			// pkg/routing/route.go::resolveDefaultAgentID). The two orderings
			// only disagree when list order isn't already lexicographic, so
			// this fixture orders the list backwards on purpose to keep
			// reproducing the "the two ladders can disagree with no configured
			// override" precondition this test exists to guard.
			List: []config.AgentConfig{
				{ID: "agent-b", Name: "Agent B"},
				{ID: "agent-a", Name: "Agent A"},
			},
		},
	}
	require.NoError(t, os.WriteFile(cfgPath, marshalConfigForDisk(t, cfg), 0o600))

	msgBus := bus.NewMessageBus()
	provider := &restMockProvider{}
	al := mustAgentLoop(t, cfg, msgBus, provider)
	api := &restAPI{agentLoop: al, homePath: tmpDir}
	seedRoutingAgentEntities(t, tmpDir, cfg.Agents.List)

	// The precondition INVERTED, deliberately.
	//
	// This block used to assert that the two ladders DISAGREE before the ★
	// toggle — that was the release blocker's exact claim, and the fixture
	// reverses cfg.Agents.List specifically to reproduce it: GetDefaultAgent
	// took the lexicographically-first agent while ResolveRoute took the first
	// in slice order.
	//
	// That divergence is now FIXED (ADR-064 §7): both ladders apply the same
	// eligibility rule and both sort, so they agree with or without an
	// override. Asserting they still disagree would pin a bug as if it were a
	// requirement, so this asserts the property that replaced it.
	//
	// The test's actual subject is unchanged and follows below: setting the
	// default via ★ must move BOTH ladders to the chosen agent.
	preRegistry := agent.NewAgentRegistry(al.GetConfig(), provider)
	preDefault := preRegistry.GetDefaultAgent()
	require.NotNil(t, preDefault)
	preResolved := preRegistry.ResolveRoute(routing.RouteInput{Channel: "telegram", AccountID: "*"})
	preRegistry.Close()
	require.Equal(t, preDefault.ID, preResolved.AgentID,
		"with no configured default the two ladders must AGREE: they answer the same question "+
			"and both now fall back to the lexicographically-first chat-target agent. This "+
			"fixture reverses cfg.Agents.List precisely because slice order used to change "+
			"routing's answer — if that ever returns, this fails here")

	// The ★ toggle: PUT /api/v1/agents/agent-b {"default": true}.
	body := `{"default": true}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/agent-b", strings.NewReader(body))
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "starring agent-b as default must succeed")

	var resp gen.Agent
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.NotNil(t, resp.Default, "response.default must be non-nil")
	assert.True(t, *resp.Default, "PUT response must echo default=true for agent-b")

	// Write half of the bug: the toggle must persist the settings singleton,
	// not just the per-entity display flag. a.agentLoop.GetConfig() already
	// reflects the PUT's write (updateConfigJSONLocked calls
	// refreshConfigAndRewireServices, which reloads config.json and swaps it
	// in via SwapConfig) — no extra reload step is needed to observe it here.
	freshCfg := al.GetConfig()
	require.Equal(t, "agent-b", freshCfg.Agents.Defaults.DefaultAgentID,
		"the star toggle must persist agents.defaults.default_agent_id — this is the write half of the bug")

	// Read half of the bug: build a registry the same way the NEXT full
	// reload/boot would (pkg/gateway's gateway.go and pkg/agent's
	// ReloadProviderAndConfig both do exactly this: NewAgentRegistry, then
	// apply the configured override — see loop.go's NewAgentLoop /
	// ReloadProviderAndConfig, which this mirrors since this lightweight test
	// harness never wires a real reload function).
	freshRegistry := agent.NewAgentRegistry(freshCfg, provider)
	t.Cleanup(freshRegistry.Close)
	if freshCfg.Agents.Defaults.DefaultAgentID != "" {
		freshRegistry.SetDefaultAgentOverride(freshCfg.Agents.Defaults.DefaultAgentID)
	}

	// Ladder 1: webchat / GetDefaultAgent.
	defaultAgent := freshRegistry.GetDefaultAgent()
	require.NotNil(t, defaultAgent, "GetDefaultAgent must resolve some agent")

	// Ladder 2: channel routing, via ResolveRoute's fallback tier (no
	// bindings configured, so the whole cascade falls through to
	// resolveDefaultAgentID — matchedBy=="default").
	resolved := freshRegistry.ResolveRoute(routing.RouteInput{Channel: "telegram", AccountID: "*"})
	assert.Equal(t, "default", resolved.MatchedBy, "no bindings are configured; must resolve via the default tier")

	assert.Equal(t, "agent-b", defaultAgent.ID,
		"registry.GetDefaultAgent must resolve to the agent that was just starred")
	assert.Equal(t, "agent-b", resolved.AgentID,
		"routing's resolveDefaultAgentID (via ResolveRoute) must resolve to the same just-starred agent")
	assert.Equal(t, defaultAgent.ID, resolved.AgentID,
		"THE BUG: webchat (GetDefaultAgent) and channel routing (ResolveRoute) must never disagree "+
			"on who the default agent is")
}

// TestUpdateAgent_DefaultToggle_WireResponseDerivedFromSingleton verifies the
// read half of the fix in isolation: the wire `default` field on GET/PUT
// responses must be DERIVED from the settings singleton
// (agents.defaults.default_agent_id), never echoed from the per-entity
// AgentConfig.Default bool — so a GET on an agent whose entity record still
// carries a stale Default:true (e.g. from before this fix, or simply never
// cleared now that the N-write fan-out is retired) does NOT falsely report
// default=true once another agent holds the singleton.
//
// BDD: Given agent-a's entity record has Default:true (a stale/legacy value)
// but the settings singleton points at agent-b,
//
//	When GET /api/v1/agents/agent-a,
//	Then the response's default field is false (derived from the singleton,
//	not the stale per-entity bool).
func TestUpdateAgent_DefaultToggle_WireResponseDerivedFromSingleton(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 20,
				// The singleton names agent-b as the real default...
				DefaultAgentID: "agent-b",
			},
			List: []config.AgentConfig{
				// ...but agent-a's entity record still carries a stale
				// Default:true (simulating a record written by an older
				// build, or simply never reconciled now that the racy
				// clear-others fan-out is retired).
				{ID: "agent-a", Name: "Agent A", Default: true},
				{ID: "agent-b", Name: "Agent B"},
			},
		},
	}
	require.NoError(t, os.WriteFile(cfgPath, marshalConfigForDisk(t, cfg), 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}
	seedRoutingAgentEntities(t, tmpDir, cfg.Agents.List)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent-a", nil)
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var resp gen.Agent
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.NotNil(t, resp.Default)
	assert.False(t, *resp.Default,
		"agent-a's stale per-entity Default:true must NOT leak onto the wire; "+
			"the response must be derived from the settings singleton (which names agent-b)")

	// Control: agent-b (named by the singleton, zero-value per-entity Default)
	// must report true.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent-b", nil)
	api.HandleAgents(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)
	var respB gen.Agent
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&respB))
	require.NotNil(t, respB.Default)
	assert.True(t, *respB.Default,
		"agent-b has no per-entity Default flag set at all, but IS named by the singleton, "+
			"so the derived wire value must be true")
}
