// Policy-resolver unification parity (#438).
//
// Proves the gateway WS approval-hook resolver (resolveApprovalToolPolicy) and
// the agent-loop tool filter (tools.FilterToolsByPolicy) compute the IDENTICAL
// verdict for the same (agent, tool) after unifying both behind
// tools.EffectiveToolPolicy. If the two resolvers ever drift again, this fails.
//
// IMPORTANT — this is NOT a "no behavior change" assertion for the gateway. The
// OLD gateway resolver matched policy keys by exact-name ONLY (it ignored ".*"
// and "_*" wildcard keys on both the global floor and the agent policy). The new
// path is wildcard-aware, so for wildcard policy keys the gateway exec gate's
// verdict CHANGED — it now ALIGNS to the agent loop's wildcard-aware verdict
// (which always honored wildcards). The wildcard cases below pin that change with
// independent hard-coded expectations and confirm the gateway now agrees with
// FilterToolsByPolicy on them.

package gateway

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// parityScopedTool is a minimal tools.Tool double with a configurable scope, used
// to drive FilterToolsByPolicy with a known scope (the gateway test cannot reach
// pkg/tools' unexported scopedMockTool).
type parityScopedTool struct {
	name  string
	scope tools.ToolScope
}

func (s *parityScopedTool) Name() string               { return s.name }
func (s *parityScopedTool) Description() string        { return "parity scoped tool" }
func (s *parityScopedTool) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (s *parityScopedTool) Scope() tools.ToolScope     { return s.scope }
func (s *parityScopedTool) Category() tools.ToolCategory {
	return tools.CategoryCore
}

func (s *parityScopedTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	return tools.SilentResult("ok")
}

func makeScopedToolForParity(name string, scope tools.ToolScope) tools.Tool {
	return &parityScopedTool{name: name, scope: scope}
}

// parityCase is one (agent, tool) cell. scope is the tool's REAL scope (fed to
// FilterToolsByPolicy via a scoped mock); the gateway resolver only sees the
// name, so the case also documents the expected shared verdict.
type parityCase struct {
	name      string
	toolName  string
	scope     tools.ToolScope
	agentID   string
	agentType string
	wantPol   string
}

// buildParityConfig constructs a *config.Config whose agents mirror the deny- and
// allow-postured fixtures used by the pkg/tools parity matrix, plus a global deny
// floor entry, so the gateway resolver and FilterToolsByPolicy resolve from the
// same intent.
//
// There is no DefaultPolicy/GlobalDefaultPolicy field any more (CLAUDE.md hard
// constraint 6): every tool the parity cases below actually exercise gets an
// explicit, literal Policies entry — including "send_message" (ava) and
// "search_web" (jim), which used to resolve purely through each agent's now-
// removed default. Without an explicit entry a tool fails closed to "deny"
// (tools.resolveEffectivePolicyWith), which would silently break jim's
// allow-by-default parity case.
func buildParityConfig() *config.Config {
	return &config.Config{
		Sandbox: config.OmnipusSandboxConfig{
			// Global deny floor on send_message_blocked to exercise strictest-wins.
			ToolPolicies: map[string]string{"send_message_blocked": "deny"},
		},
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{
					ID:   "ava",
					Type: config.AgentTypeCustom,
					Tools: &config.AgentToolsCfg{
						Builtin: config.AgentBuiltinToolsCfg{
							Policies: map[string]config.ToolPolicy{
								"search_web":           config.ToolPolicyAllow,
								"fetch_url":            config.ToolPolicyAsk,
								"exec_allowed":         config.ToolPolicyAllow,
								"send_message_blocked": config.ToolPolicyAllow, // global deny must still win
								"send_message":         config.ToolPolicyDeny,  // explicit — replaces the removed deny-default
								"exec":                 config.ToolPolicyDeny,  // explicit — replaces the removed deny-default
							},
						},
					},
				},
				{
					ID:   "jim",
					Type: config.AgentTypeCustom,
					Tools: &config.AgentToolsCfg{
						Builtin: config.AgentBuiltinToolsCfg{
							Policies: map[string]config.ToolPolicy{
								"exec":       config.ToolPolicyDeny,
								"fetch_url":  config.ToolPolicyAsk,
								"search_web": config.ToolPolicyAllow, // explicit — replaces the removed allow-default
							},
						},
					},
				},
			},
		},
	}
}

func parityCases() []parityCase {
	return []parityCase{
		// ava: mostly-deny fixture (Ava-like) — every tool below has an
		// explicit Policies entry in buildParityConfig; none rely on a
		// default any more (CLAUDE.md hard constraint 6).
		{"ava/ToolSearch(infra)", "ToolSearch", tools.ScopeGeneral, "ava", "custom", "allow"},
		{"ava/allowed", "search_web", tools.ScopeGeneral, "ava", "custom", "allow"},
		{"ava/denied-unlisted", "send_message", tools.ScopeGeneral, "ava", "custom", "deny"},
		{"ava/ask", "fetch_url", tools.ScopeGeneral, "ava", "custom", "ask"},
		{"ava/scopecore-unlisted", "exec", tools.ScopeCore, "ava", "custom", "deny"},
		{"ava/scopecore-allowed", "exec_allowed", tools.ScopeCore, "ava", "custom", "allow"},
		{"ava/global-deny-floor-wins", "send_message_blocked", tools.ScopeGeneral, "ava", "custom", "deny"},

		// jim: mostly-allow fixture (Jim-like) — same note as above.
		{"jim/ToolSearch(infra)", "ToolSearch", tools.ScopeGeneral, "jim", "custom", "allow"},
		{"jim/unlisted-allow", "search_web", tools.ScopeGeneral, "jim", "custom", "allow"},
		{"jim/explicit-deny", "exec", tools.ScopeCore, "jim", "custom", "deny"},
		{"jim/explicit-ask", "fetch_url", tools.ScopeGeneral, "jim", "custom", "ask"},
	}
}

// agentToolPolicyCfg builds the tools.ToolPolicyCfg the loop would use for the
// given agent from the parity config — the same construction
// pkg/agent/instance.go's agentToolsCfgToPolicy performs internally (minus the
// GodMode floor, which the parity cases below don't exercise), used here to
// drive FilterToolsByPolicy. There is no DefaultPolicy/GlobalDefaultPolicy
// field any more (CLAUDE.md hard constraint 6) — only the explicit
// Policies/GlobalPolicies maps are threaded through; a tool with no match on
// either side fails closed to "deny" inside tools.EffectiveToolPolicy itself.
func agentToolPolicyCfg(cfg *config.Config, agentID string) *tools.ToolPolicyCfg {
	out := &tools.ToolPolicyCfg{}
	if len(cfg.Sandbox.ToolPolicies) > 0 {
		gp := make(map[string]config.ToolPolicy, len(cfg.Sandbox.ToolPolicies))
		for k, v := range cfg.Sandbox.ToolPolicies {
			gp[k] = config.ToolPolicy(v)
		}
		out.GlobalPolicies = gp
	}
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		if ac.ID != agentID || ac.Tools == nil {
			continue
		}
		ap := make(map[string]config.ToolPolicy, len(ac.Tools.Builtin.Policies))
		for k, v := range ac.Tools.Builtin.Policies {
			ap[k] = v
		}
		out.Policies = ap
		break
	}
	return out
}

// TestResolveApprovalToolPolicy_FilterParity is the crux of the unification: for
// every (agent, tool) cell, the gateway approval-hook resolver and the agent-loop
// FilterToolsByPolicy verdict are IDENTICAL. FilterToolsByPolicy's verdict is the
// returned policyMap entry, or "deny" when the tool was dropped.
func TestResolveApprovalToolPolicy_FilterParity(t *testing.T) {
	cfg := buildParityConfig()

	for _, tc := range parityCases() {
		t.Run(tc.name, func(t *testing.T) {
			// Gateway resolver verdict (name + config only).
			gatewayVerdict := resolveApprovalToolPolicy(cfg, tc.toolName, tc.agentID)

			// Agent-loop filter verdict for the same tool (real scope).
			polCfg := agentToolPolicyCfg(cfg, tc.agentID)
			filtered, polMap := tools.FilterToolsByPolicy(
				[]tools.Tool{makeScopedToolForParity(tc.toolName, tc.scope)},
				tc.agentType, polCfg,
			)
			loopVerdict := "deny"
			if len(filtered) == 1 {
				loopVerdict = polMap[tc.toolName]
			}

			// They MUST be identical.
			assert.Equal(t, loopVerdict, gatewayVerdict,
				"gateway hook and FilterToolsByPolicy must agree for %s: loop=%s gateway=%s",
				tc.toolName, loopVerdict, gatewayVerdict)

			// And both must equal the documented expected verdict.
			assert.Equal(t, tc.wantPol, gatewayVerdict,
				"gateway verdict for %s must be %s", tc.toolName, tc.wantPol)
			assert.Equal(t, tc.wantPol, loopVerdict,
				"loop verdict for %s must be %s", tc.toolName, tc.wantPol)
		})
	}
}

// wildcardParityCase pins ONE wildcard scenario. Each carries its own *config.Config
// because the global floor is config-wide (a shared global "browser_*":deny would
// clobber the agent-allow scenario), so per-case configs keep the scenarios
// isolated while staying faithful to the browser_*/system.* key shapes.
type wildcardParityCase struct {
	name     string
	cfg      *config.Config
	toolName string
	scope    tools.ToolScope
	agentID  string
	wantPol  string
}

// wildcardAgentCfg builds a one-agent config with the given per-tool policies,
// plus an optional global floor map. There is no default-policy field any
// more (CLAUDE.md hard constraint 6) — each case below relies only on exact
// or wildcard matches in agentPolicies/globalFloor; a tool with no match on
// either side fails closed to "deny".
func wildcardAgentCfg(
	agentID string,
	agentPolicies map[string]config.ToolPolicy,
	globalFloor map[string]string,
) *config.Config {
	return &config.Config{
		Sandbox: config.OmnipusSandboxConfig{
			ToolPolicies: globalFloor,
		},
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{
					ID:   agentID,
					Type: config.AgentTypeCustom,
					Tools: &config.AgentToolsCfg{
						Builtin: config.AgentBuiltinToolsCfg{
							Policies: agentPolicies,
						},
					},
				},
			},
		},
	}
}

// TestResolveApprovalToolPolicy_WildcardParity (F4) pins the ONE input class whose
// gateway-gate verdict CHANGED under unification: wildcard policy keys. The OLD
// gateway resolved exact-match only and IGNORED these keys; the new gateway path
// honors them, matching FilterToolsByPolicy. Each case asserts an independent
// hard-coded expectation AND that the gateway resolver == FilterToolsByPolicy.
func TestResolveApprovalToolPolicy_WildcardParity(t *testing.T) {
	cases := []wildcardParityCase{
		{
			// (a) agent "browser_*":allow → matching tool allow.
			name: "agent browser_*=allow → browser_navigate allow",
			cfg: wildcardAgentCfg("wild",
				map[string]config.ToolPolicy{"browser_*": config.ToolPolicyAllow},
				nil),
			toolName: "browser_navigate",
			scope:    tools.ScopeGeneral,
			agentID:  "wild",
			wantPol:  "allow",
		},
		{
			// (b) global "browser_*":deny → matching tool deny (wildcard floor
			//     wins via strictest-wins; the agent has no competing entry).
			name: "global browser_*=deny → browser_navigate deny",
			cfg: wildcardAgentCfg("wild",
				nil,
				map[string]string{"browser_*": "deny"}),
			toolName: "browser_navigate",
			scope:    tools.ScopeGeneral,
			agentID:  "wild",
			wantPol:  "deny",
		},
		{
			// (c1) agent "system.*":allow → matching system tool allow.
			name: "agent system.*=allow → matching system tool allow",
			cfg: wildcardAgentCfg("wild",
				map[string]config.ToolPolicy{"system.*": config.ToolPolicyAllow},
				nil),
			toolName: "system.agent.list",
			scope:    tools.ScopeCore,
			agentID:  "wild",
			wantPol:  "allow",
		},
		{
			// (c2) SAME agent policy → NON-matching tool deny. "system.*" does
			// not cover "read_file", and there is no default-policy fallback
			// any more (CLAUDE.md hard constraint 6) — an uncovered tool fails
			// closed to "deny". This case pins that fail-closed guarantee
			// directly, rather than an implicit per-agent default.
			name: "agent system.*=allow → non-matching tool fails closed to deny",
			cfg: wildcardAgentCfg("wild",
				map[string]config.ToolPolicy{"system.*": config.ToolPolicyAllow},
				nil),
			toolName: "read_file",
			scope:    tools.ScopeGeneral,
			agentID:  "wild",
			wantPol:  "deny",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Gateway resolver verdict (now wildcard-aware).
			gatewayVerdict := resolveApprovalToolPolicy(tc.cfg, tc.toolName, tc.agentID)

			// Agent-loop filter verdict for the same tool/scope.
			polCfg := agentToolPolicyCfg(tc.cfg, tc.agentID)
			filtered, polMap := tools.FilterToolsByPolicy(
				[]tools.Tool{makeScopedToolForParity(tc.toolName, tc.scope)},
				"custom", polCfg,
			)
			loopVerdict := "deny"
			if len(filtered) == 1 {
				loopVerdict = polMap[tc.toolName]
			}

			// Independent hard-coded expectation (NOT new==new).
			assert.Equal(t, tc.wantPol, gatewayVerdict,
				"gateway wildcard verdict for %s must be %s", tc.toolName, tc.wantPol)
			assert.Equal(t, tc.wantPol, loopVerdict,
				"loop wildcard verdict for %s must be %s", tc.toolName, tc.wantPol)

			// And gateway == filter on the changed input class.
			assert.Equal(t, loopVerdict, gatewayVerdict,
				"gateway and FilterToolsByPolicy must agree on wildcard %s: loop=%s gateway=%s",
				tc.toolName, loopVerdict, gatewayVerdict)
		})
	}
}
