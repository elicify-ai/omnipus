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

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
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
	// AgentBuiltinToolsCfg for a non-god-mode agent. There is no
	// DefaultPolicy/GlobalDefaultPolicy field any more (CLAUDE.md hard
	// constraint 6) — an uncovered tool now fails closed to "deny" inside
	// EffectiveToolPolicy itself, so this test's seeded "bash": deny entry is
	// asserted directly rather than via a fallback field.
	policies := make(map[string]config.ToolPolicy, len(newAgent.Tools.Builtin.Policies))
	for k, v := range newAgent.Tools.Builtin.Policies {
		policies[k] = v
	}
	polCfg := &tools.ToolPolicyCfg{
		Policies: policies,
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

	// There is no default_policy field on the wire any more (CLAUDE.md hard
	// constraint 6) — createAgent's strict decode (decodeAgentCreateVariant,
	// DisallowUnknownFields, independent of ValidateInbound) rejects a stray
	// "default_policy" key inside tools_cfg.builtin with 400, so it must not
	// appear here.
	//
	// CONTRACT CHANGE (2026-09-02): this body used to be the SPARSE
	// `{"bash":"allow"}` map, relying on createAgent merging it on top of the
	// deny-seeded baseline. That merge ran BEFORE validation, which is exactly
	// how a caller-side gap became structurally undetectable — a live UAT round
	// created an agent with `bash` omitted entirely and got 201, with the
	// server silently filling a value nobody sent. A submitted policy map is
	// now validated for completeness before any merge, so the caller must
	// enumerate the whole static catalog explicitly.
	//
	// The claim this test makes is UNCHANGED and still exercised: the seed is a
	// default, not a hard rail — a caller who explicitly says bash:allow gets
	// allow, and every tool they left at deny stays denied.
	policies := fullBuiltinPolicyMap("deny")
	policies["bash"] = "allow"
	body := `{"name":"Bash Enabled Bot","type":"Main","description":"Needs bash","soul":"s",` +
		`"tools_cfg":{"builtin":{"policies":` + mustPolicyJSON(t, policies) + `}}}`
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
	// An unrelated seed entry must still be present at its seeded "deny"
	// value (the caller only overrode "bash"). "system.*" was the OLD,
	// retired wildcard mechanism (matched zero real tool names) this test
	// used to check — the current seed (coreagent.NewCustomAgentToolsCfg,
	// denyAllThenOverride) is a fully-enumerated, wildcard-free map, so the
	// equivalent "untouched" check is any other real tool name that stays at
	// its seeded deny default.
	require.Equal(t, config.ToolPolicyDeny, newAgent.Tools.Builtin.Policies["delete_agent"],
		"a tool the caller left at deny must stay denied")
}
