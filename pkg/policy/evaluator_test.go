// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package policy_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapicom-ai/omnipus/pkg/policy"
)

// makeEvaluator builds an Evaluator from a default policy string and optional agent map.
func makeEvaluator(defaultPolicy policy.DefaultPolicy, agents map[string]policy.AgentPolicy) *policy.Evaluator {
	return policy.NewEvaluator(&policy.SecurityConfig{
		DefaultPolicy: defaultPolicy,
		Agents:        agents,
	})
}

// TestPolicyEvaluator_DenyByDefault validates that deny-by-default blocks all tool calls
// when an agent has no tools.allow list configured.
// Traces to: wave2-security-layer-spec.md line 788 (TestPolicyEvaluator_DenyByDefault)
// BDD: Scenario: No tools available without explicit allow (spec line 529)
func TestPolicyEvaluator_DenyByDefault(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 529 (Scenario: No tools without explicit allow)
	evaluator := makeEvaluator(policy.PolicyDeny, nil) // no agent-specific policies

	tests := []struct {
		name    string
		agentID string
		tool    string
	}{
		{name: "search_web denied without allow list", agentID: "researcher", tool: "search_web"},
		{name: "exec denied without allow list", agentID: "assistant", tool: "exec"},
		{name: "file.write denied without allow list", agentID: "researcher", tool: "file.write"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			decision := evaluator.EvaluateTool(tc.agentID, tc.tool)
			assert.False(t, decision.Allowed,
				"tool %q should be denied with deny-by-default and no allow list", tc.tool)
			assert.Contains(t, decision.PolicyRule, "default_policy is 'deny'")
			assert.Contains(t, decision.PolicyRule, tc.tool)
		})
	}
}

// TestPolicyEvaluator_AllowByDefault validates explicit allow-by-default behavior.
// Traces to: wave2-security-layer-spec.md line 789 (TestPolicyEvaluator_AllowByDefault)
// BDD: Scenario: Backward-compatible allow-by-default (spec line 539)
func TestPolicyEvaluator_AllowByDefault(t *testing.T) {
	t.Run("explicit allow default", func(t *testing.T) {
		evaluator := makeEvaluator(policy.PolicyAllow, nil)
		decision := evaluator.EvaluateTool("researcher", "search_web")
		assert.True(t, decision.Allowed,
			"tool should be allowed with explicit default_policy=allow")
		assert.Contains(t, decision.PolicyRule, "allow",
			"allow decision should explain why it was allowed")
	})

	t.Run("empty string defaults to deny", func(t *testing.T) {
		// Deny-by-default per CLAUDE.md hard constraint #6
		evaluator := makeEvaluator("", nil)
		decision := evaluator.EvaluateTool("researcher", "search_web")
		assert.False(t, decision.Allowed,
			"tool should be denied with empty default_policy (deny-by-default)")
		assert.Contains(t, decision.PolicyRule, "deny",
			"deny decision should explain why it was denied")
	})
}

// TestPolicyEvaluator_ToolAllowList validates that tools not in the allow list are denied.
// Traces to: wave2-security-layer-spec.md line 790 (TestPolicyEvaluator_ToolAllowList)
// BDD: Scenario: Tool not in allow list is denied (spec line 465)
func TestPolicyEvaluator_ToolAllowList(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 465 (Scenario: Tool not in allow list)
	agents := map[string]policy.AgentPolicy{
		"researcher": {
			Tools: policy.AgentToolsPolicy{
				Allow: []string{"search_web", "fetch_url"},
			},
		},
	}
	evaluator := makeEvaluator(policy.PolicyAllow, agents)

	t.Run("search_web in allow list passes", func(t *testing.T) {
		decision := evaluator.EvaluateTool("researcher", "search_web")
		assert.True(t, decision.Allowed)
		assert.Contains(t, decision.PolicyRule, "search_web")
		assert.Contains(t, decision.PolicyRule, "researcher")
	})

	t.Run("fetch_url in allow list passes", func(t *testing.T) {
		decision := evaluator.EvaluateTool("researcher", "fetch_url")
		assert.True(t, decision.Allowed)
	})

	t.Run("exec not in allow list is denied", func(t *testing.T) {
		// Traces to: wave2-security-layer-spec.md line 465 (BDD: exec not in tools.allow)
		decision := evaluator.EvaluateTool("researcher", "exec")
		assert.False(t, decision.Allowed)
		assert.Contains(t, decision.PolicyRule, "exec")
		assert.Contains(t, decision.PolicyRule, "researcher")
	})

	t.Run("file.write not in allow list is denied", func(t *testing.T) {
		decision := evaluator.EvaluateTool("researcher", "file.write")
		assert.False(t, decision.Allowed)
		assert.Contains(t, decision.PolicyRule, "not in tools.allow")
	})

	t.Run("empty tools.allow denies all tools", func(t *testing.T) {
		// Traces to: wave2-security-layer-spec.md line 304 (empty tools.allow = allow nothing)
		agentsEmpty := map[string]policy.AgentPolicy{
			"researcher": {
				Tools: policy.AgentToolsPolicy{
					Allow: []string{}, // explicit empty = no tools
				},
			},
		}
		ev := makeEvaluator(policy.PolicyAllow, agentsEmpty)
		decision := ev.EvaluateTool("researcher", "search_web")
		assert.False(t, decision.Allowed,
			"explicit empty tools.allow should deny all tools even if default is allow")
	})
}

// TestPolicyEvaluator_ToolDenyList validates that tools in the deny list are blocked
// even when default_policy is "allow".
// Traces to: wave2-security-layer-spec.md line 791 (TestPolicyEvaluator_ToolDenyList)
// BDD: Scenario: Tool in deny list is blocked even if default is allow (spec line 476)
func TestPolicyEvaluator_ToolDenyList(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 476 (Scenario: Tool in deny list blocked)
	agents := map[string]policy.AgentPolicy{
		"researcher": {
			Tools: policy.AgentToolsPolicy{
				Deny: []string{"exec", "file.write"},
			},
		},
	}
	evaluator := makeEvaluator(policy.PolicyAllow, agents)

	t.Run("exec in deny list is blocked", func(t *testing.T) {
		decision := evaluator.EvaluateTool("researcher", "exec")
		assert.False(t, decision.Allowed)
		assert.Contains(t, decision.PolicyRule, "exec")
		assert.Contains(t, decision.PolicyRule, "tools.deny")
	})

	t.Run("file.write in deny list is blocked", func(t *testing.T) {
		decision := evaluator.EvaluateTool("researcher", "file.write")
		assert.False(t, decision.Allowed)
		assert.Contains(t, decision.PolicyRule, "tools.deny")
	})

	t.Run("search_web not in deny list is allowed", func(t *testing.T) {
		decision := evaluator.EvaluateTool("researcher", "search_web")
		assert.True(t, decision.Allowed)
	})
}

// TestPolicyEvaluator_DenyPrecedence validates deny takes precedence when a tool
// appears in both tools.allow and tools.deny.
// Traces to: wave2-security-layer-spec.md line 792 (TestPolicyEvaluator_DenyPrecedence)
// BDD: Scenario: Deny takes precedence over allow (spec line 485)
func TestPolicyEvaluator_DenyPrecedence(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 485 (Scenario: Deny takes precedence)
	agents := map[string]policy.AgentPolicy{
		"researcher": {
			Tools: policy.AgentToolsPolicy{
				Allow: []string{"exec", "search_web"},
				Deny:  []string{"exec"}, // exec in both → deny wins
			},
		},
	}
	evaluator := makeEvaluator(policy.PolicyAllow, agents)

	t.Run("exec in both allow and deny is blocked (deny wins)", func(t *testing.T) {
		decision := evaluator.EvaluateTool("researcher", "exec")
		assert.False(t, decision.Allowed,
			"deny must take precedence over allow when both lists contain the tool")
		assert.Contains(t, decision.PolicyRule, "deny takes precedence")
	})

	t.Run("search_web only in allow is still permitted", func(t *testing.T) {
		decision := evaluator.EvaluateTool("researcher", "search_web")
		assert.True(t, decision.Allowed)
	})
}

// TestPolicyEvaluator_ExplainableDecision validates every policy decision includes
// a human-readable policy_rule explaining the match.
// Traces to: wave2-security-layer-spec.md line 793 (TestPolicyEvaluator_ExplainableDecision)
// BDD: Scenario: Denial includes matching rule + Allow includes matching rule (spec line 623–640)
func TestPolicyEvaluator_ExplainableDecision(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 623 (Scenario: Denial includes matching rule)
	agents := map[string]policy.AgentPolicy{
		"researcher": {
			Tools: policy.AgentToolsPolicy{
				Allow: []string{"search_web"},
			},
		},
	}
	evaluator := makeEvaluator(policy.PolicyDeny, agents)

	t.Run("deny decision includes full policy_rule string", func(t *testing.T) {
		decision := evaluator.EvaluateTool("researcher", "exec")
		assert.False(t, decision.Allowed)
		assert.NotEmpty(t, decision.PolicyRule,
			"denied decision must include a non-empty policy_rule")
		assert.Contains(t, decision.PolicyRule, "exec",
			"policy_rule must reference the denied tool")
		assert.Contains(t, decision.PolicyRule, "researcher",
			"policy_rule must reference the agent")
	})

	t.Run("allow decision includes policy_rule string", func(t *testing.T) {
		// Traces to: wave2-security-layer-spec.md line 633 (Scenario: Allow includes matching rule)
		decision := evaluator.EvaluateTool("researcher", "search_web")
		assert.True(t, decision.Allowed)
		assert.NotEmpty(t, decision.PolicyRule,
			"allowed decision must also include a policy_rule")
		assert.Contains(t, decision.PolicyRule, "search_web")
	})

	t.Run("deny by default includes policy_rule", func(t *testing.T) {
		// Traces to: wave2-security-layer-spec.md line 186 (multiple rules: first match wins)
		ev := makeEvaluator(policy.PolicyDeny, nil)
		decision := ev.EvaluateTool("researcher", "exec")
		assert.False(t, decision.Allowed)
		assert.NotEmpty(t, decision.PolicyRule)
	})
}

// TestPolicyEvaluator_GlobToolNames validates glob pattern matching for tool names
// in both agent-level allow/deny lists and global tool_policies.
// Closes: issue #79
// README citation: "Three-tier tool policy per agent — allow / ask / deny with glob patterns"
func TestPolicyEvaluator_GlobToolNames(t *testing.T) {
	t.Run("fs.* glob in allow matches fs.read", func(t *testing.T) {
		agents := map[string]policy.AgentPolicy{
			"worker": {
				Tools: policy.AgentToolsPolicy{
					Allow: []string{"fs.*"},
				},
			},
		}
		ev := makeEvaluator(policy.PolicyDeny, agents)
		d := ev.EvaluateTool("worker", "fs.read")
		assert.True(t, d.Allowed, "fs.* should match fs.read")
	})

	t.Run("fs.* glob in allow matches fs.write", func(t *testing.T) {
		agents := map[string]policy.AgentPolicy{
			"worker": {
				Tools: policy.AgentToolsPolicy{
					Allow: []string{"fs.*"},
				},
			},
		}
		ev := makeEvaluator(policy.PolicyDeny, agents)
		d := ev.EvaluateTool("worker", "fs.write")
		assert.True(t, d.Allowed, "fs.* should match fs.write")
	})

	t.Run("fs.* glob in allow matches fs.list", func(t *testing.T) {
		agents := map[string]policy.AgentPolicy{
			"worker": {
				Tools: policy.AgentToolsPolicy{
					Allow: []string{"fs.*"},
				},
			},
		}
		ev := makeEvaluator(policy.PolicyDeny, agents)
		d := ev.EvaluateTool("worker", "fs.list")
		assert.True(t, d.Allowed, "fs.* should match fs.list")
	})

	t.Run("fs.* glob does NOT match fsx.read (prefix boundary)", func(t *testing.T) {
		agents := map[string]policy.AgentPolicy{
			"worker": {
				Tools: policy.AgentToolsPolicy{
					Allow: []string{"fs.*"},
				},
			},
		}
		ev := makeEvaluator(policy.PolicyDeny, agents)
		d := ev.EvaluateTool("worker", "fsx.read")
		assert.False(t, d.Allowed, "fs.* must not match fsx.read (different prefix)")
	})

	t.Run("deny beats allow when deny=fs.delete, allow=fs.*", func(t *testing.T) {
		agents := map[string]policy.AgentPolicy{
			"worker": {
				Tools: policy.AgentToolsPolicy{
					Allow: []string{"fs.*"},
					Deny:  []string{"fs.delete"},
				},
			},
		}
		ev := makeEvaluator(policy.PolicyDeny, agents)

		d := ev.EvaluateTool("worker", "fs.delete")
		assert.False(t, d.Allowed, "fs.delete in deny must block even though fs.* is in allow")
		assert.Contains(t, d.PolicyRule, "deny")

		// Other fs.* tools still pass.
		d2 := ev.EvaluateTool("worker", "fs.read")
		assert.True(t, d2.Allowed, "fs.read not in deny should still be allowed via fs.* glob")
	})

	t.Run("deny glob fs.* beats allow=fs.*", func(t *testing.T) {
		agents := map[string]policy.AgentPolicy{
			"worker": {
				Tools: policy.AgentToolsPolicy{
					Allow: []string{"fs.*"},
					Deny:  []string{"fs.*"},
				},
			},
		}
		ev := makeEvaluator(policy.PolicyDeny, agents)
		d := ev.EvaluateTool("worker", "fs.read")
		assert.False(t, d.Allowed, "deny glob fs.* takes precedence over allow glob fs.*")
	})

	t.Run("literal tool name in allow still works (backward compat)", func(t *testing.T) {
		agents := map[string]policy.AgentPolicy{
			"worker": {
				Tools: policy.AgentToolsPolicy{
					Allow: []string{"search_web"},
				},
			},
		}
		ev := makeEvaluator(policy.PolicyDeny, agents)
		d := ev.EvaluateTool("worker", "search_web")
		assert.True(t, d.Allowed, "exact literal 'web_search' in allow must still match")

		d2 := ev.EvaluateTool("worker", "fetch_url")
		assert.False(t, d2.Allowed, "web_fetch not in allow list")
	})

	t.Run("global tool_policies with fs.* glob → ask mode", func(t *testing.T) {
		ev := policy.NewEvaluator(&policy.SecurityConfig{
			DefaultPolicy: policy.PolicyAllow,
			ToolPolicies: map[string]policy.ToolPolicy{
				"fs.*": policy.ToolPolicyAsk,
			},
		})

		d := ev.EvaluateTool("worker", "fs.read")
		assert.True(t, d.Allowed, "fs.read matched by fs.* glob should be allowed")
		assert.Equal(t, "ask", d.Policy, "global fs.* policy=ask should be reflected in decision")

		d2 := ev.EvaluateTool("worker", "fs.write")
		assert.True(t, d2.Allowed)
		assert.Equal(t, "ask", d2.Policy)

		// Tool outside the glob is not affected by the fs.* policy.
		d3 := ev.EvaluateTool("worker", "search_web")
		assert.True(t, d3.Allowed, "web_search not matched by fs.* so not elevated to ask")
		assert.NotEqual(t, "ask", d3.Policy)
	})

	t.Run("global tool_policies with fs.* glob → deny", func(t *testing.T) {
		ev := policy.NewEvaluator(&policy.SecurityConfig{
			DefaultPolicy: policy.PolicyAllow,
			ToolPolicies: map[string]policy.ToolPolicy{
				"fs.*": policy.ToolPolicyDeny,
			},
		})

		d := ev.EvaluateTool("worker", "fs.write")
		assert.False(t, d.Allowed, "global deny via fs.* glob should block fs.write")

		// Tool outside glob still allowed by default_policy.
		d2 := ev.EvaluateTool("worker", "search_web")
		assert.True(t, d2.Allowed)
	})

	t.Run("question-mark wildcard matches exactly one character", func(t *testing.T) {
		agents := map[string]policy.AgentPolicy{
			"worker": {
				Tools: policy.AgentToolsPolicy{
					Allow: []string{"fs.?ead"},
				},
			},
		}
		ev := makeEvaluator(policy.PolicyDeny, agents)

		d := ev.EvaluateTool("worker", "fs.read")
		assert.True(t, d.Allowed, "fs.?ead should match fs.read (? = 'r')")

		d2 := ev.EvaluateTool("worker", "fs.lead")
		assert.True(t, d2.Allowed, "fs.?ead should match fs.lead (? = 'l')")

		d3 := ev.EvaluateTool("worker", "fs.head")
		assert.True(t, d3.Allowed, "fs.?ead should match fs.head (? = 'h')")

		d4 := ev.EvaluateTool("worker", "fs.write")
		assert.False(t, d4.Allowed, "fs.?ead must not match fs.write")

		d5 := ev.EvaluateTool("worker", "fs.aread")
		assert.False(t, d5.Allowed, "fs.?ead must not match fs.aread (? is exactly one char)")
	})

	t.Run("empty deny pattern never matches any tool (edge case)", func(t *testing.T) {
		agents := map[string]policy.AgentPolicy{
			"worker": {
				Tools: policy.AgentToolsPolicy{
					Allow: []string{"search_web"},
					Deny:  []string{""}, // empty pattern
				},
			},
		}
		ev := makeEvaluator(policy.PolicyDeny, agents)
		d := ev.EvaluateTool("worker", "search_web")
		// Empty pattern only matches empty string, so web_search should be allowed.
		assert.True(t, d.Allowed, "empty deny pattern should not match non-empty tool names")
	})

	t.Run("star-only pattern matches everything", func(t *testing.T) {
		agents := map[string]policy.AgentPolicy{
			"worker": {
				Tools: policy.AgentToolsPolicy{
					Allow: []string{"*"},
				},
			},
		}
		ev := makeEvaluator(policy.PolicyDeny, agents)
		for _, tool := range []string{"search_web", "fs.read", "exec", "browser.navigate"} {
			d := ev.EvaluateTool("worker", tool)
			assert.True(t, d.Allowed, "* should match any tool name: %s", tool)
		}
	})

	t.Run("table-driven glob allow/deny combinations", func(t *testing.T) {
		type row struct {
			allow []string
			deny  []string
			tool  string
			want  bool
		}
		rows := []row{
			// Exact match works.
			{allow: []string{"search_web"}, tool: "search_web", want: true},
			{allow: []string{"search_web"}, tool: "fetch_url", want: false},
			// fs.* glob.
			{allow: []string{"fs.*"}, tool: "fs.read", want: true},
			{allow: []string{"fs.*"}, tool: "fs.write", want: true},
			{allow: []string{"fs.*"}, tool: "fsx.read", want: false},
			// deny beats allow.
			{allow: []string{"fs.*"}, deny: []string{"fs.delete"}, tool: "fs.delete", want: false},
			{allow: []string{"fs.*"}, deny: []string{"fs.delete"}, tool: "fs.read", want: true},
			// Star-only.
			{allow: []string{"*"}, tool: "anything", want: true},
			// Deny-only with glob.
			{deny: []string{"exec.*"}, tool: "exec.shell", want: false},
			{deny: []string{"exec.*"}, tool: "search_web", want: true},
		}

		for _, r := range rows {
			agents := map[string]policy.AgentPolicy{
				"agent": {
					Tools: policy.AgentToolsPolicy{
						Allow: r.allow,
						Deny:  r.deny,
					},
				},
			}
			// Use allow-by-default so deny-only rows behave correctly.
			ev := makeEvaluator(policy.PolicyAllow, agents)
			d := ev.EvaluateTool("agent", r.tool)
			assert.Equal(t, r.want, d.Allowed,
				"allow=%v deny=%v tool=%q: expected allowed=%v", r.allow, r.deny, r.tool, r.want)
		}
	})
}
