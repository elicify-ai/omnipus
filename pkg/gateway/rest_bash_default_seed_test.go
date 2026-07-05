//go:build !cgo

// Regression test for the CRIT-001/FR-B12 bash:deny seed on the REST
// agent-creation path (POST /api/v1/agents), which is the primary,
// human-facing way agents are created in production (the SPA's "+ Add
// Agent"/"+ Add Subagent" flows call createAgent(), src/lib/api.ts).
//
// Before this fix, pkg/coreagent.NewCustomAgentToolsCfg() — the shared
// helper createAgent (pkg/gateway/rest.go) uses to build a fresh custom
// agent's default tool policy — seeded only {"system.*": "deny"} and did NOT
// seed {"bash": "deny"}. Since pkg/tools/compositor.go's passesScopeGate does
// not hard-deny ScopeCore tools (which "bash" is) for custom agents, an
// unlisted "bash" policy entry fell through to DefaultPolicy (allow),
// leaving bash reachable with zero configuration on every agent created via
// the actual product UI. This mirrors TestBash_NewCustomAgentDeniedByDefault
// (pkg/sysagent/tools/agent_test.go), which proves the same invariant on the
// LLM-driven system.agent.create tool path.

package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// TestCreateAgent_Bash_NewCustomAgentDeniedByDefault proves that a fresh
// custom agent created via the REAL REST POST /api/v1/agents handler, with
// no explicit bash policy entry supplied by the caller, resolves the bash
// tool to "deny" by default.
func TestCreateAgent_Bash_NewCustomAgentDeniedByDefault(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// Deliberately no tools_cfg override — proving the DEFAULT seed, not a
	// caller-supplied one.
	body := `{"name":"Research Bot","type":"Main","description":"A research assistant","soul":"You are a research bot."}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())

	created := decodeAgentResp(t, w.Body.Bytes())

	// Load-bearing assertion: read the persisted agent back out of the LIVE
	// config (createAgent's TriggerReload() has already run by the time the
	// handler returns), the same way the frontend's subsequent GET/list would.
	cfg := api.agentLoop.GetConfig()
	var newAgent *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == created.Id {
			newAgent = &cfg.Agents.List[i]
			break
		}
	}
	require.NotNil(t, newAgent, "created agent must be present in the reloaded live config")
	require.NotNil(t, newAgent.Tools, "created agent must have a Tools config")

	// Sanity: the seed actually landed in the persisted policy map.
	require.Equal(t, config.ToolPolicyDeny, newAgent.Tools.Builtin.Policies["bash"],
		`expected seeded Tools.Builtin.Policies["bash"] = deny`)

	// Resolve through the single authoritative primitive
	// (pkg/tools/compositor.go's EffectiveToolPolicy), built the same way
	// pkg/agent/instance.go's agentToolsCfgToPolicy converts
	// AgentBuiltinToolsCfg for a non-god-mode agent.
	policies := make(map[string]string, len(newAgent.Tools.Builtin.Policies))
	for k, v := range newAgent.Tools.Builtin.Policies {
		policies[k] = string(v)
	}
	polCfg := &tools.ToolPolicyCfg{
		DefaultPolicy:       string(newAgent.Tools.Builtin.DefaultPolicy),
		Policies:            policies,
		GlobalDefaultPolicy: "allow",
	}
	agentType := string(newAgent.ResolveType(nil))
	require.Equal(t, string(config.AgentTypeCustom), agentType,
		"test setup invariant: a REST-created Main agent resolves to AgentTypeCustom")

	got := tools.EffectiveToolPolicy(polCfg, tools.ScopeCore, agentType, "bash")
	require.Equal(t, "deny", got,
		`EffectiveToolPolicy(bash, ScopeCore, %q) must be "deny"`, agentType)
}

// TestCreateAgent_Bash_CallerOverrideRespected proves the seed is a
// default, not a hard rail: a caller that explicitly sets bash:allow via
// tools_cfg on create gets that value, not the seeded deny.
func TestCreateAgent_Bash_CallerOverrideRespected(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Bash Enabled Bot","type":"Main","description":"Needs bash","soul":"s",` +
		`"tools_cfg":{"builtin":{"default_policy":"allow","policies":{"bash":"allow"}}}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())

	created := decodeAgentResp(t, w.Body.Bytes())

	cfg := api.agentLoop.GetConfig()
	var newAgent *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == created.Id {
			newAgent = &cfg.Agents.List[i]
			break
		}
	}
	require.NotNil(t, newAgent, "created agent must be present in the reloaded live config")
	require.NotNil(t, newAgent.Tools, "created agent must have a Tools config")
	require.Equal(t, config.ToolPolicyAllow, newAgent.Tools.Builtin.Policies["bash"],
		"explicit caller override must win over the default deny seed")
	// system.* seed must still be present (caller did not override it).
	require.Equal(t, config.ToolPolicyDeny, newAgent.Tools.Builtin.Policies["system.*"],
		"unrelated seed entries must survive a partial caller override")
}
