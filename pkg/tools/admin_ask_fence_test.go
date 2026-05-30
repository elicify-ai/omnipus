// Privilege-escalation invariant tests for the Tool Registry redesign
// (FR-061, FR-005, FR-079). These live in `pkg/tools/` — the package
// where `FilterToolsByPolicy` is defined — so that the security
// invariants are proven against the real filter implementation as it
// evolves through Phase A1.
//
// Wave A2 deliverable: the security lane provides the *invariant tests*;
// A1 is responsible for landing the matching `RequiresAdminAsk()` method
// on `Tool` and wiring `policy.ApplyAdminAskFence` into the resolver. The
// `TestFilterToolsByPolicy_AdminAskFenceOnCustomAgents` test exercises
// the policy.ApplyAdminAskFence helper directly so the invariant has a
// failing-but-buildable home today and a passing home once A1 lands.

package tools

import (
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/policy"
)

// TestFilterToolsByPolicy_GlobalDenyOverridesAgentAllow asserts FR-005:
// `global × agent × deny>ask>allow`. When the operator-global layer
// denies a tool, the agent-level allow MUST NOT override it. This is a
// regression guard around `compositor.go:286-374` so that A1's wildcard
// resolver extension does not weaken the precedence rule.
func TestFilterToolsByPolicy_GlobalDenyOverridesAgentAllow(t *testing.T) {
	t.Parallel()

	tools := []Tool{
		makeScopedTool("system.config.set", ScopeCore),
		makeScopedTool("exec", ScopeCore),
		makeScopedTool("web_search", ScopeGeneral),
	}
	cfg := &ToolPolicyCfg{
		DefaultPolicy: "allow",
		Policies: map[string]string{
			"web_search": "allow", // agent says allow…
		},
		GlobalPolicies: map[string]string{
			"web_search": "deny", // …operator says deny.
		},
		GlobalDefaultPolicy: "allow",
	}
	got, policyMap := FilterToolsByPolicy(tools, "system", cfg)
	for _, x := range got {
		if x.Name() == "web_search" {
			t.Fatalf("FR-005: global deny must remove tool; got %q in result", x.Name())
		}
	}
	if _, ok := policyMap["web_search"]; ok {
		t.Fatalf("FR-005: global-denied tool must not appear in policyMap")
	}
}

// TestFilterToolsByPolicy_GlobalDefaultDenyStripsAll asserts that when
// `GlobalDefaultPolicy = deny` and no explicit allow entries are provided,
// every tool is stripped from the result regardless of the agent's local
// policy. This is the operator's emergency-shut-off invariant.
func TestFilterToolsByPolicy_GlobalDefaultDenyStripsAll(t *testing.T) {
	t.Parallel()

	tools := []Tool{
		makeScopedTool("exec", ScopeCore),
		makeScopedTool("web_search", ScopeGeneral),
	}
	cfg := &ToolPolicyCfg{
		DefaultPolicy:       "allow",
		GlobalDefaultPolicy: "deny",
	}
	got, policyMap := FilterToolsByPolicy(tools, "core", cfg)
	if len(got) != 0 {
		names := make([]string, 0, len(got))
		for _, x := range got {
			names = append(names, x.Name())
		}
		t.Fatalf("global default deny should strip all tools; got %v", names)
	}
	if len(policyMap) != 0 {
		t.Fatalf("global default deny should yield empty policyMap; got %v", policyMap)
	}
}

// TestFilterToolsByPolicy_AdminAskFenceOnCustomAgents — FR-061.
//
// The fence downgrades `allow` → `ask` for a `RequiresAdminAsk` tool on
// a non-core (custom) agent. The test exercises the policy package's
// pure helper directly to assert the invariant at the contract level;
// once A1 lands the resolver wiring, the same invariant holds end-to-end
// through `FilterToolsByPolicy`.
func TestFilterToolsByPolicy_AdminAskFenceOnCustomAgents(t *testing.T) {
	t.Parallel()

	requiresAdminAsk := func(name string) bool {
		switch name {
		case "system.config.set", "system.agent.create", "system.exec":
			return true
		}
		return false
	}
	isCoreAgent := func(id string) bool {
		switch id {
		case "ava", "billy", "celia", "dax", "eve":
			return true
		}
		return false
	}

	cases := []struct {
		name        string
		effective   string
		toolName    string
		agentID     string
		wantPolicy  string
		wantFenceOn bool
	}{
		// (1) custom + sysagent + allow → ask
		{
			name:        "custom_agent_allow_for_admin_tool_downgrades_to_ask",
			effective:   "allow",
			toolName:    "system.config.set",
			agentID:     "my-custom-agent",
			wantPolicy:  "ask",
			wantFenceOn: true,
		},
		// (2) core + sysagent + allow → allow (NOT downgraded)
		{
			name:        "core_agent_allow_for_admin_tool_stays_allow",
			effective:   "allow",
			toolName:    "system.config.set",
			agentID:     "ava",
			wantPolicy:  "allow",
			wantFenceOn: false,
		},
		// (3) custom + non-sysagent + allow → allow
		{
			name:        "custom_agent_allow_for_benign_tool_stays_allow",
			effective:   "allow",
			toolName:    "web_search",
			agentID:     "my-custom-agent",
			wantPolicy:  "allow",
			wantFenceOn: false,
		},
		// Boundary cases: deny stays deny, ask stays ask.
		{
			name:        "deny_dominates_fence",
			effective:   "deny",
			toolName:    "system.config.set",
			agentID:     "my-custom-agent",
			wantPolicy:  "deny",
			wantFenceOn: false,
		},
		{
			name:        "ask_unchanged_by_fence",
			effective:   "ask",
			toolName:    "system.config.set",
			agentID:     "my-custom-agent",
			wantPolicy:  "ask",
			wantFenceOn: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, fenceApplied := policy.ApplyAdminAskFence(
				tc.effective, tc.toolName, tc.agentID,
				requiresAdminAsk, isCoreAgent,
			)
			if got != tc.wantPolicy || fenceApplied != tc.wantFenceOn {
				t.Errorf("ApplyAdminAskFence(%q, %q, %q) = (%q, %v); want (%q, %v)",
					tc.effective, tc.toolName, tc.agentID,
					got, fenceApplied, tc.wantPolicy, tc.wantFenceOn)
			}
		})
	}
}

// TestFilterToolsByPolicy_PreservesScopeCoreGate — FR-006. Asserts that
// the scope-gate semantics in `compositor.go:286-374` survive: a core-scoped
// tool reaches a custom agent only when its effective policy is not deny.
// This test guards against A1's planned changes (deleting ScopeSystem,
// adjusting passesScopeGate) accidentally weakening the gate for core
// scope tools.
func TestFilterToolsByPolicy_PreservesScopeCoreGate(t *testing.T) {
	t.Parallel()

	tools := []Tool{makeScopedTool("exec", ScopeCore)}

	// Custom agent with default-allow → core-scoped tool should be present.
	cfg := &ToolPolicyCfg{DefaultPolicy: "allow"}
	got, _ := FilterToolsByPolicy(tools, "custom", cfg)
	if len(got) != 1 {
		t.Fatalf("FR-006: custom agent + default allow + core-scoped tool: want 1 tool, got %d", len(got))
	}

	// Custom agent with explicit deny → core-scoped tool stripped.
	cfg = &ToolPolicyCfg{
		DefaultPolicy: "allow",
		Policies:      map[string]string{"exec": "deny"},
	}
	got, _ = FilterToolsByPolicy(tools, "custom", cfg)
	if len(got) != 0 {
		t.Fatalf("FR-006: custom agent + per-tool deny: want 0 tools, got %d", len(got))
	}
}
